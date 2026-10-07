package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestStatusJSON(t *testing.T) {
	h := newHarness(t)

	// No tasks is still valid JSON, not the prose hint.
	if err := h.exec(t, h.app.newStatusCmd(), "--json"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(h.out.String()); got != "[]" {
		t.Fatalf("empty status: %q", got)
	}

	tk := h.startedTask(t, "PAY-142")
	name := tk.RepoNames()[0]
	wt := tk.Repos[name].Worktree
	if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("unsaved"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := h.exec(t, h.app.newStatusCmd(), "PAY-142", "--json"); err != nil {
		t.Fatal(err)
	}
	var got []statusJSON
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, h.out.String())
	}
	if len(got) != 1 || got[0].Task != "PAY-142" || got[0].Session != "aw-PAY-142" || got[0].Running {
		t.Fatalf("task: %+v", got)
	}
	if got[0].Root.Agent != "shell" || got[0].Root.Runtime != "no-session" {
		t.Fatalf("root: %+v", got[0].Root)
	}
	if len(got[0].Repos) != 1 {
		t.Fatalf("repos: %+v", got[0].Repos)
	}
	r := got[0].Repos[0]
	if r.Repo != name || r.Worktree != wt || r.Branch != "agent/PAY-142" || r.Runtime != "no-session" {
		t.Fatalf("repo: %+v", r)
	}
	if r.Git != "dirty" || r.Untracked != 1 {
		t.Fatalf("git: %+v", r)
	}
}

func TestTaskJSON(t *testing.T) {
	h := newHarness(t)

	if err := h.exec(t, h.app.newTaskCmd(), "list", "--json"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(h.out.String()); got != "[]" {
		t.Fatalf("empty list: %q", got)
	}

	if err := h.exec(t, h.app.newTaskCmd(), "create", "PAY-142", "--repo", "backend="+h.repo,
		"--title", "Checkout", "--agent", "claude", "--agent=--model", "--agent", "opus"); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newTaskCmd(), "handoff", "PAY-142", "tests pass"); err != nil {
		t.Fatal(err)
	}

	if err := h.exec(t, h.app.newTaskCmd(), "show", "PAY-142", "--json"); err != nil {
		t.Fatal(err)
	}
	var got taskJSON
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, h.out.String())
	}
	if got.Name != "PAY-142" || got.Title != "Checkout" || got.Handoff != "tests pass" || got.HandoffUpdatedAt == nil {
		t.Fatalf("task: %+v", got)
	}
	if got.Dir != h.app.Store.TaskDir("PAY-142") || got.ContextDir != h.app.Store.ContextDir("PAY-142") {
		t.Fatalf("paths: %+v", got)
	}
	if !slices.Equal(got.Agent, []string{"claude", "--model", "opus"}) {
		t.Fatalf("agent: %q", got.Agent)
	}
	if len(got.Repos) != 1 || got.Repos[0].Name != "backend" || got.Repos[0].Branch != "agent/PAY-142" ||
		got.Repos[0].Worktree != h.app.Store.WorktreePath("PAY-142", "backend") {
		t.Fatalf("repos: %+v", got.Repos)
	}
	// A shell is an empty array, never null, so a reader need not special-case it.
	if !strings.Contains(h.out.String(), `"agent": []`) {
		t.Fatalf("repo agent: %s", h.out.String())
	}

	if err := h.exec(t, h.app.newTaskCmd(), "list", "--json"); err != nil {
		t.Fatal(err)
	}
	var list []taskJSON
	if err := json.Unmarshal(h.out.Bytes(), &list); err != nil {
		t.Fatalf("%v\n%s", err, h.out.String())
	}
	if len(list) != 1 || list[0].Name != "PAY-142" {
		t.Fatalf("list: %+v", list)
	}
}

// `aw config repo add PATH` must not register a repository called "add".
func TestConfigRepoRejectsSubcommandNames(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"repo", "add", h.repo},
		{"repo", "rm", h.repo},
		{"repo", "list"},
	} {
		err := h.exec(t, h.app.newConfigCmd(), args...)
		if err == nil || !strings.Contains(err.Error(), "aw config repo") {
			t.Fatalf("%v: %v", args, err)
		}
	}
	c, err := h.app.Store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Repos) != 0 {
		t.Fatalf("registered: %v", c.Repos)
	}
}
