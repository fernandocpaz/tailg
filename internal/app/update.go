package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/fernandocpaz/tailg/internal/update"
	"github.com/spf13/cobra"
)

const RestartExitCode = 75

func startupUpdates(ctx context.Context, options Options, stdin io.Reader, stdout io.Writer) *update.Client {
	input, inputOK := stdin.(*os.File)
	output, outputOK := stdout.(*os.File)
	disabled := strings.ToLower(os.Getenv("TAILG_NO_UPDATE_CHECK"))
	if options.NoUpdateCheck || disabled == "1" || disabled == "true" || !inputOK || !outputOK || !term.IsTerminal(input.Fd()) || !term.IsTerminal(output.Fd()) {
		return nil
	}
	if !options.LiveFilter || options.NoFollow || options.Status || options.Versions || options.DumpRequested || options.DeploymentDump || options.FilterFile != "" || len(options.TracePods) > 0 || len(options.TraceSelectors) > 0 {
		return nil
	}
	return update.NewSession(ctx, currentBuildMetadata().version)
}

func newUpdateCommand(ctx context.Context, stdout io.Writer) *cobra.Command {
	var checkOnly bool
	command := &cobra.Command{Use: "update", Short: "Check for and install the latest stable tailg release", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := update.New(currentBuildMetadata().version)
			client.Cleanup()
			checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			release, err := client.Check(checkCtx)
			cancel()
			if err != nil {
				return fmt.Errorf("check for updates: %w", err)
			}
			fmt.Fprintf(stdout, "Installed: %s · Latest: %s\n", client.Version, release.Version)
			if checkOnly {
				return nil
			}
			if !update.Newer(client.Version, release.Version) && update.KnownVersion(client.Version) {
				fmt.Fprintln(stdout, "Already up to date; no changes made.")
				return nil
			}
			if err := client.Install(ctx, release); err != nil {
				return fmt.Errorf("update failed: %w\nManual install: go install github.com/fernandocpaz/tailg/cmd/tailg@%s", err, release.Version)
			}
			fmt.Fprintf(stdout, "Updated to %s. Start tailg to use the new version.\n", release.Version)
			return nil
		}}
	command.Flags().BoolVar(&checkOnly, "check", false, "show installed and latest versions without installing")
	return command
}
