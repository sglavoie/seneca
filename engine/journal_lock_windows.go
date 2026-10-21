//go:build windows

package engine

import (
	"errors"
	"os"
	"syscall"
)

const (
	fileFlagDeleteOnClose      = 0x04000000
	errnoSharingViolation      = syscall.Errno(32)
	errnoAccessDeniedOnPending = syscall.Errno(5)
)

// fileLock is a handle opened with no sharing: while it is open no other
// process can open the lock file, and closing it deletes the file.
type fileLock struct {
	path   string
	handle syscall.Handle
}

// tryFileLock takes the lock at path without waiting. It returns nil with no
// error when another process holds it.
func tryFileLock(path string) (*fileLock, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
		syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL|fileFlagDeleteOnClose, 0)
	if err != nil {
		if errors.Is(err, errnoSharingViolation) || errors.Is(err, errnoAccessDeniedOnPending) {
			return nil, nil
		}
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return &fileLock{path: path, handle: handle}, nil
}

func (l *fileLock) release() {
	_ = syscall.CloseHandle(l.handle)
}

// lockDirRace reports whether err is what creating something inside a
// directory looks like while another process removes that directory.
func lockDirRace(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
