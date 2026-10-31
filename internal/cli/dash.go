package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"github.com/wuyuz99/taskspace/internal/status"
	"github.com/wuyuz99/taskspace/internal/task"
	"github.com/wuyuz99/taskspace/internal/workspace"
)

func (a *App) newDashCmd() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "dash",
		Short: "Watch every task and step into the one that needs you",
		Long: "dash is a live, full-screen view of every task: one row per window, with\n" +
			"its runtime state, what its agent is doing, and its git state. STATE is\n" +
			"the agent's own signal, else the window's bell, else a long silence, as\n" +
			"in `tspace status --help`. Below the table it shows the selected row's\n" +
			"directory, where its state came from, and its handoff. It is `tspace\n" +
			"status` for a person supervising several agents; everything it does is\n" +
			"also a command. Extra windows, opened with `tspace window`, are _NAME\n" +
			"rows under their task: enter, X and r work on them as on any window, r\n" +
			"only on a saved one.\n\n" +
			"  up/down, j/k  select a row (mouse wheel moves one row)\n" +
			"  space         expand/collapse a task; one that needs you stays expanded\n" +
			"  enter         attach to that window; detaching returns here\n" +
			"  s             start the task (tspace start)\n" +
			"  r             restart a dead window (tspace restart)\n" +
			"  x             stop the task, after confirming (tspace stop)\n" +
			"  S / X         start / stop only the selected window (tspace start/stop TASK REPO)\n" +
			"  D             clean the task, after confirming (tspace clean)\n" +
			"  R             delete the task, after confirming (tspace clean --rm)\n" +
			"  -             remove the selected repository from the task, after\n" +
			"                confirming (tspace repo rm)\n" +
			"  m             move the selected repository to another task, in a form\n" +
			"                (tspace repo move)\n" +
			"  t             open the task in a new terminal tab (tspace config terminal)\n" +
			"  c             clear the row's signal\n" +
			"  N             rename the selected task (tspace task rename)\n" +
			"  n             create a task, in a form below the table (tspace task create)\n" +
			"  I             start every interrupted task (tspace start --interrupted);\n" +
			"                dash offers it on launch when there are any\n" +
			"  w             open another window at the row's place, in a form: a shell,\n" +
			"                the task's agent or a command (tspace window -d)\n" +
			"  q             quit\n\n" +
			"Inside the multiplexer, enter switches the client to the window and\n" +
			"dash keeps running in its own.\n\n" +
			"dash needs a terminal. In a script, use `tspace status --json`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isTerminal(a.Out) {
				return fmt.Errorf("tspace dash needs a terminal; use `tspace status --watch` or `tspace status --json` instead")
			}
			if interval < 200*time.Millisecond {
				return fmt.Errorf("--interval must be at least 200ms")
			}
			ctx := cmd.Context()
			m := newDashModel(ctx, a, interval)
			for {
				setTitle(a.Out, "tspace dash", a.noTitle)
				final, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx)).Run()
				if ctx.Err() != nil {
					return nil
				}
				if err != nil {
					return err
				}
				m = final.(dashModel)
				if m.attach == nil {
					return nil
				}

				// Outside the multiplexer, attaching needs the whole terminal, so the
				// dashboard steps aside and comes back when the user detaches.
				target := *m.attach
				m.attach = nil
				session := a.Runtime.SessionName(target.Task)
				err = a.Runtime.Select(ctx, session, target.Window)
				if err == nil {
					setTitle(a.Out, "tspace: "+target.Task, a.noTitle)
					err = a.Runtime.Attach(ctx, session)
				}
				m.note, m.noteErr = "detached from "+target.label(), false
				if err != nil {
					m.note, m.noteErr = err.Error(), true
				}
			}
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "time between refreshes")
	return cmd
}

// dashRow is one window: a task's root, or one of its repositories.
type dashRow struct {
	Summary int // number of windows represented by a collapsed task
	Task    string
	Window  string // a repository name, or task.RootWindowName
	Branch  string
	Agent   string
	Runtime status.RuntimeState
	Git     string
	Signal  *task.Signal
	// Report is what the STATE column shows: the signal, the bell, or a
	// long silence, whichever is most trusted. Output is when the window
	// last printed, if the multiplexer says.
	Report *status.Report
	Output time.Time
	Dir    string
	// Saved marks an extra window the task's record keeps; Repo is the
	// repository it opens in, empty at the task root.
	Saved bool
	Repo  string
}

func (r dashRow) root() bool { return r.Window == task.RootWindowName }

// extra says the row is a window opened with `tspace window`.
func (r dashRow) extra() bool { return task.IsExtraWindow(r.Window) }

func (r dashRow) label() string {
	if r.root() {
		return r.Task + " (root)"
	}
	return r.Task + "/" + r.Window
}

// dashTask is what the detail pane shows about a row's task.
type dashTask struct {
	Title   string
	Ticket  string
	Handoff task.Note
}

// dashData is one refresh of everything on screen.
type dashData struct {
	Rows  []dashRow
	Tasks map[string]dashTask
	Warn  []string
	At    time.Time
}

// dashLoad gathers a refresh. It prints nothing: the screen belongs to the
// dashboard, so problems come back as warnings to draw.
func (a *App) dashLoad(ctx context.Context) dashData {
	d := dashData{Tasks: map[string]dashTask{}, At: time.Now()}
	tasks, err := a.Store.List()
	var skipped *task.SkippedError
	switch {
	case errors.As(err, &skipped):
		for _, e := range skipped.Errs {
			d.Warn = append(d.Warn, e.Error())
		}
	case err != nil:
		d.Warn = append(d.Warn, err.Error())
		return d
	}
	byName := make(map[string]*task.Task, len(tasks))
	for _, t := range tasks {
		byName[t.Name] = t
		info := dashTask{Title: t.Title, Ticket: t.Ticket}
		// Read, never written: a refresh every two seconds must not write.
		if _, n, err := a.Store.Notes(t); err == nil {
			info.Handoff = n
		}
		d.Tasks[t.Name] = info
	}
	for _, ts := range a.aggregator().All(ctx, tasks) {
		if ts.RuntimeErr != nil {
			d.Warn = append(d.Warn, fmt.Sprintf("%s: runtime could not be queried: %v", ts.Task, ts.RuntimeErr))
		}
		if ts.Root != nil {
			d.Rows = append(d.Rows, dashRow{
				Task: ts.Task, Window: task.RootWindowName, Agent: ts.Root.Agent,
				Runtime: ts.Root.Runtime, Git: "-", Signal: ts.Root.Signal, Report: ts.Root.Report, Output: ts.Root.Pane.Output,
				Dir: a.Store.TaskDir(ts.Task),
			})
		}
		for _, r := range ts.Repos {
			if r.Err != nil {
				d.Warn = append(d.Warn, fmt.Sprintf("%s/%s: %v", r.Task, r.Repo, r.Err))
			}
			d.Rows = append(d.Rows, dashRow{
				Task: r.Task, Window: r.Repo, Branch: r.Branch, Agent: r.Agent,
				Runtime: r.Runtime, Git: gitSummary(r), Signal: r.Signal, Report: r.Report, Output: r.Pane.Output,
				Dir: byName[r.Task].Repos[r.Repo].Worktree,
			})
		}
		for _, e := range ts.Extra {
			// Only a saved window's place is known.
			dir := ""
			if e.Saved {
				w := task.Window{Name: strings.TrimPrefix(e.Name, task.ExtraWindowPrefix), Repo: e.Repo}
				dir = workspace.ExtraWindow(a.Store, byName[ts.Task], w).Dir
			}
			d.Rows = append(d.Rows, dashRow{
				Task: ts.Task, Window: e.Name, Agent: extraAgent(e), Runtime: e.Runtime, Git: "-", Dir: dir, Saved: e.Saved, Repo: e.Repo,
			})
		}
	}
	return d
}

type (
	dashDataMsg   dashData
	dashTickMsg   struct{}
	dashAttachMsg dashRow
	// dashDoneMsg is the outcome of an action, for the note line.
	dashDoneMsg struct {
		text string
		err  error
		// gone is a task the action deleted; made, one it made, and window
		// the window it opened there, for the cursor to go to.
		gone, made, window string
	}
)

// dashConfirm is a question waiting on y.
type dashConfirm struct {
	prompt string
	run    tea.Cmd
	// passing lets a key other than y, n or esc through after cancelling,
	// for a question dash asks unprompted.
	passing bool
}

type dashModel struct {
	rawRows  []dashRow
	expanded map[string]bool
	ctx      context.Context
	app      *App
	interval time.Duration

	data    dashData
	loaded  bool
	loading bool
	cursor  int
	width   int
	height  int

	note    string
	noteErr bool
	confirm *dashConfirm
	// gone is the task an action just deleted, until the next refresh;
	// made and madeWindow are where an action wants the cursor next.
	gone, made, madeWindow string
	// form is open while its fields are typed in.
	form *dashForm
	// offered says the first refresh has offered to start interrupted tasks.
	offered bool
	// attach is set when the program quits in order to attach.
	attach *dashRow
}

func newDashModel(ctx context.Context, a *App, interval time.Duration) dashModel {
	return dashModel{ctx: ctx, app: a, interval: interval, width: 80, height: 24}
}

func (m dashModel) load() tea.Cmd {
	return func() tea.Msg { return dashDataMsg(m.app.dashLoad(m.ctx)) }
}

func (m dashModel) tick() tea.Cmd {
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return dashTickMsg{} })
}

func (m dashModel) Init() tea.Cmd { return tea.Batch(m.load(), m.tick()) }

func (m dashModel) selected() (dashRow, bool) {
	if m.cursor < 0 || m.cursor >= len(m.data.Rows) {
		return dashRow{}, false
	}
	return m.data.Rows[m.cursor], true
}

func (m dashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case dashTickMsg:
		// A refresh that outlasts the interval is not piled onto.
		if m.loading {
			return m, m.tick()
		}
		m.loading = true
		return m, tea.Batch(m.load(), m.tick())

	case dashDataMsg:
		// The cursor follows its row, not its position: tasks come and go.
		prev, had := m.selected()
		m.data, m.loaded, m.loading = dashData(msg), true, false
		m.rawRows = append([]dashRow(nil), m.data.Rows...)
		m.data.Rows = collapseDashRows(m.rawRows, m.expanded)
		// A refresh begun before the delete may still show the task.
		present := func(name string) bool {
			return slices.ContainsFunc(m.data.Rows, func(r dashRow) bool { return r.Task == name })
		}
		if m.gone != "" && !present(m.gone) && had && prev.Task == m.gone {
			// A task just deleted leaves the cursor on the first row of the
			// one after it, or of the last; rows are sorted by task name.
			for i, r := range m.data.Rows {
				if i == 0 || r.Task != m.data.Rows[i-1].Task {
					m.cursor = i
				}
				if r.Task > prev.Task {
					break
				}
			}
			had = false
		}
		if m.gone != "" && !present(m.gone) {
			m.gone = ""
		}
		// A window opened waits for a refresh that shows it.
		if m.made != "" && present(m.made) && (m.madeWindow == "" ||
			slices.ContainsFunc(m.data.Rows, func(r dashRow) bool { return r.Task == m.made && r.Window == m.madeWindow })) {
			first := true
			for i, r := range m.data.Rows {
				if r.Task == m.made && (first || r.Window == m.madeWindow) {
					m.cursor, first = i, false
				}
			}
			m.made, m.madeWindow, had = "", "", false
		}
		if had {
			for i, r := range m.data.Rows {
				if r.Task == prev.Task {
					m.cursor = i
					break
				}
			}
			for i, r := range m.data.Rows {
				if r.Task == prev.Task && r.Window == prev.Window {
					m.cursor = i
				}
			}
		}
		m.cursor = max(min(m.cursor, len(m.data.Rows)-1), 0)
		// Once, on launch: after a computer restart this is the question.
		if !m.offered {
			m.offered = true
			if n := m.interrupted(); n > 0 && m.confirm == nil && m.form == nil {
				m.confirm = m.startInterrupted(n)
				m.confirm.passing = true
			}
		}
		return m, nil

	case dashDoneMsg:
		if msg.made != "" {
			m.made, m.madeWindow = msg.made, msg.window
			// A window opened is shown, not folded into its task.
			if msg.window != "" {
				if m.expanded == nil {
					m.expanded = map[string]bool{}
				}
				m.expanded[msg.made] = true
			}
		}
		if msg.gone != "" {
			m.gone = msg.gone
			delete(m.expanded, msg.gone)
		}
		m.note, m.noteErr = msg.text, false
		if msg.err != nil {
			m.note, m.noteErr = msg.err.Error(), true
		}
		return m, m.load()

	case dashAttachMsg:
		row := dashRow(msg)
		m.attach = &row
		return m, tea.Quit

	case tea.KeyMsg:
		return m.key(msg)
	case tea.MouseMsg:
		if m.confirm != nil || m.form != nil || msg.Action != tea.MouseActionPress {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.cursor = max(m.cursor-1, 0)
		case tea.MouseButtonWheelDown:
			m.cursor = max(min(m.cursor+1, len(m.data.Rows)-1), 0)
		}
		return m, nil
	}
	return m, nil
}

func (m dashModel) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if f := m.form; f != nil {
		run, done := f.key(msg)
		if done {
			m.form = nil
			m.note, m.noteErr = "cancelled", false
			if run != nil {
				m.note = f.busy
			}
		}
		return m, run
	}
	if c := m.confirm; c != nil {
		m.confirm = nil
		if msg.String() == "y" {
			return m, c.run
		}
		m.note, m.noteErr = "cancelled", false
		if k := msg.String(); !c.passing || k == "n" || k == "esc" {
			return m, nil
		}
	}

	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
		return m, nil
	case "down", "j":
		m.cursor = max(min(m.cursor+1, len(m.data.Rows)-1), 0)
		return m, nil
	case "home", "g":
		m.cursor = 0
		return m, nil
	case "end", "G":
		m.cursor = max(len(m.data.Rows)-1, 0)
		return m, nil
	}

	switch msg.String() {
	case "n":
		m.form = m.createForm()
		return m, nil
	case "I":
		if n := m.interrupted(); n > 0 {
			m.confirm = m.startInterrupted(n)
		} else {
			m.note = "no task was interrupted"
		}
		return m, nil
	}
	row, ok := m.selected()
	if !ok {
		return m, nil
	}
	// Summary state describes the whole task, but attach/start/stop target
	// its chosen primary window. A missing sibling must not block attachment.
	if row.Summary > 1 {
		for _, original := range m.rawRows {
			if original.Task == row.Task && original.Window == row.Window {
				row.Runtime = original.Runtime
				break
			}
		}
	}
	m.noteErr = false
	switch msg.String() {
	case " ", "right", "left":
		if m.expanded == nil {
			m.expanded = map[string]bool{}
		}
		m.expanded[row.Task] = !m.expanded[row.Task]
		if msg.String() == "right" {
			m.expanded[row.Task] = true
		}
		if msg.String() == "left" {
			m.expanded[row.Task] = false
		}
		m.data.Rows = collapseDashRows(m.rawRows, m.expanded)
		for i, r := range m.data.Rows {
			if r.Task == row.Task {
				m.cursor = i
				break
			}
		}
		return m, nil
	case "enter":
		if row.Runtime != status.RuntimeRunning && row.Runtime != status.RuntimeDead {
			m.note = fmt.Sprintf("%s has no window to attach to (%s); press s to start the task", row.label(), row.Runtime)
			return m, nil
		}
		return m, m.focus(row)
	case "s":
		m.note = "starting " + row.Task + "..."
		return m, m.act(func(ws *workspace.Service) (string, error) {
			res, err := ws.Start(m.ctx, row.Task, workspace.StartOptions{})
			if err != nil {
				return "", err
			}
			return startedText(res), nil
		})
	case "S":
		if row.extra() {
			m.note = "S starts a primary window; s starts the task with its saved windows"
			return m, nil
		}
		opts := workspace.StartOptions{Root: row.root()}
		if !row.root() {
			opts.Repos = []string{row.Window}
		}
		return m, m.act(func(ws *workspace.Service) (string, error) {
			res, err := ws.Start(m.ctx, row.Task, opts)
			if err != nil {
				return "", err
			}
			return startedText(res), nil
		})
	case "X":
		m.confirm = &dashConfirm{
			prompt: fmt.Sprintf("stop only %s? (y/N)", row.label()),
			run: m.act(func(ws *workspace.Service) (string, error) {
				repo := row.Window
				if row.root() {
					repo = ""
				}
				err := ws.StopWindow(m.ctx, row.Task, repo, row.root())
				return "stopped " + row.label(), err
			}),
		}
		return m, nil
	case "r":
		if row.Runtime != status.RuntimeDead {
			m.note = fmt.Sprintf("%s is %s; only a dead window can be restarted", row.label(), row.Runtime)
			return m, nil
		}
		opts := workspace.RestartOptions{Root: row.root(), Repo: row.Window}
		if row.root() {
			opts.Repo = ""
		}
		return m, m.act(func(ws *workspace.Service) (string, error) {
			label, resumed, err := ws.Restart(m.ctx, row.Task, opts)
			if err != nil {
				return "", err
			}
			return restartedText(label, row.Task, resumed), nil
		})
	case "t":
		// As enter does: to a window that is there, the one selected.
		if row.Runtime != status.RuntimeRunning && row.Runtime != status.RuntimeDead {
			m.note = fmt.Sprintf("%s has no window to attach to (%s); press s to start the task", row.label(), row.Runtime)
			return m, nil
		}
		m.note = "opening " + row.label() + " in a new tab..."
		return m, func() tea.Msg {
			if err := m.app.Runtime.Select(m.ctx, m.app.Runtime.SessionName(row.Task), row.Window); err != nil {
				return dashDoneMsg{err: err}
			}
			if err := m.app.openInNewTab(m.ctx, row.Task); err != nil {
				return dashDoneMsg{err: err}
			}
			return dashDoneMsg{text: "opened " + row.label() + " in a new tab"}
		}
	case "x":
		m.confirm = &dashConfirm{
			prompt: fmt.Sprintf("stop %s and end all its windows? worktrees are kept (y/N)", row.Task),
			run: m.act(func(ws *workspace.Service) (string, error) {
				running, err := ws.Stop(m.ctx, row.Task)
				if err != nil {
					return "", err
				}
				return stoppedText(row.Task, running), nil
			}),
		}
		return m, nil
	case "D":
		return m.clean(row.Task, workspace.CleanOptions{},
			fmt.Sprintf("clean %s and remove its worktrees? record and branches are kept (y/N)", row.Task),
			"cleaned "+row.Task)
	case "R":
		// Its own key, not a choice under D: D can be undone with s, R cannot.
		return m.clean(row.Task, workspace.CleanOptions{Remove: true},
			fmt.Sprintf("delete %s: remove worktrees, record and notes? branches are kept (y/N)", row.Task),
			"deleted "+row.Task+"; its branches are kept, with no task referring to them")
	case "-", "m":
		switch {
		case row.Summary > 1:
			m.note = "expand the task with space to choose a repository"
			return m, nil
		case row.root():
			m.note = "the root window isn't a repository; X closes it, R deletes the task"
			return m, nil
		case row.extra():
			m.note = row.Window + " isn't a repository; X closes it"
			return m, nil
		}
		if msg.String() == "m" {
			m.form = m.moveRepoForm(row.Task, row.Window)
			return m, nil
		}
		if os.Getenv(envTask) == row.Task {
			m.note, m.noteErr = fmt.Sprintf("dash runs in %s's session, and this closes windows of it; run it from outside: tspace repo rm %s %s", row.Task, row.Task, row.Window), true
			return m, nil
		}
		m.confirm = &dashConfirm{
			prompt: fmt.Sprintf("remove %s from %s: clean its worktree and close its window? branch is kept (y/N)", row.Window, row.Task),
			run: m.act(func(ws *workspace.Service) (string, error) {
				left, err := ws.RemoveRepo(m.ctx, row.Task, row.Window, false)
				text := removedRepoText(row.Window, row.Task)
				if left != "" {
					text += "; note: the worktree at " + left + " was left in place"
				}
				return text, err
			}),
		}
		return m, nil
	case "N":
		m.form = m.renameForm(row.Task)
		return m, nil
	case "w":
		if !m.sessionUp(row.Task) {
			m.note = row.Task + " is not running; press s to start it"
			return m, nil
		}
		m.form = m.windowForm(row)
		return m, nil
	case "c":
		if row.Summary > 1 {
			m.note = "expand the task with space to clear a window's signal"
			return m, nil
		}
		if row.Signal == nil {
			m.note = row.label() + " has no signal"
			return m, nil
		}
		return m, func() tea.Msg {
			err := m.app.Store.ClearSignal(row.Task, row.Window)
			return dashDoneMsg{text: row.label() + ": signal cleared", err: err}
		}
	}
	return m, nil
}

// interrupted counts the tasks shown as interrupted.
func (m dashModel) interrupted() int {
	seen := map[string]bool{}
	for _, r := range m.rawRows {
		if r.Runtime == status.RuntimeInterrupted {
			seen[r.Task] = true
		}
	}
	return len(seen)
}

// startInterrupted asks to start the n interrupted tasks, as `tspace start
// --interrupted` does: what each was running, carrying on past a failure.
func (m dashModel) startInterrupted(n int) *dashConfirm {
	prompt := fmt.Sprintf("%d tasks were interrupted; start them? (y/N)", n)
	if n == 1 {
		prompt = "1 task was interrupted; start it? (y/N)"
	}
	return &dashConfirm{
		prompt: prompt,
		run: m.act(func(ws *workspace.Service) (string, error) {
			started, failed, err := ws.StartInterrupted(m.ctx, workspace.StartOptions{})
			if err != nil {
				return "", err
			}
			text := ""
			err = interruptedResult(started, failed, func(t string) { text = t })
			return text, err
		}),
	}
}

// clean asks prompt, then cleans name as `tspace clean` does with opts. A
// dash running in name's own session would end with it, halfway through, so
// it refuses there. A task deleted is remembered, for the cursor to move on.
func (m dashModel) clean(name string, opts workspace.CleanOptions, prompt, done string) (tea.Model, tea.Cmd) {
	if os.Getenv(envTask) == name {
		m.note, m.noteErr = fmt.Sprintf("dash runs in %s's session, which this would end first; run it from outside: tspace clean %s", name, name), true
		if opts.Remove {
			m.note += " --rm"
		}
		return m, nil
	}
	act := m.act(func(ws *workspace.Service) (string, error) {
		return done, ws.Clean(m.ctx, name, opts)
	})
	m.confirm = &dashConfirm{prompt: prompt, run: func() tea.Msg {
		msg := act().(dashDoneMsg)
		if msg.err == nil && opts.Remove {
			msg.gone = name
		}
		return msg
	}}
	return m, nil
}

// focus shows a window: by switching the client when dash is inside the
// multiplexer, and otherwise by asking the program to quit and attach.
func (m dashModel) focus(row dashRow) tea.Cmd {
	return func() tea.Msg {
		focused, err := m.app.Runtime.Focus(m.ctx, m.app.Runtime.SessionName(row.Task), row.Window)
		switch {
		case err != nil:
			return dashDoneMsg{err: err}
		case focused:
			return dashDoneMsg{text: "switched to " + row.label()}
		}
		return dashAttachMsg(row)
	}
}

// act performs a lifecycle action through the same service the CLI's
// commands use, and says what it did in the CLI's words, so the two cannot
// drift apart. Nothing is written over the screen: the warnings, as the CLI
// would show them, go after the text instead, and hooks' output nowhere.
// It does not wait for a task another command is changing: nobody would see
// the wait, or be able to stop it.
func (m dashModel) act(do func(*workspace.Service) (string, error)) tea.Cmd {
	return func() tea.Msg {
		var warning bytes.Buffer
		sub := *m.app
		sub.Out, sub.Err = io.Discard, &warning
		sub.noWait = true
		ws := sub.workspace()
		ws.Stdout, ws.Stderr = io.Discard, io.Discard
		var notes []string
		ws.Progress = func(e workspace.Event) {
			// Said on the CLI's standard output, which dash drops, but worth
			// the note line: the agent came back with its conversation.
			switch e := e.(type) {
			case workspace.Resumed:
				notes = append(notes, "resumed "+dashRow{Task: e.Task, Window: e.Window}.label())
			case workspace.ConversationCarried:
				notes = append(notes, "carried "+dashRow{Task: e.Task, Window: e.Window}.label()+"'s conversation")
			case workspace.AgentPlaced:
				notes = append(notes, e.Task+"'s agent runs in "+e.Repo)
			case workspace.RootWindowPending:
				notes = append(notes, "the next start opens a root window for "+e.Task)
			case workspace.RootWindowOpened:
				notes = append(notes, "opened the root window for "+e.Task)
			}
			warning.Reset()
			sub.render(e)
			if w := oneLine(strings.TrimSpace(warning.String())); w != "" && !slices.Contains(notes, w) {
				notes = append(notes, w)
			}
		}
		text, err := do(ws)
		if err == nil && len(notes) > 0 {
			text += "; " + strings.Join(notes, "; ")
		}
		return dashDoneMsg{text: text, err: err}
	}
}

var (
	dashBold   = lipgloss.NewStyle().Bold(true)
	dashFaint  = lipgloss.NewStyle().Faint(true)
	dashPicked = lipgloss.NewStyle().Reverse(true)
	dashRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	dashGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	dashYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

func runtimeStyle(s status.RuntimeState) lipgloss.Style {
	switch s {
	case status.RuntimeRunning:
		return dashGreen
	case status.RuntimeDead:
		return dashRed
	case status.RuntimeUnknown, status.RuntimeInterrupted:
		return dashYellow
	}
	return dashFaint
}

func reportStyle(r *status.Report) lipgloss.Style {
	if r == nil {
		return dashFaint
	}
	switch r.State {
	case status.ReportState(task.SignalBlocked), status.ReportAttention:
		return dashYellow.Bold(true)
	case status.ReportState(task.SignalFailed):
		return dashRed.Bold(true)
	case status.ReportState(task.SignalDone), status.ReportIdle:
		return dashGreen
	}
	return dashFaint
}

// dashDetailLines is the height kept for everything that is not the table.
const dashDetailLines = 13

func (m dashModel) View() string {
	if !m.loaded {
		return "tspace dash: loading...\n"
	}
	var b strings.Builder
	now := time.Now()

	// Header: what needs attention, counted, so the answer to "is anyone
	// waiting on me" does not require reading the table.
	tasks, blocked, bell, failed, dead := map[string]bool{}, 0, 0, 0, 0
	for _, r := range m.data.Rows {
		tasks[r.Task] = true
		if r.Runtime == status.RuntimeDead {
			dead++
		}
		if r.Report != nil {
			switch r.Report.State {
			case status.ReportState(task.SignalBlocked):
				blocked++
			case status.ReportAttention:
				bell++
			case status.ReportState(task.SignalFailed):
				failed++
			}
		}
	}
	head := []string{fmt.Sprintf("%d task%s", len(tasks), plural(len(tasks)))}
	if blocked > 0 {
		head = append(head, dashYellow.Bold(true).Render(fmt.Sprintf("%d blocked", blocked)))
	}
	if bell > 0 {
		head = append(head, dashYellow.Bold(true).Render(fmt.Sprintf("%d bell", bell)))
	}
	if failed > 0 {
		head = append(head, dashRed.Bold(true).Render(fmt.Sprintf("%d failed", failed)))
	}
	if dead > 0 {
		head = append(head, dashRed.Render(fmt.Sprintf("%d dead", dead)))
	}
	fmt.Fprintf(&b, "%s  %s  %s\n\n", dashBold.Render("tspace dash"), strings.Join(head, " · "),
		dashFaint.Render(m.data.At.Format("15:04:05")))

	if len(m.data.Rows) == 0 {
		b.WriteString("no tasks yet; press n to create one (tspace task create NAME)\n")
	} else {
		m.table(&b, now)
		// The form takes the detail pane's room.
		if m.form == nil {
			m.detail(&b, now)
		}
	}

	for _, w := range m.data.Warn {
		b.WriteString(dashYellow.Render(oneLine(w)) + "\n")
	}
	switch {
	case m.form != nil:
		b.WriteString(m.form.view(m.width))
	case m.confirm != nil:
		b.WriteString(dashBold.Render(m.confirm.prompt) + "\n")
	case m.noteErr:
		b.WriteString(dashRed.Render(oneLine(m.note)) + "\n")
	default:
		b.WriteString(m.note + "\n")
	}
	if m.form == nil {
		b.WriteString(dashFaint.Render("enter attach · space expand · s/x task · S/X window · r restart · D clean · R delete · - remove repo · m move repo · t tab · c clear · n new · N rename · w window · I interrupted · q quit"))
	}

	// Nothing may wrap: a wrapped line pushes the rest off the alt screen.
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "…")
	}
	return strings.Join(lines, "\n")
}

func (m dashModel) table(b *strings.Builder, now time.Time) {
	cells := make([][]string, 0, len(m.data.Rows)+1)
	cells = append(cells, []string{"TASK", "REPO", "AGENT", "RUNTIME", "STATE", "GIT"})
	for i, r := range m.data.Rows {
		// The task is named once, on its first row, so each task reads as a group.
		name, repo := "", r.Window
		if i == 0 || m.data.Rows[i-1].Task != r.Task {
			name = r.Task
		}
		if r.root() {
			repo = "(root)"
		}
		if r.Saved {
			repo += " (saved)"
		}
		if r.Summary > 1 {
			repo = fmt.Sprintf("(%d windows)", r.Summary)
		}
		cells = append(cells, []string{name, repo, r.Agent, string(r.Runtime), reportSummary(r.Report, now), r.Git})
	}
	widths := make([]int, len(cells[0]))
	for _, row := range cells {
		for i, c := range row {
			widths[i] = max(widths[i], lipgloss.Width(c))
		}
	}
	// Titles use only spare width, leaving the runtime/signal/git columns
	// as visible as they were without a title.
	used := 2
	for _, w := range widths {
		used += w + 2
	}
	spare := max(m.width-used+2, 0)
	for _, row := range cells[1:] {
		if row[0] == "" {
			continue
		}
		title := oneLine(m.data.Tasks[row[0]].Title)
		if title != "" && spare > 3 {
			row[0] += "  " + ansi.Truncate(title, spare-2, "…")
		}
	}
	for _, row := range cells {
		widths[0] = max(widths[0], lipgloss.Width(row[0]))
	}
	pad := func(i int, s string) string { return s + strings.Repeat(" ", widths[i]-lipgloss.Width(s)+2) }

	// Scroll so the cursor stays on screen.
	visible := max(m.height-dashDetailLines, 3)
	first := 0
	if m.cursor >= visible {
		first = m.cursor - visible + 1
	}

	line := "  "
	for i, c := range cells[0] {
		line += pad(i, c)
	}
	b.WriteString(dashFaint.Render(strings.TrimRight(line, " ")) + "\n")
	for n, r := range m.data.Rows {
		if n < first || n >= first+visible {
			continue
		}
		c := cells[n+1]
		mark, name := "  ", lipgloss.NewStyle()
		if n == m.cursor {
			mark, name = "> ", dashBold
		}
		b.WriteString(mark + name.Render(pad(0, c[0])) + name.Render(pad(1, c[1])) + pad(2, c[2]) +
			runtimeStyle(r.Runtime).Render(pad(3, c[3])) + reportStyle(r.Report).Render(pad(4, c[4])) + c[5] + "\n")
	}
	if rest := len(m.data.Rows) - first - visible; rest > 0 {
		b.WriteString(dashFaint.Render(fmt.Sprintf("  ... %d more", rest)) + "\n")
	}
}

// reportLines say where the selected row's state came from: the signal, with
// its message; the bell; or a silence, offered as a guess. A signal the bell
// outranked is still shown, and so is a working agent's long silence.
func reportLines(row dashRow, now time.Time) []string {
	var out []string
	r := row.Report
	if r != nil {
		var text string
		switch r.Source {
		case status.SourceBell:
			text = "  state    bell: the window rang it"
			if !r.At.IsZero() {
				text += fmt.Sprintf(", last output %s ago", age(now.Sub(r.At)))
			}
		case status.SourceSilence:
			text = fmt.Sprintf("  state    quiet: no output for %s, so it may be waiting (a guess)", age(now.Sub(r.At)))
		case status.SourceSignal:
			text = fmt.Sprintf("  signal   %s, %s ago", r.State, age(now.Sub(r.At)))
			if r.Message != "" {
				text += ": " + oneLine(r.Message)
			}
		default:
			text = fmt.Sprintf("  state    %s, as %s reports it now", r.State, r.Source)
			if r.Message != "" {
				text += ": " + oneLine(r.Message)
			}
		}
		out = append(out, reportStyle(r).Render(text))
	}
	if s := row.Signal; s != nil && (r == nil || r.Source != status.SourceSignal) {
		out = append(out, dashFaint.Render(fmt.Sprintf("  signal   %s, %s ago", s.State, age(now.Sub(s.At)))))
	}
	if r != nil && r.Source == status.SourceSignal && !row.Output.IsZero() && now.Sub(row.Output) >= status.QuietAfter {
		out = append(out, dashFaint.Render(fmt.Sprintf("  quiet    no output for %s", age(now.Sub(row.Output)))))
	}
	return out
}

// detail describes the selected row: where it is, what its agent said, and
// where the task stands.
func (m dashModel) detail(b *strings.Builder, now time.Time) {
	row, ok := m.selected()
	if !ok {
		return
	}
	info := m.data.Tasks[row.Task]
	title := row.label()
	if row.Branch != "" {
		title += "  " + row.Branch
	}
	b.WriteString("\n" + dashBold.Render(title) + "\n")
	if s := strings.TrimSpace(oneLine(info.Ticket) + "  " + oneLine(info.Title)); s != "" {
		b.WriteString("  " + s + "\n")
	}
	if row.Dir != "" {
		b.WriteString(dashFaint.Render("  "+row.Dir) + "\n")
	}
	for _, l := range reportLines(row, now) {
		b.WriteString(l + "\n")
	}
	if h := info.Handoff; h.Text != "" {
		shown := 0
		for _, l := range strings.Split(displayText(h.Text), "\n") {
			if l = strings.TrimSpace(l); l == "" {
				continue
			}
			label := "           "
			if shown == 0 {
				label = "  handoff  "
			}
			b.WriteString(label + l + "\n")
			if shown++; shown == 3 {
				break
			}
		}
	}
	b.WriteString("\n")
}
