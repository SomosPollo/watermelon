//go:build darwin

package lima

import "golang.org/x/sys/unix"

func renameSSHConfigNoReplace(dirFD int, oldName, newName string) error {
	return unix.RenameatxNp(dirFD, oldName, dirFD, newName, unix.RENAME_EXCL)
}

func exchangeSSHConfigNames(dirFD int, firstName, secondName string) error {
	return unix.RenameatxNp(dirFD, firstName, dirFD, secondName, unix.RENAME_SWAP)
}
