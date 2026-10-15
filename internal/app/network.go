package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/fernandocpaz/tailg/internal/agent"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
	"github.com/fernandocpaz/tailg/internal/tui"
)

func validateNetworkOptions(options Options) error {
	if options.NetworkOutput != "" && options.NetworkOutput != "text" && options.NetworkOutput != "json" && options.NetworkOutput != "ndjson" {
		return fmt.Errorf("--output must be json, ndjson, or text")
	}
	if options.NetworkOutput == "json" || options.NetworkOutput == "ndjson" {
		if options.NetworkMaxBytes < 4096 || options.NetworkMaxBytes > 16*1024*1024 {
			return fmt.Errorf("--max-bytes must be between 4096 and 16777216")
		}
	}
	if options.Target != "" || options.Status || options.Versions || options.DumpRequested || options.DeploymentDump || options.SplitPanes || options.TileWindows || options.Selector != "" {
		return fmt.Errorf("--network reviews the namespace; use it without a target, --status, --versions, --dump, --deployment-dump, layout flags, or --selector")
	}
	return nil
}

func runNetwork(ctx context.Context, runner kube.Runner, options Options, stdin io.Reader, stdout, stderr io.Writer) int {
	if runner.Context == "" || runner.Namespace == "" {
		kubeContext, namespace, err := runner.CurrentContext(ctx)
		if err != nil {
			if options.NetworkOutput == "json" || options.NetworkOutput == "ndjson" {
				_ = agent.WriteError(stdout, "COLLECTION_FAILED", err.Error(), options.NetworkOutput)
				return 3
			}
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
	if options.NetworkOutput == "json" || options.NetworkOutput == "ndjson" {
		raw := runner.NetworkReview(ctx)
		report, err := agent.LimitNetworkReport(agent.BuildNetworkReport(raw, nil, options.NetworkMaxBytes), options.NetworkOutput)
		if err != nil {
			_ = agent.WriteError(stdout, "OUTPUT_FAILED", err.Error(), options.NetworkOutput)
			return 3
		}
		if err = agent.WriteNetworkReport(stdout, report, options.NetworkOutput); err != nil {
			fmt.Fprintln(stderr, err)
			return 3
		}
		return agent.NetworkExitCode(report)
	}
	decorated, _ := terminalPresentation(stdout)
	if options.NetworkOutput == "text" || !interactive(stdin) || !decorated || !options.LiveFilter || options.NoFollow {
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

func collectNetworkAgent(parent context.Context, options agentOptions) (agent.NetworkReport, error) {
	if options.Target != "" || options.Selector != "" {
		return agent.NetworkReport{}, fmt.Errorf("network diagnostics review the whole namespace; omit target and selector")
	}
	if options.Timeout <= 0 {
		return agent.NetworkReport{}, fmt.Errorf("--timeout must be greater than zero")
	}
	if options.MaxBytes < 4096 || options.MaxBytes > 16*1024*1024 {
		return agent.NetworkReport{}, fmt.Errorf("--max-bytes must be between 4096 and 16777216")
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		return agent.NetworkReport{}, fmt.Errorf("kubectl was not found in PATH")
	}
	ctx, cancel := context.WithTimeout(parent, options.Timeout)
	defer cancel()
	runner := kube.NewRunner(options.Namespace, options.Context)
	if runner.Namespace == "" || runner.Context == "" {
		kubeContext, namespace, err := runner.CurrentContext(ctx)
		if err != nil {
			return agent.NetworkReport{}, err
		}
		if runner.Context == "" {
			runner.Context = kubeContext
		}
		if runner.Namespace == "" {
			runner.Namespace = namespace
		}
	}
	return agent.LimitNetworkReport(agent.BuildNetworkReport(runner.NetworkReview(ctx), nil, options.MaxBytes), "json")
}
