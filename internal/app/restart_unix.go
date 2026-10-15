//go:build !windows

package app

import (
	"fmt"
	"os"
	"syscall"
)

func Restart(path string, args []string) error {
	if path == "" {
		return fmt.Errorf("executable path unavailable; start tailg again")
	}
	return syscall.Exec(path, append([]string{path}, args...), os.Environ())
}
