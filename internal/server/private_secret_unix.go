//go:build !windows

package server

import (
	"fmt"
	"os"
	"syscall"
)

func openPrivateSecretFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

func checkPrivateSecretOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("secret file must be owned by the service user")
	}
	return nil
}
