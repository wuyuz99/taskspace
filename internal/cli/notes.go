package cli

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/aw-tool/aw/internal/task"
	"github.com/spf13/cobra"
)

// notes reads a task's brief and handoff, moving a legacy record's inline
// Markdown into files on the way past.
func (a *App) notes(t *task.Task) (brief, handoff task.Note, err error) {
	if _, err := a.Store.MigrateNotes(t); err != nil {
		return brief, handoff, err
	}
	if brief, err = a.Store.ReadNote(t.Name, task.BriefFile); err != nil {
		return brief, handoff, err
	}
	handoff, err = a.Store.ReadNote(t.Name, task.HandoffFile)
	return brief, handoff, err
}

type taskDetails struct{ title, ticket, ticketURL, briefFile string }

func (d *taskDetails) flags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&d.title, "title", "", "human-readable task title")
	cmd.Flags().StringVar(&d.ticket, "ticket", "", "ticket key, independent of workspace name (e.g. PAY-142)")
	cmd.Flags().StringVar(&d.ticketURL, "ticket-url", "", "ticket HTTP(S) URL; no ticket service is contacted")
	cmd.Flags().StringVar(&d.briefFile, "brief-file", "", "read the task brief from a Markdown file")
}

func (d *taskDetails) readBrief() (string, error) {
	if d.ticketURL != "" {
		u, err := url.Parse(d.ticketURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", fmt.Errorf("ticket URL must be an absolute HTTP(S) URL")
		}
	}
	if strings.ContainsAny(d.title+d.ticket, "\r\n\t\x1b") {
		return "", fmt.Errorf("title and ticket must be single-line text")
	}
	if d.briefFile == "" {
		return "", nil
	}
	b, err := os.ReadFile(d.briefFile)
	if err != nil {
		return "", fmt.Errorf("read brief: %w", err)
	}
	return string(b), nil
}

// apply sets the record's identity fields. The brief is not one of them; it is
// written to the task's note file once the record itself exists.
func (d *taskDetails) apply(cmd *cobra.Command, t *task.Task) {
	if cmd.Flags().Changed("title") {
		t.Title = d.title
	}
	if cmd.Flags().Changed("ticket") {
		t.Ticket = d.ticket
	}
	if cmd.Flags().Changed("ticket-url") {
		t.TicketURL = d.ticketURL
	}
}

// applyBrief stores an imported brief, after the task record is on disk.
func (a *App) applyBrief(cmd *cobra.Command, t *task.Task, brief string) error {
	if !cmd.Flags().Changed("brief-file") {
		return nil
	}
	return a.Store.WriteNote(t.Name, task.BriefFile, brief)
}

func (a *App) newTaskUpdateCmd() *cobra.Command {
	var d taskDetails
	cmd := &cobra.Command{Use: "update NAME", Short: "Update a task's title, ticket or brief", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			brief, err := d.readBrief()
			if err != nil {
				return err
			}
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			d.apply(cmd, t)
			if err := a.Store.Save(t); err != nil {
				return err
			}
			if err := a.applyBrief(cmd, t, brief); err != nil {
				return err
			}
			a.printf("updated task %s\n", t.Name)
			return nil
		}}
	d.flags(cmd)
	return cmd
}

func (a *App) newTaskNoteCmd(handoff bool) *cobra.Command {
	name := "brief"
	if handoff {
		name = "handoff"
	}
	var file string
	var clear bool
	var edit bool
	noteFile := task.BriefFile
	if handoff {
		noteFile = task.HandoffFile
	}
	cmd := &cobra.Command{Use: name + " NAME [TEXT]", Short: "Show or replace the task's " + name,
		Long: "With no text or flags, print the stored Markdown. TEXT or --file replaces it; --clear removes it.\n\n" +
			"The note is a Markdown file in the task's .context directory, beside its\n" +
			"worktrees, so anything working in one reaches it as ../.context/" + noteFile + ".\n" +
			"Notes survive aw clean and are deleted with the task record.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.loadTask(args[0])
			if err != nil {
				return err
			}
			if _, err := a.Store.MigrateNotes(t); err != nil {
				return err
			}
			modes := 0
			if len(args) == 2 {
				modes++
			}
			if cmd.Flags().Changed("file") {
				modes++
			}
			if clear {
				modes++
			}
			if edit {
				modes++
			}
			if modes > 1 {
				return fmt.Errorf("choose only one of TEXT, --file, --clear or --edit")
			}
			if edit {
				return a.editTaskNote(cmd.Context(), t, noteFile)
			}
			if modes == 0 {
				stored, err := a.Store.ReadNote(t.Name, noteFile)
				if err != nil {
					return err
				}
				// Exported verbatim so the Markdown round-trips through --file,
				// but never without a final newline: the shell prompt would
				// otherwise land on the last line of the note.
				out := stored.Text
				if out != "" && !strings.HasSuffix(out, "\n") {
					out += "\n"
				}
				_, err = fmt.Fprint(a.Out, out)
				return err
			}
			var note string
			if len(args) == 2 {
				note = args[1]
			}
			if cmd.Flags().Changed("file") {
				b, err := os.ReadFile(file)
				if err != nil {
					return fmt.Errorf("read %s: %w", name, err)
				}
				note = string(b)
			}
			if err := a.Store.WriteNote(t.Name, noteFile, note); err != nil {
				return err
			}
			if note == "" {
				a.printf("cleared %s for %s\n", name, t.Name)
				return nil
			}
			a.printf("updated %s for %s (%s)\n", name, t.Name, a.Store.NotePath(t.Name, noteFile))
			return nil
		}}
	cmd.Flags().StringVar(&file, "file", "", "read Markdown from a file")
	cmd.Flags().BoolVar(&clear, "clear", false, "clear the stored note")
	cmd.Flags().BoolVar(&edit, "edit", false, "edit Markdown using VISUAL, EDITOR, or vi")
	return cmd
}

// displayText strips the control characters a terminal would act on.
//
// A brief is imported from an arbitrary file, and it is printed on the way into
// attach, open and shell — the moment before the terminal is handed to tmux. An
// escape sequence in one would retitle the tab or worse, so nothing but tabs
// and newlines survives to the screen. The stored note keeps every byte: this
// is a rendering rule, not a validation rule.
func displayText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func oneLine(s string) string { return strings.Join(strings.Fields(displayText(s)), " ") }

// summaryWidth keeps `aw task list` inside an 80-column terminal: the row
// already carries a workspace, ticket, title, timestamp and repo names.
const summaryWidth = 48

func noteSummary(note string) string {
	for _, line := range strings.Split(note, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := []rune(oneLine(line))
		if len(r) > summaryWidth {
			return string(r[:summaryWidth-3]) + "..."
		}
		return string(r)
	}
	return ""
}

// handoffTime renders a note's modification time. A note that does not exist
// has no time, so a cleared handoff shows nothing rather than the moment it was
// cleared.
func handoffTime(n task.Note) string {
	if n.UpdatedAt.IsZero() {
		return "-"
	}
	return n.UpdatedAt.UTC().Format("2006-01-02 15:04")
}

func (a *App) printTaskContext(t *task.Task) {
	if t.Title != "" {
		a.printf("title: %s\n", oneLine(t.Title))
	}
	if t.Ticket != "" {
		a.printf("ticket: %s\n", oneLine(t.Ticket))
	}
	if t.TicketURL != "" {
		a.printf("ticket URL: %s\n", oneLine(t.TicketURL))
	}
	brief, handoff, err := a.notes(t)
	if err != nil {
		// Context is a courtesy on the way into a workspace; failing to read it
		// must not stop you getting there.
		a.warnf("aw: could not read task notes: %v\n", err)
		return
	}
	if brief.Text != "" {
		a.printf("\nTask brief\n%s\n", strings.TrimRight(displayText(brief.Text), "\n"))
	}
	if handoff.Text != "" {
		a.printf("\nLocal handoff (updated %s UTC)\n%s\n", handoffTime(handoff), strings.TrimRight(displayText(handoff.Text), "\n"))
	}
}
