// Package guards holds the engine's safety checks. Phase 0 ships only the
// single-instance lock; mass-delete and sync-dir guards arrive in phase 2.
package guards

import (
	"fmt"
	"path/filepath"

	"github.com/gofrs/flock"
)

// InstanceLock is a held single-instance lock. Release it with Unlock.
type InstanceLock struct {
	fl *flock.Flock
}

// AcquireInstanceLock takes the exclusive lock at <configDir>/lock,
// failing immediately if another synckeeper process holds it.
func AcquireInstanceLock(configDir string) (*InstanceLock, error) {
	fl := flock.New(filepath.Join(configDir, "lock"))
	ok, err := fl.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire instance lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("another synckeeper instance is running (lock at %s)", fl.Path())
	}
	return &InstanceLock{fl: fl}, nil
}

// Unlock releases the lock.
func (l *InstanceLock) Unlock() error {
	return l.fl.Unlock()
}
