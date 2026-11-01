package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wuyuz99/taskspace/internal/task"
)

// OpenWindowOptions say what OpenWindow opens. Repo or Root place it, as
// DefaultsToRoot says when neither does, and Agent runs the task's agent
// instead of Command. Save keeps the window with the task, under Name, and
// works with no session running. Detach leaves the window unfocused.
type OpenWindowOptions struct {
	Repo    string
	Root    bool
	Name    string
	Command []string
	Agent   bool
	Save    bool
	Detach  bool
}

// OpenWindowResult is what OpenWindow did. Window is the window it opened,
// empty when it only saved one. Unfocused says it could not be shown to the
// user, who has to attach.
type OpenWindowResult struct {
	Task      *task.Task
	Window    string
	Unfocused bool
}

// WindowOpened reports an extra window opened, with the command it runs,
// empty for a shell.
type WindowOpened struct {
	Task, Window, Dir string
	Command           []string
}

// Info is a line that says what an operation did.
type Info struct{ Text string }

func (WindowOpened) event() {}
func (Info) event()         {}

// OpenWindow opens an extra window in the task's running session, under the
// task's lock: it waits for whatever is changing the task, such as a clean
// removing the worktree.
func (s *Service) OpenWindow(ctx context.Context, name string, o OpenWindowOptions) (*OpenWindowResult, error) {
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace window"), true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	repoArg := o.Repo
	if o.Root && repoArg != "" {
		return nil, fmt.Errorf("--root cannot be combined with a repository")
	}

	session := s.Runtime.SessionName(t.Name)
	st, stateErr := s.Runtime.State(ctx, session)
	running := stateErr == nil && st.Exists
	if !running && !o.Save {
		if stateErr != nil {
			return nil, stateErr
		}
		return nil, fmt.Errorf("task %s has no running session; start it with: tspace open %s", t.Name, t.Name)
	}
	// A window saved for later, with nothing running to open it in,
	// only needs its repository to be the task's: start makes the
	// worktree.
	later := !running
	place, repo, dir := "root", "", s.Store.TaskDir(t.Name)
	if !o.Root && !DefaultsToRoot(t, repoArg) {
		resolve := WorktreeDir
		if later {
			resolve = ResolveRepo
		}
		r, repoName, err := resolve(t, repoArg)
		if err != nil {
			return nil, err
		}
		place, repo, dir = repoName, repoName, r.Worktree
	} else if !isDir(dir) && !later {
		return nil, fmt.Errorf("task directory %s is missing; run: tspace start %s", dir, t.Name)
	}

	argv := o.Command
	if o.Agent {
		if argv, err = s.taskAgent(t); err != nil {
			return nil, err
		}
	}
	// Saved as given, placeholders and all, so a recipe carries it to
	// other tasks; run expanded, as start runs it.
	raw := append([]string(nil), argv...)
	argv = ExpandArgv(s.Store, t, argv)

	var saved task.Window
	if o.Save {
		if err := task.ValidateName("window", o.Name); err != nil {
			return nil, err
		}
		if _, ok := t.SavedWindow(o.Name); ok {
			return nil, fmt.Errorf("%s already has a saved window %s%s; forget it first: tspace window %s --forget %s", t.Name, task.ExtraWindowPrefix, o.Name, t.Name, o.Name)
		}
		if task.ExtraWindowPrefix+o.Name == task.RootWindowName {
			return nil, fmt.Errorf("%q names the task's root window; choose another name", o.Name)
		}
		saved = task.Window{Name: o.Name, Repo: repo, Command: raw}
	}
	saveWindow := func() error {
		t.Windows = append(t.Windows, saved)
		return s.Store.Save(t)
	}

	if later {
		if err := saveWindow(); err != nil {
			return nil, err
		}
		s.report(Info{Text: fmt.Sprintf("saved window %s for %s; start opens it", saved.SessionName(), t.Name)})
		return &OpenWindowResult{Task: t}, nil
	}
	taken := func(n string) bool {
		_, ok := st.Pane(s.Runtime.WindowName(n))
		return ok
	}

	var window string
	if o.Name != "" {
		if err := task.ValidateName("window", o.Name); err != nil {
			return nil, err
		}
		window = task.Window{Name: o.Name}.SessionName()
		if taken(window) {
			return nil, fmt.Errorf("window %s already exists in %s's session", window, t.Name)
		}
	} else {
		label := "shell"
		if len(argv) > 0 {
			label = WindowLabel(filepath.Base(argv[0]))
		}
		base := task.ExtraWindowPrefix + place + "-" + label
		window = base
		for i := 2; taken(window); i++ {
			window = fmt.Sprintf("%s-%d", base, i)
		}
	}

	// Opened exactly as restart and start will open it again.
	opened := task.Window{Name: strings.TrimPrefix(window, task.ExtraWindowPrefix), Repo: repo, Command: raw}
	if err := s.Runtime.OpenWindow(ctx, session, ExtraWindow(s.Store, t, opened)); err != nil {
		return nil, err
	}
	if !o.Save {
		if err := s.Store.RememberWindow(t.Name, opened); err != nil {
			s.warnf("tspace: %v", err)
		}
	}
	s.report(WindowOpened{Task: t.Name, Window: window, Command: argv, Dir: dir})
	if o.Save {
		if err := saveWindow(); err != nil {
			return nil, err
		}
		s.report(Info{Text: "saved it: start opens it again whenever it is not there"})
	}
	res := &OpenWindowResult{Task: t, Window: window}
	if o.Detach {
		return res, nil
	}
	focused, err := s.Runtime.Focus(ctx, session, window)
	if err != nil {
		return nil, err
	}
	res.Unfocused = !focused
	return res, nil
}

// SaveOpenWindow keeps an open unsaved window with the task.
func (s *Service) SaveOpenWindow(ctx context.Context, name, window string) error {
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace window"), true)
	if err != nil {
		return err
	}
	defer unlock()
	return s.saveOpenWindow(ctx, t, window)
}

// ForgetWindow stops recreating a saved window. That only edits the record,
// so it does not wait out a lifecycle change.
func (s *Service) ForgetWindow(ctx context.Context, name, window string) error {
	t, _, unlock, err := s.Lock(ctx, name, s.command("tspace window"), false)
	if err != nil {
		return err
	}
	defer unlock()
	return s.forgetWindow(t, window)
}

// RenameWindow renames an extra window, open or saved.
func (s *Service) RenameWindow(ctx context.Context, name, from, to string) error {
	t, ctx, unlock, err := s.Lock(ctx, name, s.command("tspace window"), true)
	if err != nil {
		return err
	}
	defer unlock()
	return s.renameWindow(ctx, t, from, to)
}

// DefaultsToRoot reports whether an omitted repository means the task root.
// With one repository the worktree is the obvious answer; with none or
// several, the root is the only directory that stands for the whole task.
func DefaultsToRoot(t *task.Task, repoArg string) bool {
	return repoArg == "" && len(t.Repos) != 1
}

// WindowLabel makes a command's name fit in a window name, which status
// and dash list only when it is one tspace could give: g++ becomes g--.
func WindowLabel(command string) string {
	label := strings.Map(func(r rune) rune {
		if r < 0x80 && (r == '.' || r == '_' || r == '-' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')) {
			return r
		}
		return '-'
	}, command)
	if label == "" {
		return "cmd"
	}
	return label
}

// saveOpenWindow keeps with the task an unsaved window that is open, as it
// was opened: the window itself is not touched.
func (s *Service) saveOpenWindow(ctx context.Context, t *task.Task, name string) error {
	name = strings.TrimPrefix(name, task.ExtraWindowPrefix)
	window := task.ExtraWindowPrefix + name
	if !task.IsExtraWindow(window) {
		return fmt.Errorf("%q is not a window name tspace gives", window)
	}
	if _, ok := t.SavedWindow(name); ok {
		return fmt.Errorf("%s is already saved with %s", window, t.Name)
	}
	st, err := s.Runtime.State(ctx, s.Runtime.SessionName(t.Name))
	if err != nil {
		return err
	}
	if _, open := st.Pane(s.Runtime.WindowName(window)); !open {
		return fmt.Errorf("%s has no open window %s; to open a window and save it: tspace window %s [REPO] --name %s --save -- COMMAND", t.Name, window, t.Name, name)
	}
	w, ok := KnownWindow(s.Store, t, name)
	if !ok {
		return fmt.Errorf("tspace does not know what %s runs; it remembers only unsaved windows it opened. Close it and open it saved: tspace stop %s %s, then tspace window %s [REPO] --name %s --save -- COMMAND", window, t.Name, window, t.Name, name)
	}
	t.Windows = append(t.Windows, w)
	if err := s.Store.Save(t); err != nil {
		return err
	}
	if err := s.Store.ForgetWindow(t.Name, name); err != nil {
		s.warnf("tspace: %v", err)
	}
	s.report(Info{Text: fmt.Sprintf("saved %s of %s: start opens it again whenever it is not there", window, t.Name)})
	return nil
}

// forgetWindow drops a saved window from the task. A window of that name
// already open is left alone.
func (s *Service) forgetWindow(t *task.Task, name string) error {
	i, ok := t.SavedWindow(name)
	if !ok {
		return fmt.Errorf("%s has no saved window %s%s%s", t.Name, task.ExtraWindowPrefix, strings.TrimPrefix(name, task.ExtraWindowPrefix), savedWindowsHint(t))
	}
	w := t.Windows[i]
	t.Windows = append(t.Windows[:i:i], t.Windows[i+1:]...)
	if len(t.Windows) == 0 {
		t.Windows = nil
	}
	if err := s.Store.Save(t); err != nil {
		return err
	}
	s.report(Info{Text: fmt.Sprintf("forgot saved window %s of %s; if it is open, it stays until it is closed or the session stops", w.SessionName(), t.Name)})
	return nil
}

// renameWindow renames an extra window, in the running session if it is open
// there and in the record if it is saved.
func (s *Service) renameWindow(ctx context.Context, t *task.Task, from, to string) error {
	from, to = strings.TrimPrefix(from, task.ExtraWindowPrefix), strings.TrimPrefix(to, task.ExtraWindowPrefix)
	oldName, newName := task.ExtraWindowPrefix+from, task.ExtraWindowPrefix+to
	if oldName == task.RootWindowName || newName == task.RootWindowName {
		return fmt.Errorf("only extra windows can be renamed; the root and repository windows are named by the task")
	}
	if err := task.ValidateName("window", to); err != nil {
		return err
	}
	i, saved := t.SavedWindow(from)
	session := s.Runtime.SessionName(t.Name)
	// Without the session's state, an open window could keep its old name
	// while the saved one changes, and start would open it twice.
	st, err := s.Runtime.State(ctx, session)
	if err != nil {
		return err
	}
	open := false
	if st.Exists {
		_, open = st.Pane(s.Runtime.WindowName(oldName))
		if _, taken := st.Pane(s.Runtime.WindowName(newName)); taken {
			return fmt.Errorf("%s's session already has a window %s", t.Name, newName)
		}
	}
	if _, taken := t.SavedWindow(to); taken {
		return fmt.Errorf("%s already has a saved window %s", t.Name, newName)
	}
	if !open && !saved {
		return fmt.Errorf("%s has no window %s, open or saved%s", t.Name, oldName, savedWindowsHint(t))
	}
	if open {
		if err := s.Runtime.RenameWindow(ctx, session, oldName, newName); err != nil {
			return err
		}
	}
	if saved {
		t.Windows[i].Name = to
		if err := s.Store.Save(t); err != nil {
			return err
		}
	} else if err := s.Store.RenameWindow(t.Name, from, to); err != nil {
		s.warnf("tspace: %v", err)
	}
	where := "open and saved"
	switch {
	case !saved:
		where = "open"
	case !open:
		where = "saved"
	}
	s.report(Info{Text: fmt.Sprintf("renamed %s to %s in %s (%s)", oldName, newName, t.Name, where)})
	return nil
}

func savedWindowsHint(t *task.Task) string {
	if len(t.Windows) == 0 {
		return ""
	}
	names := make([]string, len(t.Windows))
	for i, w := range t.Windows {
		names[i] = w.SessionName()
	}
	return " (saved: " + strings.Join(names, ", ") + ")"
}

// taskAgent is the agent `window --agent` runs: the one the task's root
// window runs, or for a task created with a shell root, the user's default.
func (s *Service) taskAgent(t *task.Task) ([]string, error) {
	if len(t.Agent) > 0 {
		return t.Agent, nil
	}
	c, err := s.Store.LoadConfig()
	if err != nil {
		return nil, err
	}
	if len(c.Agent) == 0 {
		return nil, fmt.Errorf("task %s has no agent and no default is set; give a command after --, or set one with: tspace config agent CMD", t.Name)
	}
	return c.Agent, nil
}
