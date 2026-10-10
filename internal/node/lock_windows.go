package node

import (
	"errors"
	"os"
	"syscall"
)

// lockExclusive opens path without sharing it: a second open, from any
// process, fails until this handle closes. (Deleting it stays allowed, so
// removing the whole folder never trips over the lock.)
func lockExclusive(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		const errorSharingViolation = syscall.Errno(32)
		if errors.Is(err, errorSharingViolation) {
			return nil, ErrProfileInUse
		}
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}
