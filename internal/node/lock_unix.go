//go:build !windows

package node

import (
	"errors"
	"os"
	"syscall"
)

// lockExclusive holds an advisory lock on path: a second lock, from any
// process, fails until this file closes.
func lockExclusive(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrProfileInUse
		}
		return nil, err
	}
	return f, nil
}
