//go:build !unix && !windows

package engine

import (
	"errors"
	"os"
)

// fileLock is the lock file's existence, for platforms with no advisory lock.
// A process that dies while holding it leaves the file behind.
type fileLock struct {
	path string
}

// tryFileLock takes the lock at path without waiting. It returns nil with no
// error when another process holds it.
func tryFileLock(path string) (*fileLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, nil
		}
		return nil, err
	}
	_ = file.Close()
	return &fileLock{path: path}, nil
}

func (l *fileLock) release() {
	_ = os.Remove(l.path)
}

// lockDirRace reports whether err is what creating something inside a
// directory looks like while another process removes that directory.
func lockDirRace(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
