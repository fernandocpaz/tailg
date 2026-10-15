package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/fernandocpaz/tailg/internal/app"
)

func main() {
	// Capture before updating: a renamed running image may later report the
	// backup path from os.Executable on Unix.
	executable, _ := os.Executable()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	command := app.NewCommand(ctx, os.Stdin, os.Stdout, os.Stderr)
	if err := command.Execute(); err != nil {
		if app.ExitCode(err) == app.RestartExitCode {
			if restartErr := app.Restart(executable, os.Args[1:]); restartErr != nil {
				if app.IsExitError(restartErr) {
					os.Exit(app.ExitCode(restartErr))
				}
				fmt.Fprintln(os.Stderr, "Updated, but restart failed:", restartErr)
				os.Exit(1)
			}
			return
		}
		if !app.IsExitError(err) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(app.ExitCode(err))
	}
}
