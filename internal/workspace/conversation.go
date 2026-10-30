package workspace

import (
	"os"
	"path/filepath"

	"github.com/wuyuz99/taskspace/internal/task"
)

// ConversationCarrier moves the conversation history an agent keeps for a
// directory when the directory moves. Each knows one agent's own storage and
// is an opt-in integration named after it; core tspace knows none.
type ConversationCarrier interface {
	// Carry moves the history kept for directory from to directory to,
	// which from has just moved to. It reports false, and changes nothing,
	// when there is no history for from.
	Carry(from, to string) (bool, error)
}

// movedDir is a directory an operation moves, and the window that runs in it.
// real is from with symbolic links resolved, when that differs, since an
// agent may have kept its history under either.
type movedDir struct{ window, from, to, real string }

func newMovedDir(window, from, to string) movedDir {
	d := movedDir{window: window, from: from, to: to}
	if r, err := filepath.EvalSymlinks(from); err == nil && r != from {
		d.real = r
	}
	return d
}

// carriers are the conversation carriers c turns on, in its order.
func (s *Service) carriers(c *task.Config) []string {
	var out []string
	for _, name := range c.CarryConversations {
		if _, ok := s.Carriers[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

// carryConversations has each chosen carrier move the history kept for the
// moved directories, now that they have moved, under both spellings of a
// path through a symbolic link, and returns the windows whose history went
// along. A carrier that fails is reported and the rest still run.
func (s *Service) carryConversations(taskName string, chosen []string, dirs []movedDir) map[string]bool {
	carried := map[string]bool{}
	for _, d := range dirs {
		pairs := [][2]string{{d.from, d.to}}
		if d.real != "" {
			if to, err := filepath.EvalSymlinks(d.to); err == nil {
				pairs = append(pairs, [2]string{d.real, to})
			}
		}
		for _, name := range chosen {
			for _, p := range pairs {
				ok, err := s.Carriers[name].Carry(p[0], p[1])
				if err != nil {
					s.warnf("could not carry %s's %s conversation (%s) from %s: %v", taskName, d.window, name, p[0], err)
				} else if ok {
					carried[d.window] = true
					s.report(ConversationCarried{Task: taskName, Window: d.window, Agent: name, From: p[0], To: p[1]})
				}
			}
		}
	}
	return carried
}

// resumedByDirectory are the windows whose agent has run and resumes, so a
// conversation it keeps by directory stays behind when root and the
// worktrees in it move; each with the directory it ran in.
func (s *Service) resumedByDirectory(t *task.Task, root string) []movedDir {
	var out []movedDir
	windows := append([]string{task.RootWindowName}, t.RepoNames()...)
	for _, window := range windows {
		if len(t.WindowAgent(window)) == 0 || len(t.ResumeFor(window)) == 0 || !s.Store.Launched(t.Name, window) {
			continue
		}
		dir := root
		if window != task.RootWindowName {
			dir = t.Repos[window].Worktree
			if _, err := os.Stat(dir); err != nil {
				continue
			}
		}
		out = append(out, movedDir{window: window, from: dir})
	}
	return out
}

// warnConversationsLeft says, for each window not carried, that its agent's
// conversation stays in the directory it ran in.
func (s *Service) warnConversationsLeft(taskName string, resumed []movedDir, carried map[string]bool) {
	for _, d := range resumed {
		if !carried[d.window] {
			s.warnf("note: %s's %s agent resumes by directory; its conversation from %s stays there", taskName, d.window, d.from)
		}
	}
}
