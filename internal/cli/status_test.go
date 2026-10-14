package cli

import (
	"testing"

	tsgit "github.com/wuyuz99/taskspace/internal/git"
	"github.com/wuyuz99/taskspace/internal/status"
)

func TestGitSummary(t *testing.T) {
	cases := []struct {
		name string
		row  status.RepoStatus
		want string
	}{
		{"clean", status.RepoStatus{Git: status.GitClean}, "clean"},
		{"missing", status.RepoStatus{Git: status.GitMissing}, "missing"},
		{"error", status.RepoStatus{Git: status.GitError}, "error"},
		{
			"one dirty file",
			status.RepoStatus{Git: status.GitDirty, Detail: tsgit.Status{Modified: 1}},
			"dirty (1 file)",
		},
		{
			"several dirty files",
			status.RepoStatus{Git: status.GitDirty, Detail: tsgit.Status{Modified: 2, Untracked: 1}},
			"dirty (3 files)",
		},
		{
			"clean but ahead",
			status.RepoStatus{Git: status.GitClean, Detail: tsgit.Status{Ahead: 2}},
			"clean, ahead 2",
		},
		{
			"stash only",
			status.RepoStatus{Git: status.GitDirty, Detail: tsgit.Status{Stashes: 1}},
			"clean, 1 stash",
		},
		{
			"everything at once",
			status.RepoStatus{Git: status.GitDirty, Detail: tsgit.Status{Modified: 1, Stashes: 2, Ahead: 1, Behind: 3}},
			"dirty (1 file), 2 stashes, ahead 1, behind 3",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := gitSummary(c.row); got != c.want {
				t.Errorf("GitSummary() = %q, want %q", got, c.want)
			}
		})
	}
}
