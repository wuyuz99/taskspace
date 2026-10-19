package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/task"
)

// notes reads a task's brief and handoff, moving a legacy record's inline
// Markdown into files on the way past.
//
// Reading never waits: if another command has the task, the inline text is
// shown from the record and moved another time.
func (a *App) notes(ctx context.Context, t *task.Task) (brief, handoff task.Note, err error) {
	try, cancel := context.WithCancel(ctx)
	cancel() // one attempt at the lock, unless ctx already holds it
	if _, err := a.Store.MigrateNotes(try, t); err != nil && try.Err() == nil {
		return brief, handoff, err
	}
	if brief, err = a.Store.ReadNote(t.Name, task.BriefFile); err != nil {
		return brief, handoff, err
	}
	if handoff, err = a.Store.ReadNote(t.Name, task.HandoffFile); err != nil {
		return brief, handoff, err
	}
	// Not moved yet; a note file, when there is one, is the newer copy.
	if brief.Text == "" {
		brief.Text = t.LegacyBrief
	}
	if handoff.Text == "" && t.LegacyHandoff != "" {
		handoff.Text = t.LegacyHandoff
		if t.LegacyHandoffUpdatedAt != nil {
			handoff.UpdatedAt = *t.LegacyHandoffUpdatedAt
		}
	}
	return brief, handoff, nil
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
	var agent agentOptions
	var resume resumeOptions
	cmd := &cobra.Command{Use: "update NAME", Short: "Update a task's title, ticket, brief or root agent", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			brief, err := d.readBrief()
			if err != nil {
				return err
			}
			if err := agent.validate(cmd); err != nil {
				return err
			}
			if err := resume.validate(cmd); err != nil {
				return err
			}
			t, unlock, err := a.lockTask(cmd, args[0], false)
			if err != nil {
				return err
			}
			defer unlock()
			d.apply(cmd, t)
			before := t.Agent
			setAgent := agent.apply(cmd, t)
			var resumeCleared bool
			t.Resume, resumeCleared = resume.apply(cmd, t.Resume, setAgent && !slices.Equal(before, t.Agent))
			if len(t.Resume) > 0 && len(t.Agent) == 0 {
				return fmt.Errorf("--resume resumes the task's agent, and it has none; give one with --agent")
			}
			if err := a.Store.Save(t); err != nil {
				return err
			}
			if err := a.applyBrief(cmd, t, brief); err != nil {
				return err
			}
			a.printf("updated task %s\n", t.Name)
			if resumeCleared {
				a.warnf("note: the resume command went with the old agent; set one for this agent with --resume\n")
			}
			if resume.changed(cmd) {
				a.printf("  resume: %s\n", resumeDisplay(t.Resume))
			}
			if setAgent {
				window := t.AgentWindow()
				if window == t.AgentRepo && len(t.Repos[window].Agent) > 0 {
					a.warnf("note: %s has an agent of its own, which its window keeps running\n", window)
				}
				a.printf("  agent: %s (window %s; applies on window creation or explicit restart)\n", agentDisplay(task.Repo{Agent: t.Agent}), window)
			}
			return nil
		}}
	d.flags(cmd)
	agent.flags(cmd)
	resume.flags(cmd, true)
	return cmd
}

func resumeDisplay(argv []string) string {
	if len(argv) == 0 {
		return "none"
	}
	return shellJoin(argv)
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
	purpose := "The brief is the task's instructions: what is to be done, and why. It is\n" +
		"written for whoever picks the task up, person or agent."
	if handoff {
		purpose = "The handoff is where the work stands: what is done, what is next, what\n" +
			"to watch for. Whoever stops working on the task, person or agent, leaves\n" +
			"it for whoever continues. `tspace task list` shows its first line."
	}
	cmd := &cobra.Command{Use: name + " NAME [TEXT]", Short: "Show or replace the task's " + name,
		Long: purpose + "\n\n" +
			"With no text or flags, print the stored Markdown. TEXT or --file replaces\n" +
			"it; --clear removes it. --edit opens an editor and needs a TTY.\n\n" +
			"The note is a Markdown file in the task's .context directory, beside its\n" +
			"worktrees, so anything working in one reaches it as ../.context/" + noteFile + ",\n" +
			"and the root agent as .context/" + noteFile + ". Editing the file directly is\n" +
			"the same as replacing it here.\n\n" +
			"Notes survive tspace clean and are deleted with the task record.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if modes == 0 {
				// Reading takes no lock, so it never waits.
				t, err := a.loadTask(args[0])
				if err != nil {
					return err
				}
				brief, handoff, err := a.notes(cmd.Context(), t)
				if err != nil {
					return err
				}
				stored := brief
				if noteFile == task.HandoffFile {
					stored = handoff
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
			t, unlock, err := a.lockTask(cmd, args[0], false)
			if err != nil {
				return err
			}
			defer unlock()
			if _, err := a.Store.MigrateNotes(cmd.Context(), t); err != nil {
				return err
			}
			if edit {
				unlock() // the editor writes the file itself, for as long as it is open
				return a.editTaskNote(cmd.Context(), t, noteFile)
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
// attach, open and shell — the moment before the terminal is handed to the
// multiplexer. An escape sequence in one would retitle the tab or worse, so
// nothing but tabs and newlines survives to the screen. The stored note keeps
// every byte: this is a rendering rule, not a validation rule.
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

// summaryWidth keeps `tspace task list` inside an 80-column terminal: the row
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

func (a *App) printTaskContext(ctx context.Context, t *task.Task) {
	if t.Title != "" {
		a.printf("title: %s\n", oneLine(t.Title))
	}
	if t.Ticket != "" {
		a.printf("ticket: %s\n", oneLine(t.Ticket))
	}
	if t.TicketURL != "" {
		a.printf("ticket URL: %s\n", oneLine(t.TicketURL))
	}
	brief, handoff, err := a.notes(ctx, t)
	if err != nil {
		// Context is a courtesy on the way into a workspace; failing to read it
		// must not stop you getting there.
		a.warnf("tspace: could not read task notes: %v\n", err)
		return
	}
	if brief.Text != "" {
		a.printf("\nTask brief\n%s\n", strings.TrimRight(displayText(brief.Text), "\n"))
	}
	if handoff.Text != "" {
		a.printf("\nLocal handoff (updated %s UTC)\n%s\n", handoffTime(handoff), strings.TrimRight(displayText(handoff.Text), "\n"))
	}
}
