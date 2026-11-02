package fspath

import (
	"os"
	"path/filepath"
	"testing"
)

// tree makes root/real/sub and a link to real, or skips without symlinks.
func tree(t *testing.T) (real, link string) {
	t.Helper()
	root := t.TempDir()
	real = filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	return real, link
}

func TestSame(t *testing.T) {
	real, link := tree(t)
	for _, c := range []struct {
		x, y string
		want bool
	}{
		{real, link, true},
		{real, real + string(filepath.Separator), true},
		{real, filepath.Join(real, "sub"), false},
		{filepath.Join(real, "gone"), filepath.Join(real, "x", "..", "gone"), true},
		{filepath.Join(real, "gone"), filepath.Join(real, "other"), false},
	} {
		if got := Same(c.x, c.y); got != c.want {
			t.Errorf("Same(%q, %q) = %v", c.x, c.y, got)
		}
	}
}

func TestWithin(t *testing.T) {
	real, link := tree(t)
	sibling := real + "2"
	if err := os.Mkdir(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path, root string
		want       bool
	}{
		{real, real, true},
		{filepath.Join(real, "sub"), real, true},
		{filepath.Join(link, "sub"), real, true},
		{filepath.Join(real, "sub"), link, true},
		{sibling, real, false}, // a shared prefix is not a parent
		{filepath.Dir(real), real, false},
	} {
		if got := Within(c.path, c.root); got != c.want {
			t.Errorf("Within(%q, %q) = %v", c.path, c.root, got)
		}
	}
}

// A link in a directory that leads out of it still counts as inside, as
// written: what guards a directory errs toward refusing.
func TestWithinCountsALinkAsWritten(t *testing.T) {
	real, link := tree(t)
	inner := filepath.Join(real, "out")
	if err := os.Symlink(filepath.Dir(link), inner); err != nil {
		t.Skip(err)
	}
	if !Within(inner, real) {
		t.Error("not within as written")
	}
}

func TestCanonicalResolvesWhatExists(t *testing.T) {
	real, link := tree(t)
	got := Canonical(filepath.Join(link, "gone", "deeper"))
	want := filepath.Join(Canonical(real), "gone", "deeper")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if Canonical("") != "" {
		t.Fatal("empty path")
	}
}

func TestKeyMatchesSpellings(t *testing.T) {
	real, link := tree(t)
	if Key(real) != Key(link+string(filepath.Separator)) {
		t.Fatalf("%q and %q differ", Key(real), Key(link))
	}
	if Key("") != "" {
		t.Fatal("empty path")
	}
}
