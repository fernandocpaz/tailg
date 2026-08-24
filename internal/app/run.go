package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/fernandocpaz/tailg/internal/bundle"
	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
	"github.com/fernandocpaz/tailg/internal/platform"
	"github.com/fernandocpaz/tailg/internal/tui"
)

func Run(ctx context.Context, options Options, stdin io.Reader, stdout, stderr io.Writer) int {
	if options.Namespace != "" && options.LegacyNamespace != "" {
		fmt.Fprintln(stderr, "specify the namespace either positionally or with --namespace, not both")
		return 2
	}
	if options.Namespace == "" {
		options.Namespace = options.LegacyNamespace
	}
	if options.Since != "" {
		options.Since = core.NormalizeSince(options.Since)
		if !options.TailSet {
			options.Tail = -1
		}
	}
	if options.Container == "" {
		options.Container = ".*"
	}
	containerPattern, err := regexp.Compile(options.Container)
	if err != nil {
		fmt.Fprintln(stderr, "Invalid regex:", err)
		return 1
	}
	excludes := append([]string(nil), options.Exclude...)
	if !options.NoDefaultExclude {
		excludes = append(core.DefaultExcludePatterns, excludes...)
	}
	includePatterns, err := core.CompilePatterns(options.Include)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	excludePatterns, err := core.CompilePatterns(excludes)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	formatter := core.Formatter{Include: includePatterns, Exclude: excludePatterns, Color: !options.NoColor, Detail: options.Detail}
	if _, err := exec.LookPath("kubectl"); err != nil {
		fmt.Fprintln(stderr, "kubectl was not found in PATH.")
		return 1
	}
	runner := kube.NewRunner(options.Namespace, options.Context)
	if options.Status {
		if options.Target != "" {
			fmt.Fprintln(stderr, "--status scans a namespace and cannot be combined with a target")
			return 2
		}
		namespace := options.Namespace
		if namespace == "" {
			_, namespace, err = runner.CurrentContext(ctx)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
		statusOptions := kube.StatusOptions{Interval: options.StatusInterval, Timeout: options.StatusTimeout, Output: stdout}
		if interactive(stdin) {
			statusOptions.Input = stdin
			if runtime.GOOS == "windows" {
				statusOptions.OpenConsoles = func(pods []string) error {
					if err := prepareSharedFilter(&options); err != nil {
						return err
					}
					_, _, err := platform.LaunchTiledWindows(pods, childArgs(options, namespace))
					return err
				}
			}
			if _, gitErr := exec.LookPath("git"); gitErr == nil {
				statusOptions.ExamineRepos = func(workloads []string) error { return examineRepositories(workloads, namespace, stdin, stdout) }
			}
		}
		return runner.RunStatus(ctx, namespace, statusOptions)
	}

	namespaceMode := options.Target == "" && options.Namespace != ""
	if options.Target == "" && !namespaceMode {
		fmt.Fprintln(stderr, "a target is required; use '*' for the app picker or --namespace to open every pod")
		return 2
	}
	effectiveNamespace := options.Namespace
	resolvedTarget := "pod/*"
	var selectedPods, selectedSelectors []string
	if !namespaceMode {
		if options.Target == "*" {
			apps, appsErr := runner.Apps(ctx)
			if appsErr != nil {
				fmt.Fprintln(stderr, appsErr)
				return 1
			}
			selected, selectErr := tui.PickApp(apps)
			if selectErr != nil {
				fmt.Fprintln(stderr, selectErr)
				return 1
			}
			effectiveNamespace = selected.Namespace
			if selected.Selector != "" {
				selectedSelectors = []string{selected.Selector}
			} else {
				selectedPods = selected.Pods
			}
			if len(selectedPods) > 0 {
				resolvedTarget = "pod/" + selectedPods[0]
			}
		} else if strings.ContainsAny(options.Target, "*?[") || strings.Contains(options.Target, ",") {
			selectedPods, selectedSelectors, effectiveNamespace, err = runner.MatchApps(ctx, options.Target)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			if len(selectedPods) > 0 {
				resolvedTarget = "pod/" + selectedPods[0]
			}
		} else {
			resolvedTarget, err = runner.ResolveTarget(ctx, options.Target)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
	}
	runner.Namespace = effectiveNamespace
	selector := options.Selector
	if selector == "" && len(selectedPods) == 0 && len(selectedSelectors) == 0 && resolvedTarget != "pod/*" {
		selector, err = runner.SelectorForResource(ctx, resolvedTarget)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	inventoryProvider := func(callCtx context.Context) ([]core.InventoryItem, error) {
		var items []core.InventoryItem
		var inventoryErr error
		switch {
		case len(selectedPods) > 0 || len(selectedSelectors) > 0:
			items, inventoryErr = selectedInventory(callCtx, runner, selectedPods, selectedSelectors)
		default:
			items, inventoryErr = runner.Inventory(callCtx, resolvedTarget, selector)
		}
		if inventoryErr != nil {
			return nil, inventoryErr
		}
		return filterInventory(items, containerPattern), nil
	}
	items, err := inventoryProvider(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	wantsBundle := options.DumpRequested || options.DeploymentDump
	if len(items) == 0 && !wantsBundle {
		fmt.Fprintln(stderr, "No matching pods/containers found.")
		return 1
	}
	if wantsBundle {
		directory := options.DumpDirectory
		deployment := options.DeploymentDump
		if deployment {
			directory = options.DeploymentDumpPath
		}
		output, generateErr := bundle.Generate(ctx, runner, bundle.Options{Directory: directory, Deployment: deployment, Target: resolvedTarget, Selector: selector, Items: items, Since: options.Since, Tail: options.Tail})
		if generateErr != nil {
			fmt.Fprintln(stderr, generateErr)
			return 1
		}
		fmt.Fprintln(stdout, "Troubleshooting bundle written to", output)
		return 0
	}
	if namespaceMode {
		pods := core.UniquePods(items)
		if err := prepareSharedFilter(&options); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if options.TileWindows {
			opened, tiled, launchErr := platform.LaunchTiledWindows(pods, childArgs(options, effectiveNamespace))
			if launchErr != nil {
				fmt.Fprintln(stderr, launchErr)
				return 1
			}
			fmt.Fprintf(stdout, "Opened %d Windows Terminal windows; tiled %d.\n", opened, tiled)
		} else {
			if launchErr := platform.LaunchTabs(pods, childArgs(options, effectiveNamespace)); launchErr != nil {
				fmt.Fprintln(stderr, launchErr)
				return 1
			}
			fmt.Fprintf(stdout, "Opened %d Windows Terminal tabs for namespace %q.\n", len(pods), effectiveNamespace)
		}
		return 0
	}
	showPod := len(core.UniquePods(items)) > 1
	if options.ShowPod != nil {
		showPod = *options.ShowPod
	}
	formatter.ShowPod = showPod
	if options.TileWindows {
		if err := prepareSharedFilter(&options); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		opened, tiled, launchErr := platform.LaunchTiledWindows(core.UniquePods(items), childArgs(options, effectiveNamespace))
		if launchErr != nil {
			fmt.Fprintln(stderr, launchErr)
			return 1
		}
		fmt.Fprintf(stdout, "Opened %d Windows Terminal windows; tiled %d.\n", opened, tiled)
		return 0
	}
	if options.SplitPanes && len(core.UniquePods(items)) > 1 {
		if err := prepareSharedFilter(&options); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if launchErr := platform.LaunchSplitPanes(core.UniquePods(items), childArgs(options, effectiveNamespace)); launchErr != nil {
			fmt.Fprintln(stderr, launchErr)
			return 1
		}
		fmt.Fprintf(stdout, "Opened Windows Terminal panes for %d pods.\n", len(core.UniquePods(items)))
		return 0
	}
	if options.NoFollow {
		return snapshot(ctx, runner, items, options, formatter, stdout, stderr)
	}
	if !options.LiveFilter {
		return followPlain(ctx, runner, items, inventoryProvider, options, formatter, stdout, stderr)
	}
	title := logTitle(items, effectiveNamespace, options.Context, resolvedTarget)
	err = tui.Run(ctx, tui.Config{Title: title, Items: items, Formatter: formatter, HeartbeatWindow: options.HeartbeatWindow, RefreshInterval: options.RefreshInterval, FilterFile: options.FilterFile,
		Stream: func(streamCtx context.Context, item core.InventoryItem, events chan<- core.LogEvent) error {
			return runner.Stream(streamCtx, item, kube.LogOptions{Since: options.Since, Tail: options.Tail, Follow: true}, events)
		}, Inventory: inventoryProvider,
		Search: func(searchCtx context.Context, query string) ([]string, error) {
			current, inventoryErr := inventoryProvider(searchCtx)
			if inventoryErr != nil {
				return nil, inventoryErr
			}
			return runner.CompleteHistory(searchCtx, current, options.Since, formatter, query, options.Tail)
		}, MappedResources: runner.MappedResources, ResourceDetail: runner.ResourceDetail})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func selectedInventory(ctx context.Context, runner kube.Runner, pods, selectors []string) ([]core.InventoryItem, error) {
	podItems, err := runner.InventoryForPods(ctx, pods)
	if err != nil {
		return nil, err
	}
	selectorItems, err := runner.InventoryForSelectors(ctx, selectors)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var result []core.InventoryItem
	for _, item := range append(podItems, selectorItems...) {
		if !seen[item.Key()] {
			seen[item.Key()] = true
			result = append(result, item)
		}
	}
	return result, nil
}
func filterInventory(items []core.InventoryItem, pattern *regexp.Regexp) []core.InventoryItem {
	var result []core.InventoryItem
	for _, item := range items {
		if pattern.MatchString(item.Container) {
			result = append(result, item)
		}
	}
	return result
}
func snapshot(ctx context.Context, runner kube.Runner, items []core.InventoryItem, options Options, formatter core.Formatter, stdout, stderr io.Writer) int {
	exit := 0
	for _, item := range items {
		events, err := runner.Snapshot(ctx, item, kube.LogOptions{Since: options.Since, Tail: options.Tail})
		if err != nil {
			fmt.Fprintln(stderr, err)
			exit = 1
			continue
		}
		for _, event := range events {
			for _, line := range formatter.Format(event.Pod, event.Container, event.Message, false) {
				fmt.Fprintln(stdout, line)
			}
		}
	}
	return exit
}
func followPlain(ctx context.Context, runner kube.Runner, items []core.InventoryItem, inventory func(context.Context) ([]core.InventoryItem, error), options Options, formatter core.Formatter, stdout, stderr io.Writer) int {
	events := make(chan core.LogEvent, 1024)
	active := map[string]context.CancelFunc{}
	reconcile := func(current []core.InventoryItem) {
		wanted := map[string]bool{}
		for _, item := range current {
			wanted[item.Key()] = true
			if _, ok := active[item.Key()]; !ok {
				streamCtx, cancel := context.WithCancel(ctx)
				active[item.Key()] = cancel
				go runner.Stream(streamCtx, item, kube.LogOptions{Since: options.Since, Tail: options.Tail, Follow: true}, events)
			}
		}
		for key, cancel := range active {
			if !wanted[key] {
				cancel()
				delete(active, key)
			}
		}
	}
	reconcile(items)
	ticker := time.NewTicker(options.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for _, cancel := range active {
				cancel()
			}
			return 0
		case event := <-events:
			if event.Err != nil && event.Closed {
				fmt.Fprintln(stderr, event.Err)
			}
			for _, line := range formatter.Format(event.Pod, event.Container, event.Message, false) {
				fmt.Fprintln(stdout, line)
			}
		case <-ticker.C:
			if current, err := inventory(ctx); err == nil {
				reconcile(current)
			}
		}
	}
}
func logTitle(items []core.InventoryItem, namespace, kubeContext, target string) string {
	services := core.ServiceNames(items)
	serviceKey := "service"
	if len(services) != 1 {
		serviceKey = "services"
	}
	podLabel := "0 pods"
	pods := core.UniquePods(items)
	if len(pods) == 1 {
		podLabel = pods[0]
	} else if len(pods) > 1 {
		podLabel = fmt.Sprintf("%d pods", len(pods))
	}
	contextLabel := ""
	if kubeContext != "" {
		contextLabel = " | context=" + kubeContext
	}
	return fmt.Sprintf("tailg | %s=%s | target=%s | pod=%s | namespace=%s%s", serviceKey, valueOr(strings.Join(services, ","), "unknown"), target, podLabel, valueOr(namespace, "default"), contextLabel)
}
func prepareSharedFilter(options *Options) error {
	if !options.LiveFilter || options.FilterFile != "" {
		return nil
	}
	file := filepath.Join(os.TempDir(), fmt.Sprintf("tailg-filter-%d-%d.txt", os.Getpid(), time.Now().Unix()))
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(file+".mode", []byte("context\n"), 0o600); err != nil {
		return err
	}
	options.FilterFile = file
	return nil
}
func childArgs(options Options, namespace string) func(string) []string {
	return func(pod string) []string {
		executable, _ := os.Executable()
		args := []string{executable, "pod/" + pod}
		if namespace != "" {
			args = append(args, namespace)
		}
		if options.Context != "" {
			args = append(args, "--context", options.Context)
		}
		if options.Container != ".*" && options.Container != "" {
			args = append(args, "--container", options.Container)
		}
		if options.Detail {
			args = append(args, "--detail")
		}
		if options.Tail != core.DefaultTailLines {
			args = append(args, "--tail", fmt.Sprint(options.Tail))
		}
		if options.Since != "" {
			args = append(args, "--since", options.Since)
		}
		if options.HeartbeatWindow != core.DefaultHeartbeatWindow {
			args = append(args, "--heartbeat-window", options.HeartbeatWindow.String())
		}
		if options.RefreshInterval > 0 && options.RefreshInterval != 2*time.Second {
			args = append(args, "--refresh-seconds", fmt.Sprint(int(options.RefreshInterval/time.Second)))
		}
		for _, value := range options.Include {
			args = append(args, "--include", value)
		}
		if options.NoDefaultExclude {
			args = append(args, "--no-default-exclude")
		}
		for _, value := range options.Exclude {
			args = append(args, "--exclude", value)
		}
		if options.ShowPod != nil {
			if *options.ShowPod {
				args = append(args, "--show-pod")
			} else {
				args = append(args, "--no-show-pod")
			}
		}
		if !options.LiveFilter {
			args = append(args, "--no-live-filter")
		}
		if options.FilterFile != "" {
			args = append(args, "--filter-file", options.FilterFile)
		}
		if options.NoColor {
			args = append(args, "--no-color")
		}
		return args
	}
}
func interactive(input io.Reader) bool {
	file, ok := input.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
