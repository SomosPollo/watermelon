//go:build linux

package ask

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func fileIsForegroundTerminal(file *os.File) bool {
	if !fileIsTerminal(file) {
		return false
	}
	return fdIsForegroundTerminal(int(file.Fd()))
}

func fdIsForegroundTerminal(fd int) bool {
	foreground, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	return err == nil && foreground == syscall.Getpgrp()
}
