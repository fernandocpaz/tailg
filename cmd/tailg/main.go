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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	command := app.NewCommand(ctx, os.Stdin, os.Stdout, os.Stderr)
	if err := command.Execute(); err != nil {
		if app.ExitCode(err) == 1 {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(app.ExitCode(err))
	}
}
