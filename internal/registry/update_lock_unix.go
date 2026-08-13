//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package registry

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockUpdateFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX)
}

func unlockUpdateFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
