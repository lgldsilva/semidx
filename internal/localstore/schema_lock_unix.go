//go:build !windows

package localstore

import (
	"os"

	"golang.org/x/sys/unix"
)

func flockExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

func flockUnlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
