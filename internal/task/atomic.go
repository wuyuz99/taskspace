package task

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path with data through a temporary file beside it,
// creating the directory if needed, so a reader or a crash sees the old
// contents or the new, never part of either.
func WriteFileAtomic(path string, data []byte, mode fs.FileMode) error {
	return writeFile(path, data, mode, true)
}

// writeFile is WriteFileAtomic, with sync saying whether the data must reach
// the disk before the file is replaced.
func writeFile(path string, data []byte, mode fs.FileMode, sync bool) error {
	tmp, err := writeTemp(path, data, mode, sync)
	if err != nil {
		return err
	}
	defer os.Remove(tmp) // no-op once the rename succeeds
	return os.Rename(tmp, path)
}

// writeTemp writes data to a new temporary file beside path and returns its
// name, for the caller to publish and remove. The name starts with a dot and
// ends in .tmp, which listings skip.
func writeTemp(path string, data []byte, mode fs.FileMode, sync bool) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", err
	}
	name := f.Name()
	_, err = f.Write(data)
	if err == nil && sync {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, mode)
	}
	if err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}
