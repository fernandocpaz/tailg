package app

import (
	"fmt"
	"os"
	"os/exec"
)

func Restart(path string, args []string) error {
	if path == "" {
		return fmt.Errorf("executable path unavailable; start tailg again")
	}
	command := exec.Command(path, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Keep the invoking PowerShell/cmd process waiting. Starting a detached
	// child would return a shell prompt that competes with tailg for input.
	if err := command.Run(); err != nil {
		if status, ok := err.(*exec.ExitError); ok {
			return exitError(status.ExitCode())
		}
		return err
	}
	return nil
}
