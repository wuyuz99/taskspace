package status

import (
	"slices"
	"testing"

	tsgit "github.com/wuyuz99/taskspace/internal/git"
)

func TestRollup(t *testing.T) {
	got := TaskStatus{Repos: []RepoStatus{
		{Repo: "clean", Git: GitClean},
		{Repo: "dirty", Git: GitDirty, Detail: tsgit.Status{Modified: 1}},
		{Repo: "stash", Git: GitDirty, Detail: tsgit.Status{Stashes: 1}},
		{Repo: "ahead", Git: GitClean, Detail: tsgit.Status{Ahead: 2}},
		{Repo: "new", Git: GitClean, Unpushed: 1},
		{Repo: "gone", Git: GitMissing},
		{Repo: "bad", Git: GitError},
	}}.Rollup()
	if got.Repos != 7 || got.Missing != 1 || got.Errors != 1 {
		t.Fatalf("%+v", got)
	}
	if !slices.Equal(got.Holding, []string{"dirty", "stash"}) || !slices.Equal(got.Unpushed, []string{"ahead", "new"}) {
		t.Fatalf("%+v", got)
	}
}
