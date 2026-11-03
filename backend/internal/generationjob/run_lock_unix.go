//go:build linux || darwin

package generationjob

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockRunFile(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func unlockRunFile(file *os.File)     { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }
