//go:build !unix

package statusstream

import "os"

func tryLockFile(_ *os.File) (bool, error) {
	return true, nil
}

func unlockFile(_ *os.File) error {
	return nil
}
