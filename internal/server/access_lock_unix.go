//go:build !windows

package server

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockAccessFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}
