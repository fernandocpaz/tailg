package app

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fernandocpaz/tailg/internal/agent"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

const (
	defaultAgentMaxLines     = 5000
	defaultAgentMaxIssues    = 100
	defaultAgentContextLines = 5
	defaultAgentMaxBytes     = 2 * 1024 * 1024
)

type agentOptions struct {
	Target           string
	LegacyNamespace  string
	Namespace        string
	Context          string
	Selector         string
	Container        string
	Since            string
	Tail             int
	Include          []string
	Exclude          []string
	NoDefaultExclude bool
	Output           string
	Timeout          time.Duration
	MaxLines         int
	MaxIssues        int
	ContextLines     int
	MaxBytes         int
	IssueID          string
}

func defaultAgentOptions() agentOptions {
	return agentOptions{Container: ".*", Tail: core.DefaultTailLines, Output: "json", Timeout: 30 * time.Second, MaxLines: defaultAgentMaxLines, MaxIssues: defaultAgentMaxIssues, ContextLines: defaultAgentContextLines, MaxBytes: defaultAgentMaxBytes}
}

func newAgentCommand(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, mode agent.Mode) *cobra.Command {
	options := defaultAgentOptions()
	command := &cobra.Command{
		Use: string(mode) + " [target] [namespace]", Args: cobra.MaximumNArgs(2), SilenceUsage: true, SilenceErrors: true,
		Short: map[agent.Mode]string{agent.ModeIssues: "Emit grouped log issues for scripts and AI agents", agent.ModeDiagnose: "Emit a bounded Kubernetes diagnostic report"}[mode],
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				options.Target = args[0]
			}
			if len(args) > 1 {
				options.LegacyNamespace = args[1]
			}
			return executeAgent(ctx, options, mode, stdout)
		},
	}
	addAgentFlags(command, &options)
	command.SetIn(stdin)
	command.SetOut(stdout)
	command.SetErr(stderr)
	return command
}

func newIssueCommand(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	options := defaultAgentOptions()
	command := &cobra.Command{
		Use: "issue ISSUE_ID [target] [namespace]", Short: "Emit bounded context for one issue ID", Args: cobra.RangeArgs(1, 3), SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			options.IssueID = args[0]
			if len(args) > 1 {
				options.Target = args[1]
			}
			if len(args) > 2 {
				options.LegacyNamespace = args[2]
			}
			return executeAgent(ctx, options, agent.ModeIssues, stdout)
		},
	}
	addAgentFlags(command, &options)
	command.SetIn(stdin)
	command.SetOut(stdout)
	command.SetErr(stderr)
	return command
}

func newMCPCommand(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	defaults := defaultAgentOptions()
	command := &cobra.Command{
		Use: "mcp", Short: "Run the read-only tailg MCP server over stdio", Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := func(callCtx context.Context, name string, arguments agent.ToolArguments) (agent.Report, error) {
				options := defaults
				applyToolArguments(&options, arguments)
				switch name {
				case "tailg_list_issues":
					return collectAgentReport(callCtx, options, agent.ModeIssues)
				case "tailg_diagnose":
					return collectAgentReport(callCtx, options, agent.ModeDiagnose)
				case "tailg_get_issue_context":
					if options.IssueID == "" {
						return agent.Report{}, fmt.Errorf("issueId is required")
					}
					report, err := collectAgentReport(callCtx, options, agent.ModeIssues)
					if err == nil && len(report.Issues) == 0 {
						err = fmt.Errorf("issue ID was not found in the bounded collection window")
					}
					return report, err
				default:
					return agent.Report{}, agent.UnknownTool(name)
				}
			}
			return agent.ServeMCP(ctx, stdin, stdout, handler)
		},
	}
	command.Flags().StringVarP(&defaults.Namespace, "namespace", "n", "", "default Kubernetes namespace for tool calls")
	command.Flags().StringVar(&defaults.Context, "context", "", "default kubectl context for tool calls")
	command.Flags().DurationVar(&defaults.Timeout, "timeout", defaults.Timeout, "maximum time for each tool call")
	command.SetIn(stdin)
	command.SetOut(stdout)
	command.SetErr(stderr)
	return command
}

func addAgentFlags(command *cobra.Command, options *agentOptions) {
	flags := command.Flags()
	flags.StringVarP(&options.Namespace, "namespace", "n", "", "Kubernetes namespace")
	flags.StringVar(&options.Context, "context", "", "kubectl context name")
	flags.StringVar(&options.Selector, "selector", "", "label selector override")
	flags.StringVar(&options.Container, "container", ".*", "regular expression selecting container names")
	flags.StringVar(&options.Since, "since", "", "relative time window, for example 30m or 2h")
	flags.IntVar(&options.Tail, "tail", core.DefaultTailLines, "maximum recent lines requested per container")
	flags.StringArrayVar(&options.Include, "include", nil, "regular expression; only matching log lines are analyzed (repeatable)")
	flags.StringArrayVar(&options.Exclude, "exclude", nil, "regular expression; matching log lines are ignored (repeatable)")
	flags.BoolVar(&options.NoDefaultExclude, "no-default-exclude", false, "analyze health, ready, and live probe traffic")
	flags.StringVarP(&options.Output, "output", "o", "json", "structured output format: json or ndjson")
	flags.DurationVar(&options.Timeout, "timeout", 30*time.Second, "maximum collection time")
	flags.IntVar(&options.MaxLines, "max-lines", defaultAgentMaxLines, "maximum log lines retained across all containers")
	flags.IntVar(&options.MaxIssues, "max-issues", defaultAgentMaxIssues, "maximum grouped issues")
	flags.IntVar(&options.ContextLines, "context-lines", defaultAgentContextLines, "lines before and after each issue match")
	flags.IntVar(&options.MaxBytes, "max-bytes", defaultAgentMaxBytes, "maximum encoded report size")
}

func executeAgent(ctx context.Context, options agentOptions, mode agent.Mode, stdout io.Writer) error {
	report, err := collectAgentReport(ctx, options, mode)
	if err != nil {
		_ = agent.WriteError(stdout, "COLLECTION_FAILED", err.Error(), options.Output)
		return exitError(3)
	}
	if options.IssueID != "" && len(report.Issues) == 0 {
		_ = agent.WriteError(stdout, "ISSUE_NOT_FOUND", "issue ID was not found in the bounded collection window", options.Output)
		return exitError(3)
	}
	if err := agent.WriteReport(stdout, report, options.Output); err != nil {
		_ = agent.WriteError(stdout, "OUTPUT_FAILED", err.Error(), options.Output)
		return exitError(3)
	}
	if code := agent.ExitCode(report); code != 0 {
		return exitError(code)
	}
	return nil
}

func collectAgentReport(parent context.Context, options agentOptions, mode agent.Mode) (agent.Report, error) {
	if err := validateAgentOptions(options); err != nil {
		return agent.Report{}, err
	}
	ctx, cancel := context.WithTimeout(parent, options.Timeout)
	defer cancel()
	if _, err := exec.LookPath("kubectl"); err != nil {
		return agent.Report{}, fmt.Errorf("kubectl was not found in PATH")
	}
	if options.Namespace != "" && options.LegacyNamespace != "" {
		return agent.Report{}, fmt.Errorf("specify namespace positionally or with --namespace, not both")
	}
	if options.Namespace == "" {
		options.Namespace = options.LegacyNamespace
	}
	if options.Since != "" {
		options.Since = core.NormalizeSince(options.Since)
	}

	containerPattern, err := regexp.Compile(options.Container)
	if err != nil {
		return agent.Report{}, fmt.Errorf("invalid container regex: %w", err)
	}
	excludes := append([]string(nil), options.Exclude...)
	if !options.NoDefaultExclude {
		excludes = append(core.DefaultExcludePatterns, excludes...)
	}
	includePatterns, err := core.CompilePatterns(options.Include)
	if err != nil {
		return agent.Report{}, err
	}
	excludePatterns, err := core.CompilePatterns(excludes)
	if err != nil {
		return agent.Report{}, err
	}
	formatter := core.Formatter{Include: includePatterns, Exclude: excludePatterns, Detail: true}

	runner := kube.NewRunner(options.Namespace, options.Context)
	effectiveContext, effectiveNamespace, err := runner.CurrentContext(ctx)
	if err != nil {
		return agent.Report{}, err
	}
	if options.Namespace != "" {
		effectiveNamespace = options.Namespace
	}
	runner.Namespace = effectiveNamespace

	resolvedTarget := "pod/*"
	var selectedPods, selectedSelectors []string
	if options.Target != "" {
		if strings.ContainsAny(options.Target, "*?[") || strings.Contains(options.Target, ",") {
			selectedPods, selectedSelectors, effectiveNamespace, err = runner.MatchApps(ctx, options.Target)
			if err != nil {
				return agent.Report{}, err
			}
			runner.Namespace = effectiveNamespace
			if len(selectedPods) > 0 {
				resolvedTarget = "pod/" + selectedPods[0]
			} else {
				resolvedTarget = options.Target
			}
		} else {
			resolvedTarget, err = runner.ResolveTarget(ctx, options.Target)
			if err != nil {
				return agent.Report{}, err
			}
		}
	}
	selector := options.Selector
	if selector == "" && len(selectedPods) == 0 && len(selectedSelectors) == 0 && resolvedTarget != "pod/*" {
		selector, err = runner.SelectorForResource(ctx, resolvedTarget)
		if err != nil {
			return agent.Report{}, err
		}
	}
	var items []core.InventoryItem
	if len(selectedPods) > 0 || len(selectedSelectors) > 0 {
		items, err = selectedInventory(ctx, runner, selectedPods, selectedSelectors)
	} else {
		items, err = runner.Inventory(ctx, resolvedTarget, selector)
	}
	if err != nil {
		return agent.Report{}, err
	}
	items = filterInventory(items, containerPattern)
	if len(items) == 0 {
		return agent.Report{}, fmt.Errorf("no matching pods or containers found")
	}

	collector := agent.Collector{Client: runner}
	report, err := collector.Collect(ctx, items, agent.CollectOptions{
		Mode: mode, Namespace: effectiveNamespace, Context: fallback(effectiveContext, options.Context), Target: fallback(options.Target, "pod/*"), Since: options.Since, IssueID: options.IssueID,
		Limits:       agent.Limits{Tail: options.Tail, MaxLines: options.MaxLines, MaxIssues: options.MaxIssues, ContextLines: options.ContextLines, MaxBytes: options.MaxBytes},
		IncludeEvent: func(message string) bool { return len(formatter.Format("", "", message, false)) > 0 },
	})
	if err != nil {
		return report, err
	}
	return agent.LimitReport(report, "json")
}

func validateAgentOptions(options agentOptions) error {
	if options.Output != "json" && options.Output != "ndjson" {
		return fmt.Errorf("--output must be json or ndjson")
	}
	if options.Timeout <= 0 {
		return fmt.Errorf("--timeout must be greater than zero")
	}
	if options.Tail <= 0 || options.Tail > 50000 {
		return fmt.Errorf("--tail must be between 1 and 50000")
	}
	if options.MaxLines <= 0 || options.MaxLines > 50000 {
		return fmt.Errorf("--max-lines must be between 1 and 50000")
	}
	if options.MaxIssues <= 0 || options.MaxIssues > 1000 {
		return fmt.Errorf("--max-issues must be between 1 and 1000")
	}
	if options.ContextLines < 0 || options.ContextLines > 50 {
		return fmt.Errorf("--context-lines must be between 0 and 50")
	}
	if options.MaxBytes < 4096 || options.MaxBytes > 16*1024*1024 {
		return fmt.Errorf("--max-bytes must be between 4096 and 16777216")
	}
	return nil
}

func applyToolArguments(options *agentOptions, arguments agent.ToolArguments) {
	if arguments.Target != "" {
		options.Target = arguments.Target
	}
	if arguments.Namespace != "" {
		options.Namespace = arguments.Namespace
	}
	if arguments.Context != "" {
		options.Context = arguments.Context
	}
	if arguments.Selector != "" {
		options.Selector = arguments.Selector
	}
	if arguments.Container != "" {
		options.Container = arguments.Container
	}
	if arguments.Since != "" {
		options.Since = arguments.Since
	}
	if arguments.Tail != nil {
		options.Tail = *arguments.Tail
	}
	if arguments.MaxLines != nil {
		options.MaxLines = *arguments.MaxLines
	}
	if arguments.MaxIssues != nil {
		options.MaxIssues = *arguments.MaxIssues
	}
	if arguments.ContextLines != nil {
		options.ContextLines = *arguments.ContextLines
	}
	if arguments.MaxBytes != nil {
		options.MaxBytes = *arguments.MaxBytes
	}
	if arguments.IssueID != "" {
		options.IssueID = arguments.IssueID
	}
}

func fallback(value, fallbackValue string) string {
	if value == "" {
		return fallbackValue
	}
	return value
}
