//go:build unix

package engine

import (
	"errors"
	"os"
	"syscall"
)

// fileLock is an exclusive flock on an open lock file.
type fileLock struct {
	path string
	file *os.File
}

// tryFileLock takes the lock at path without waiting. It returns nil with no
// error when another process holds it.
func tryFileLock(path string) (*fileLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, err
	}

	// The previous holder removes the file as it lets go, so a lock taken on a
	// file that is no longer the one at path guards nothing: try again.
	opened, err := file.Stat()
	if err == nil {
		var current os.FileInfo
		if current, err = os.Stat(path); err == nil && os.SameFile(opened, current) {
			return &fileLock{path: path, file: file}, nil
		}
	}
	_ = file.Close()
	return nil, nil
}

// release removes the lock file while still holding it, then lets go, so no
// process can take a lock on a name that is about to disappear.
func (l *fileLock) release() {
	_ = os.Remove(l.path)
	_ = l.file.Close()
}

// lockDirRace reports whether err is what creating something inside a
// directory looks like while another process removes that directory: ENOENT,
// or on some systems EINVAL.
func lockDirRace(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.EINVAL)
}
