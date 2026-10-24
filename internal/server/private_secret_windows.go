//go:build windows

package server

import (
	"fmt"
	"os"
)

func openPrivateSecretFile(path string) (*os.File, error) { return os.Open(path) }

func checkPrivateSecretOwner(info os.FileInfo) error {
	return fmt.Errorf("private secret files require POSIX ownership and permissions; use Linux or macOS")
}
