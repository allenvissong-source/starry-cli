//go:build darwin || linux

package cli

import (
	"os"
	"syscall"
)

func lockState(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockState(file *os.File) {
	syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
