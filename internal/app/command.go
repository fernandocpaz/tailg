package app

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fernandocpaz/tailg/internal/agent"
	"github.com/fernandocpaz/tailg/internal/core"
)

func NewCommand(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	options := Options{Tail: core.DefaultTailLines, BufferLines: core.DefaultBufferLines, RefreshInterval: 2_000_000_000, HeartbeatWindow: core.DefaultHeartbeatWindow, StatusLookback: core.DefaultStatusLookback, StatusInterval: core.DefaultStatusInterval, StatusTimeout: core.DefaultStatusTimeout, Container: ".*", LiveFilter: true}
	var showPod, noShowPod, noLiveFilter bool
	var deployDumpAlias string
	var contexts []string
	var refreshSeconds int
	command := &cobra.Command{
		Use: "tailg [target] [namespace]", Short: "Human-friendly Kubernetes log tailer built in Go", SilenceUsage: true, SilenceErrors: true, Args: cobra.MaximumNArgs(2),
		Long:    "tailg follows Kubernetes logs across pods, provides synchronized live filtering, heartbeat diagnostics, resource inspection, status recovery monitoring, and Windows Terminal layouts.",
		Example: strings.Join([]string{"tailg", "tailg example-app default", "tailg 'example-*' default --tile-windows", "tailg example-app default --since 4d", "tailg --status --namespace default", "tailg --status 20 --namespace default", "tailg --versions", "tailg --versions --context tkgs-qa --context tkgs-dev"}, "\n"),
		Version: BuildDescription(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.Status {
				lookback, remaining, err := statusLookbackFromArgs(args, options.StatusLookback)
				if err != nil {
					return err
				}
				options.StatusLookback, args = lookback, remaining
			}
			if len(args) > 0 {
				options.Target = args[0]
			}
			if len(args) > 1 {
				options.LegacyNamespace = args[1]
			}
			if len(contexts) > 1 && !options.Versions {
				return fmt.Errorf("--context may be repeated only with --versions")
			}
			options.VersionContexts = append([]string(nil), contexts...)
			if len(contexts) == 1 {
				options.Context = contexts[0]
			}
			options.TailSet = cmd.Flags().Changed("tail")
			options.RefreshInterval = time.Duration(refreshSeconds) * time.Second
			options.DumpRequested = cmd.Flags().Changed("dump")
			if cmd.Flags().Changed("deployment-dump") {
				options.DeploymentDump = true
			}
			if cmd.Flags().Changed("deploy-dump") {
				if options.DeploymentDump {
					return fmt.Errorf("use either --deployment-dump or --deploy-dump, not both")
				}
				options.DeploymentDump = true
				options.DeploymentDumpPath = deployDumpAlias
			}
			if options.DumpRequested && options.DeploymentDump {
				return fmt.Errorf("use either --dump or --deployment-dump, not both")
			}
			if showPod && noShowPod {
				return fmt.Errorf("use either --show-pod or --no-show-pod")
			}
			if showPod {
				value := true
				options.ShowPod = &value
			}
			if noShowPod {
				value := false
				options.ShowPod = &value
			}
			if noLiveFilter {
				options.LiveFilter = false
			}
			if options.SplitPanes && options.TileWindows {
				return fmt.Errorf("use either --split-panes or --tile-windows")
			}
			code := Run(ctx, options, stdin, stdout, stderr)
			if code != 0 {
				return exitError(code)
			}
			return nil
		},
	}
	flags := command.Flags()
	flags.StringVarP(&options.Namespace, "namespace", "n", "", "Kubernetes namespace; without a target, open every pod in Windows Terminal")
	flags.StringArrayVar(&contexts, "context", nil, "kubectl context name; repeat with --versions to compare environments")
	flags.BoolVar(&options.Status, "status", false, "scan health and recent errors; optional positional argument is lookback minutes (default 120)")
	flags.BoolVar(&options.Versions, "versions", false, "list pod image versions; compare by Image ID when any matching pod uses latest")
	flags.DurationVar(&options.StatusInterval, "status-interval", core.DefaultStatusInterval, "delay between status scans")
	flags.DurationVar(&options.StatusTimeout, "status-timeout", core.DefaultStatusTimeout, "maximum status recovery wait")
	flags.StringVar(&options.Selector, "selector", "", "label selector override")
	flags.StringVar(&options.Container, "container", ".*", "regular expression selecting container names")
	flags.BoolVar(&options.Detail, "detail", false, "retain trailing structured JSON properties")
	flags.IntVar(&options.Tail, "tail", core.DefaultTailLines, "initial lines per container; -1 means all retained lines")
	flags.IntVar(&options.BufferLines, "buffer-lines", core.DefaultBufferLines, "maximum live log lines retained in memory")
	flags.StringVar(&options.Since, "since", "", "relative time window, for example 30m, 2h, or 4d")
	flags.IntVar(&refreshSeconds, "refresh-seconds", 2, "pod inventory refresh cadence in seconds")
	flags.DurationVar(&options.HeartbeatWindow, "heartbeat-window", core.DefaultHeartbeatWindow, "F5 heartbeat grouping interval")
	flags.StringArrayVar(&options.Include, "include", nil, "regular expression; only matching log lines are shown (repeatable)")
	flags.StringArrayVar(&options.Exclude, "exclude", nil, "regular expression; matching log lines are hidden (repeatable)")
	flags.BoolVar(&options.NoDefaultExclude, "no-default-exclude", false, "show health, ready, and live probe traffic")
	flags.BoolVar(&options.NoFollow, "no-follow", false, "read recent logs and exit")
	flags.StringVar(&options.DumpDirectory, "dump", "", "write a troubleshooting bundle, optionally to DIR")
	flags.Lookup("dump").NoOptDefVal = "."
	flags.StringVar(&options.DeploymentDumpPath, "deployment-dump", "", "write a deployment-focused troubleshooting bundle, optionally to DIR")
	flags.Lookup("deployment-dump").NoOptDefVal = "."
	flags.StringVar(&deployDumpAlias, "deploy-dump", "", "alias for --deployment-dump")
	flags.Lookup("deploy-dump").NoOptDefVal = "."
	flags.BoolVar(&showPod, "show-pod", false, "always display pod identifiers in log rows")
	flags.BoolVar(&noShowPod, "no-show-pod", false, "hide pod identifiers in log rows")
	flags.BoolVar(&options.SplitPanes, "split-panes", false, "open one Windows Terminal pane per pod")
	flags.BoolVar(&options.TileWindows, "tile-windows", false, "open and automatically tile one Windows Terminal window per pod")
	flags.BoolVar(&options.LiveFilter, "live-filter", true, "show the full-screen live filter UI")
	flags.BoolVar(&noLiveFilter, "no-live-filter", false, "stream logs directly without the full-screen UI")
	flags.StringArrayVar(&options.TracePods, "trace-pod", nil, "internal request trace pod scope")
	flags.StringArrayVar(&options.TraceSelectors, "trace-selector", nil, "internal request trace workload scope")
	_ = flags.MarkHidden("trace-pod")
	_ = flags.MarkHidden("trace-selector")
	flags.StringVar(&options.FilterFile, "filter-file", "", "internal shared filter file")
	_ = flags.MarkHidden("filter-file")
	flags.BoolVar(&options.NoColor, "no-color", false, "disable ANSI colors")
	command.AddCommand(newAgentCommand(ctx, stdin, stdout, stderr, agent.ModeIssues))
	command.AddCommand(newAgentCommand(ctx, stdin, stdout, stderr, agent.ModeDiagnose))
	command.AddCommand(newIssueCommand(ctx, stdin, stdout, stderr))
	command.AddCommand(newMCPCommand(ctx, stdin, stdout, stderr))
	command.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the tailg version and build information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), BuildDescription())
		},
	})
	command.SetIn(stdin)
	command.SetOut(stdout)
	command.SetErr(stderr)
	return command
}

func statusLookbackFromArgs(args []string, defaultLookback time.Duration) (time.Duration, []string, error) {
	if defaultLookback <= 0 {
		defaultLookback = core.DefaultStatusLookback
	}
	if len(args) == 0 {
		return defaultLookback, args, nil
	}
	if len(args) != 1 {
		return 0, nil, fmt.Errorf("--status accepts at most one lookback value in minutes")
	}
	minutes, err := strconv.ParseInt(strings.TrimSpace(args[0]), 10, 64)
	maxDurationMinutes := int64(^uint64(0)>>1) / int64(time.Minute)
	if err != nil || minutes <= 0 || minutes > maxDurationMinutes {
		return 0, nil, fmt.Errorf("invalid --status lookback %q: use a positive whole number of minutes", args[0])
	}
	return time.Duration(minutes) * time.Minute, nil, nil
}

type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func IsExitError(err error) bool {
	_, ok := err.(exitError)
	return ok
}
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if code, ok := err.(exitError); ok {
		return int(code)
	}
	return 1
}
