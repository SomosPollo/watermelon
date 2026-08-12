//go:build linux

package lima

import "golang.org/x/sys/unix"

func renameSSHConfigNoReplace(dirFD int, oldName, newName string) error {
	return unix.Renameat2(dirFD, oldName, dirFD, newName, unix.RENAME_NOREPLACE)
}

func exchangeSSHConfigNames(dirFD int, firstName, secondName string) error {
	return unix.Renameat2(dirFD, firstName, dirFD, secondName, unix.RENAME_EXCHANGE)
}
