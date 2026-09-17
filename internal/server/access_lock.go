package server

import (
	"fmt"
	"os"
	"path/filepath"
)

// LockAccess holds an OS lock until Close or process exit. Keep the lock file:
// removing it permits two processes to lock different inodes at the same path.
func LockAccess(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "access.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = lockAccessFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("access directory is in use; stop the server before provisioning: %w", err)
	}
	return f, nil
}
