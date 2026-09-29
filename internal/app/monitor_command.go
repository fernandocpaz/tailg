package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/fernandocpaz/tailg/internal/agent"
)

type monitorPoll struct {
	Kind       string               `json:"kind"`
	ObservedAt string               `json:"observedAt"`
	Cursor     string               `json:"cursor"`
	Status     string               `json:"status"`
	Coverage   agent.Coverage       `json:"coverage"`
	Events     []agent.MonitorEvent `json:"events"`
}

func collectMonitorSnapshot(ctx context.Context, options agentOptions) agent.Report {
	options.WorkloadScope = true
	report, collectErr := collectAgentReport(ctx, options, agent.ModeDiagnose)
	if collectErr == nil {
		return report
	}
	if report.SchemaVersion == "" {
		report = agent.Report{SchemaVersion: agent.SchemaVersion, Kind: "DiagnosticReport", GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Scope:   agent.Scope{Context: options.Context, Namespace: options.Namespace, Target: fallback(options.Target, "pod/*")},
			Summary: agent.Summary{Status: "unknown"}, Coverage: agent.Coverage{Status: "unavailable", Reasons: []string{"collection_failed"}},
			Pods: []agent.Pod{}, Issues: []agent.Issue{}, CollectionErrors: []agent.CollectionError{}}
	}
	report.CollectionErrors = append(report.CollectionErrors, agent.CollectionError{Source: "monitor", Message: agent.Redact(collectErr.Error())})
	report.Coverage.Status = "unavailable"
	report.Coverage.Reasons = append(report.Coverage.Reasons, "collection_failed")
	report.Summary.Status = "unknown"
	return report
}

func applyMonitorPoll(ctx context.Context, options agentOptions, store *agent.MonitorStore, stdout io.Writer) (monitorPoll, error) {
	report := collectMonitorSnapshot(ctx, options)
	events, err := store.Apply(report, time.Now().UTC())
	if err != nil {
		return monitorPoll{}, err
	}
	state := store.ListIncidents()
	result := monitorPoll{Kind: "MonitorPoll", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Cursor: state.Cursor,
		Status: state.Status, Coverage: state.Coverage, Events: events}
	if stdout != nil {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		err = encoder.Encode(result)
	}
	return result, err
}

func newMonitorCommand(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	options := defaultAgentOptions()
	options.Since = "5m"
	options.Tail = 50000
	options.MaxLines = 50000
	options.MaxIssues = 1000
	options.ContextLines = 0
	options.MaxBytes = 16 * 1024 * 1024
	var statePath string
	var interval, resolveAfter time.Duration
	var once bool
	command := &cobra.Command{
		Use: "monitor", Short: "Continuously monitor a pinned Kubernetes scope and emit persistent incident changes",
		Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		Long: "Poll a Kubernetes scope, persist incident state and a replayable change cursor, and emit one JSON record per poll. Incomplete scans are reported as partial coverage and never resolve incidents.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.Context == "" || options.Namespace == "" || statePath == "" {
				return fmt.Errorf("monitor requires --context, --namespace, and --state")
			}
			if interval <= 0 || resolveAfter <= 0 {
				return fmt.Errorf("--interval and --resolve-after must be greater than zero")
			}
			if _, err := time.ParseDuration(options.Since); err != nil {
				return fmt.Errorf("invalid --since duration: %w", err)
			}
			spec := agent.MonitorSpec{Context: options.Context, Namespace: options.Namespace, Target: fallback(options.Target, "pod/*"), Selector: options.Selector,
				Container: options.Container, Since: options.Since, Tail: options.Tail, MaxLines: options.MaxLines, MaxIssues: options.MaxIssues, MaxBytes: options.MaxBytes,
				Include: options.Include, Exclude: options.Exclude,
				DefaultExclude: !options.NoDefaultExclude, ResolveAfterSeconds: int64(resolveAfter / time.Second)}
			store, err := agent.OpenMonitorStore(statePath, spec)
			if err != nil {
				return err
			}
			defer store.Close()
			poll := func() error { _, err := applyMonitorPoll(ctx, options, store, stdout); return err }
			if err := poll(); err != nil {
				return err
			}
			if once {
				return nil
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					if err := poll(); err != nil {
						return err
					}
				}
			}
		},
	}
	addAgentFlags(command, &options)
	command.Flags().StringVar(&statePath, "state", "", "private persistent monitor state file (required)")
	command.Flags().DurationVar(&interval, "interval", 30*time.Second, "time between monitor polls")
	command.Flags().DurationVar(&resolveAfter, "resolve-after", 5*time.Minute, "complete quiet period required before resolving an incident")
	command.Flags().BoolVar(&once, "once", false, "perform one poll and exit")
	command.SetIn(stdin)
	command.SetOut(stdout)
	command.SetErr(stderr)
	return command
}
