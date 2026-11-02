// Package fspath is how tspace compares paths. It decides what tspace moves
// and removes, so there is one set of rules:
//
//   - Symbolic links count: two spellings of a directory are the same place.
//   - A path that does not exist is compared by its cleaned name, ignoring
//     case where the file system usually does.
//   - Where the answer guards a directory, a path that could be inside it, as
//     written or through its links, counts as inside.
package fspath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// foldsCase says this platform's file systems usually ignore case.
func foldsCase() bool { return runtime.GOOS == "windows" || runtime.GOOS == "darwin" }

// Same reports whether x and y name one place: the same file or directory
// when both exist, else the same cleaned name.
func Same(x, y string) bool {
	if a, err := os.Stat(x); err == nil {
		if b, err := os.Stat(y); err == nil && os.SameFile(a, b) {
			return true
		}
	}
	cx, cy := filepath.Clean(filepath.FromSlash(x)), filepath.Clean(filepath.FromSlash(y))
	if foldsCase() {
		return strings.EqualFold(cx, cy)
	}
	return cx == cy
}

// Within reports whether path is root or below it, as written or with symbolic
// links resolved.
func Within(path, root string) bool {
	return below(path, root) || below(resolved(path), resolved(root))
}

func below(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// Canonical is p made absolute with symbolic links resolved as far as it
// exists. git records worktree paths with links resolved (on macOS /var is
// /private/var), and a worktree deleted by hand cannot be resolved itself, so
// without its parents it would never match its own stale registration.
func Canonical(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	abs = filepath.Clean(abs)
	rest := ""
	for dir := abs; ; {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// Key is the form of dir to use as a map key, so that the path an agent reports
// and the one tspace recorded match when they name the same place.
func Key(dir string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(resolved(dir))
	if foldsCase() {
		dir = strings.ToLower(dir)
	}
	return dir
}

// IsDir reports whether path is a directory.
func IsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
