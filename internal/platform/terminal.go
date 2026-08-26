package platform

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func LaunchTabs(pods []string, childArgs func(string) []string) error {
	if err := requireWindowsTerminal(); err != nil {
		return err
	}
	pods = unique(pods)
	if len(pods) == 0 {
		return nil
	}
	args := []string{"new-tab", "--title", "tailg " + pods[0], "--"}
	args = append(args, childArgs(pods[0])...)
	for _, pod := range pods[1:] {
		args = append(args, ";", "new-tab", "--title", "tailg "+pod, "--")
		args = append(args, childArgs(pod)...)
	}
	return exec.Command("wt", args...).Start()
}

func LaunchSplitPanes(pods []string, childArgs func(string) []string) error {
	if err := requireWindowsTerminal(); err != nil {
		return err
	}
	args := splitPaneArgs(pods, childArgs)
	if len(args) == 0 {
		return nil
	}
	return exec.Command("wt", args...).Start()
}

func splitPaneArgs(pods []string, childArgs func(string) []string) []string {
	pods = unique(pods)
	if len(pods) == 0 {
		return nil
	}
	args := []string{"new-tab", "--title", "tailg " + pods[0], "--"}
	args = append(args, childArgs(pods[0])...)
	vertical := true
	for index, pod := range pods[1:] {
		direction := "-V"
		if !vertical {
			direction = "-H"
		}
		vertical = !vertical

		// Windows Terminal focuses the pane it just created, so every subsequent
		// split operates on the remaining region. Reserve one equal share for the
		// current pane and give the rest to the new pane. Without an explicit size,
		// panes shrink as 1/2, 1/4, 1/8 and quickly become unusable.
		remaining := len(pods) - index
		size := float64(remaining-1) / float64(remaining)
		args = append(args, ";", "split-pane", direction, "--size", formatPaneSize(size), "--title", "tailg "+pod, "--")
		args = append(args, childArgs(pod)...)
	}
	return args
}

func formatPaneSize(size float64) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(size, 'f', 6, 64), "0"), ".")
}

func LaunchTiledWindows(pods []string, childArgs func(string) []string) (int, int, error) {
	if err := requireWindowsTerminal(); err != nil {
		return 0, 0, err
	}
	pods = unique(pods)
	if len(pods) == 0 {
		return 0, 0, nil
	}
	before := visibleWindows()
	for _, pod := range pods {
		args := []string{"-w", "new", "new-tab", "--title", "tailg " + pod, "--"}
		args = append(args, childArgs(pod)...)
		if err := exec.Command("wt", args...).Start(); err != nil {
			return 0, 0, err
		}
	}
	titles := make([]string, len(pods))
	for i, pod := range pods {
		titles[i] = "tailg " + pod
	}
	handles := waitForWindows(before, titles, 8*time.Second)
	tiled := tileHandles(handles)
	return len(pods), tiled, nil
}

func requireWindowsTerminal() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("Windows Terminal layouts require Windows")
	}
	if _, err := exec.LookPath("wt"); err != nil {
		return fmt.Errorf("Windows Terminal (wt.exe) was not found in PATH")
	}
	return nil
}
func unique(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
func ShellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t\"") {
			quoted[i] = "\"" + strings.ReplaceAll(arg, "\"", "\\\"") + "\""
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}
