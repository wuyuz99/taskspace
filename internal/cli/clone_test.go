package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLooksLikeURL(t *testing.T) {
	for s, want := range map[string]bool{
		"https://github.com/o/r.git": true,
		"ssh://git@host/o/r":         true,
		"file:///srv/r.git":          true,
		"git@github.com:o/r.git":     true,
		"~/src/backend":              false,
		"../backend":                 false,
		`C:\src\backend`:             false,
		"C:/src/backend":             false,
		"backend":                    false,
		"proj:v2":                    false,
		"github.com:o/r.git":         true,
	} {
		if got := looksLikeURL(s); got != want {
			t.Errorf("looksLikeURL(%q) = %v", s, got)
		}
	}
}

// fileURL is a URL git clones repo from.
func fileURL(repo string) string {
	p := filepath.ToSlash(repo)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

func TestConfigRepoClonesAURL(t *testing.T) {
	h := newHarness(t)
	bare := filepath.Join(t.TempDir(), "backend.git")
	gitIn(t, h.repo, "clone", "-q", "--bare", h.repo, bare)
	url := fileURL(bare)

	if err := h.exec(t, h.app.newConfigCmd(), "repo", "backend", url); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(h.app.Store.ReposDir(), "backend")
	c, err := h.app.Store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(c.Repos["backend"], want) {
		t.Fatalf("registered %q, want %q", c.Repos["backend"], want)
	}
	if got := gitIn(t, want, "remote", "get-url", "origin"); got != url {
		t.Fatalf("clone's origin %q", got)
	}

	// Again: the clone is there already, and is used as it is.
	if err := h.exec(t, h.app.newConfigCmd(), "repo", "backend", url); err != nil || !strings.Contains(h.out.String(), "already a clone") {
		t.Fatalf("second clone = %v, out %q", err, h.out.String())
	}
	// Something else in the way is refused.
	other := filepath.Join(t.TempDir(), "other.git")
	gitIn(t, h.repo, "clone", "-q", "--bare", h.repo, other)
	if err := h.exec(t, h.app.newConfigCmd(), "repo", "backend", fileURL(other)); err == nil || !strings.Contains(err.Error(), "--into") {
		t.Fatalf("clone over another repository = %v", err)
	}
	// --into chooses the place; and is only for URLs.
	into := filepath.Join(t.TempDir(), "elsewhere")
	if err := h.exec(t, h.app.newConfigCmd(), "repo", "api", fileURL(other), "--into", into); err != nil {
		t.Fatal(err)
	}
	if c, _ := h.app.Store.LoadConfig(); !samePath(c.Repos["api"], into) {
		t.Fatalf("api registered at %q", c.Repos["api"])
	}
	if err := h.exec(t, h.app.newConfigCmd(), "repo", "web", h.repo, "--into", into); err == nil {
		t.Fatal("--into accepted for a path")
	}
}

func TestConfigRepoCloneEdges(t *testing.T) {
	h := newHarness(t)
	bare := filepath.Join(t.TempDir(), "backend.git")
	gitIn(t, h.repo, "clone", "-q", "--bare", h.repo, bare)
	url := fileURL(bare)

	// An empty directory is cloned into.
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.exec(t, h.app.newConfigCmd(), "repo", "a", url, "--into", empty); err != nil {
		t.Fatalf("clone into an empty directory: %v", err)
	}

	// An interrupted clone, its remote set but nothing fetched, is not
	// taken for a finished one.
	half := filepath.Join(t.TempDir(), "half")
	gitIn(t, filepath.Dir(half), "init", "-q", half)
	gitIn(t, half, "remote", "add", "origin", url)
	if err := h.exec(t, h.app.newConfigCmd(), "repo", "b", url, "--into", half); err == nil || !strings.Contains(err.Error(), "unfinished clone") {
		t.Fatalf("half a clone = %v", err)
	}

	// A failed clone leaves nothing behind.
	gone := filepath.Join(t.TempDir(), "gone")
	if err := h.exec(t, h.app.newConfigCmd(), "repo", "c", fileURL(filepath.Join(t.TempDir(), "missing.git")), "--into", gone); err == nil {
		t.Fatal("clone of a missing repository succeeded")
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Fatalf("failed clone left %s: %v", gone, err)
	}

	// --into does nothing without a URL to clone, so it is an error.
	if err := h.exec(t, h.app.newConfigCmd(), "repo", "a", "--into", empty); err == nil {
		t.Fatal("--into accepted without a URL")
	}
	// The destination is checked against other registrations first.
	if err := h.exec(t, h.app.newConfigCmd(), "repo", "d", url, "--into", empty); err == nil || !strings.Contains(err.Error(), `registered as "a"`) {
		t.Fatalf("clone onto another registration = %v", err)
	}
}

func TestRedactURL(t *testing.T) {
	if got := redactURL("https://user:ghp_secret@github.com/o/r.git"); strings.Contains(got, "ghp_secret") || strings.Contains(got, "user:") {
		t.Fatalf("redacted %q", got)
	}
	if got := redactURL("git@github.com:o/r.git"); got != "git@github.com:o/r.git" {
		t.Fatalf("scp form changed: %q", got)
	}
}
