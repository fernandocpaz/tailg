package app

import (
	"strings"
	"testing"
	"time"
)

func TestChildArgsPreserveDetailHeartbeatAndFilter(t *testing.T) {
	show := true
	options := Options{Detail: true, Tail: 42, HeartbeatWindow: 30 * time.Minute, LiveFilter: true, FilterFile: "C:/Temp/filter.txt", ShowPod: &show, Include: []string{"needle"}}
	args := childArgs(options, "default")("pod-1")
	joined := strings.Join(args, " ")
	for _, expected := range []string{"pod/pod-1 default", "--detail", "--tail 42", "--heartbeat-window 30m0s", "--filter-file C:/Temp/filter.txt", "--show-pod", "--include needle"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %q in %s", expected, joined)
		}
	}
}
