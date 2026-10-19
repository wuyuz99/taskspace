package task

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// The locked range lies far past anything written, because LockFileEx stops
// other processes reading the bytes it covers and waiters read the holder's
// details from the file.
const lockOffset = 1 << 30

// tryLockFile takes an exclusive lock on f without waiting, reporting false
// if another handle holds it.
func tryLockFile(f *os.File) (bool, error) {
	ol := &windows.Overlapped{Offset: lockOffset}
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION), errors.Is(err, windows.ERROR_IO_PENDING):
		return false, nil
	default:
		return false, err
	}
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{Offset: lockOffset})
}
