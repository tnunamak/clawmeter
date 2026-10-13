//go:build !windows

package anthropic

import (
	"os"

	"golang.org/x/sys/unix"
)

func tryRenewalLock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}
