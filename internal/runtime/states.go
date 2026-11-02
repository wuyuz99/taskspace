package runtime

import (
	"context"
	"strings"

	tsexec "github.com/wuyuz99/taskspace/internal/exec"
)

// SessionRead is one session in a StateLister's answer: what State would have
// returned for it.
type SessionRead struct {
	State SessionState
	Err   error
}

// StateLister is optional: a multiplexer that can report every tspace session
// with one query. A refresh of many tasks then costs one query, not one or two
// per task. A session not in the answer has no session running.
type StateLister interface {
	States(ctx context.Context) (map[string]SessionRead, error)
}

// States implements StateLister: one list-panes across the server.
func (t *Tmux) States(ctx context.Context) (map[string]SessionRead, error) {
	res, err := t.tmux(ctx, "list-panes", "-a", "-F", "#{session_name}\t"+stateFormat)
	if err != nil {
		if tsexec.IsExit(err) && noSession(res.Stderr) {
			return map[string]SessionRead{}, nil
		}
		return nil, err
	}
	lines := map[string]*strings.Builder{}
	for _, raw := range strings.Split(res.Stdout, "\n") {
		session, rest, ok := strings.Cut(strings.TrimRight(raw, "\r"), "\t")
		if !ok || !strings.HasPrefix(session, sessionPrefix) {
			continue
		}
		if lines[session] == nil {
			lines[session] = &strings.Builder{}
		}
		lines[session].WriteString(rest + "\n")
	}
	out := make(map[string]SessionRead, len(lines))
	for session, b := range lines {
		out[session] = SessionRead{State: SessionState{Name: session, Exists: true, Panes: parsePanes(b.String())}}
	}
	return out, nil
}

// States implements StateLister: the session list, and panes only for the
// sessions that are up, so a stopped task costs nothing. Commands running
// while a session starts are what can take it down; see create.
func (z *Zellij) States(ctx context.Context) (map[string]SessionRead, error) {
	all, err := z.sessions(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]SessionRead{}
	for session, e := range all {
		if e.exited || !strings.HasPrefix(session, sessionPrefix) {
			continue
		}
		panes, exists, err := z.panes(ctx, session)
		out[session] = SessionRead{State: paneState(session, panes, exists), Err: err}
	}
	return out, nil
}
