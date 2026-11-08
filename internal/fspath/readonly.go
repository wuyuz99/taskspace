package fspath

import (
	"io/fs"
	"os"
	"path/filepath"
)

// SetReadOnly makes every regular file under dir read-only, or writable
// again, leaving the directories writable so git can still replace files and
// clean can remove them. The top-level .git, which is git's own, is skipped,
// and so are symbolic links, whose own mode is not the file's.
//
// On Windows it sets or clears the read-only attribute, which also keeps a
// file from being deleted; callers make a tree writable before git changes
// it. A file it cannot change is skipped and the first such error returned,
// once it has done what it can.
func SetReadOnly(dir string, readOnly bool) error {
	var first error
	keep := func(err error) {
		if first == nil {
			first = err
		}
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			keep(err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() == ".git" && filepath.Dir(path) == filepath.Clean(dir) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			keep(err)
			return nil
		}
		mode := info.Mode().Perm()
		want := mode | 0o200
		if readOnly {
			want = mode &^ 0o222
		}
		if want != mode {
			if err := os.Chmod(path, want); err != nil {
				keep(err)
			}
		}
		return nil
	})
	if err != nil {
		keep(err)
	}
	return first
}
