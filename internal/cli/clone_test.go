package cli

import (
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
