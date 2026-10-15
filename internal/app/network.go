package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
	"github.com/fernandocpaz/tailg/internal/tui"
)

func validateNetworkOptions(options Options) error {
	if options.Target != "" || options.Status || options.Versions || options.DumpRequested || options.DeploymentDump || options.SplitPanes || options.TileWindows || options.Selector != "" {
		return fmt.Errorf("--network reviews the namespace; use it without a target, --status, --versions, --dump, --deployment-dump, layout flags, or --selector")
	}
	return nil
}

func runNetwork(ctx context.Context, runner kube.Runner, options Options, stdin io.Reader, stdout, stderr io.Writer) int {
	if runner.Context == "" || runner.Namespace == "" {
		kubeContext, namespace, err := runner.CurrentContext(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if runner.Context == "" {
			runner.Context = kubeContext
		}
		if runner.Namespace == "" {
			runner.Namespace = namespace
		}
	}
	decorated, _ := terminalPresentation(stdout)
	if !interactive(stdin) || !decorated || !options.LiveFilter || options.NoFollow {
		report := runner.NetworkReview(ctx)
		fmt.Fprint(stdout, tui.NetworkText(report))
		if len(report.Errors) > 0 {
			return 1
		}
		return 0
	}
	err := tui.RunNetwork(ctx, core.NetworkReport{Context: runner.Context, Namespace: runner.Namespace}, runner.NetworkReview, stdin, stdout, !options.NoColor, startupUpdates(ctx, options, stdin, stdout))
	if errors.Is(err, tui.ErrRestart) {
		return RestartExitCode
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
