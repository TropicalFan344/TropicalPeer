package node

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrProfileInUse means another running TropicalPeer (the app, a second
// copy of it, or tpcli) already uses this data folder. Two copies with one
// identity would keep taking the server registration from each other, so
// peers would reach one, then the other.
var ErrProfileInUse = errors.New("another copy of TropicalPeer is already using this profile")

const lockFile = "lock"

// lockProfile takes the data folder for this process until release. The OS
// drops the lock if the process dies, so a crash never leaves it stuck.
func lockProfile(dir string) (release func(), err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := lockExclusive(filepath.Join(dir, lockFile))
	if err != nil {
		return nil, err
	}
	return func() { f.Close() }, nil
}
