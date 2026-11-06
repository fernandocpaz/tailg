package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestChildArgsPreserveDetailHeartbeatAndFilter(t *testing.T) {
	show := true
	options := Options{Detail: true, Tail: 42, BufferLines: 12_000, HeartbeatWindow: 30 * time.Minute, LiveFilter: true, FilterFile: "C:/Temp/filter.txt", ShowPod: &show, Include: []string{"needle"}}
	args := childArgs(options, "default")("pod-1")
	joined := strings.Join(args, " ")
	for _, expected := range []string{"pod/pod-1 default", "--detail", "--tail 42", "--buffer-lines 12000", "--heartbeat-window 30m0s", "--filter-file C:/Temp/filter.txt", "--show-pod", "--include needle"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %q in %s", expected, joined)
		}
	}
}

func TestUsesAppPickerByDefault(t *testing.T) {
	if !usesAppPicker(Options{}) {
		t.Fatal("bare tailg should use the application picker")
	}
	if usesAppPicker(Options{Target: "api"}) {
		t.Fatal("explicit target should not use the application picker")
	}
	if !usesAppPicker(Options{Namespace: "apollo", LiveFilter: true}) {
		t.Fatal("a namespace-only invocation should open the application picker")
	}
	if usesAppPicker(Options{Target: "api", Namespace: "apollo", LiveFilter: true}) {
		t.Fatal("a namespace and explicit target should open the target")
	}
	for _, options := range []Options{
		{Namespace: "apollo"},
		{Namespace: "apollo", LiveFilter: true, NoFollow: true},
		{Namespace: "apollo", LiveFilter: true, SplitPanes: true},
		{Namespace: "apollo", LiveFilter: true, TileWindows: true},
		{Namespace: "apollo", LiveFilter: true, DumpRequested: true},
		{Namespace: "apollo", LiveFilter: true, DeploymentDump: true},
	} {
		if usesAppPicker(options) {
			t.Fatalf("namespace with an explicit mode opened the picker: %+v", options)
		}
	}
}

func TestStarPickerTargetWasRemoved(t *testing.T) {
	var output bytes.Buffer
	code := Run(context.Background(), Options{
		Target: "*", BufferLines: 100, Container: ".*", LiveFilter: true,
	}, strings.NewReader(""), &output, &output)
	if code != 2 {
		t.Fatalf("Run code = %d, want 2; output=%q", code, output.String())
	}
	if !strings.Contains(output.String(), "run tailg with no target") {
		t.Fatalf("unexpected migration message: %q", output.String())
	}
}
