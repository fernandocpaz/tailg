package app

import (
	"strings"
	"testing"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

func TestChildArgsPreserveDetailHeartbeatAndFilter(t *testing.T) {
	show := true
	options := Options{Detail: true, Tail: 42, BufferLines: 12_000, HeartbeatWindow: 30 * time.Minute, LiveFilter: true, FilterFile: "C:/Temp/filter.txt", ShowPod: &show, Include: []string{"needle"}, HideProbes: true}
	args := childArgs(options, "default")("pod-1")
	joined := strings.Join(args, " ")
	for _, expected := range []string{"pod/pod-1 default", "--detail", "--tail 42", "--buffer-lines 12000", "--heartbeat-window 30m0s", "--filter-file C:/Temp/filter.txt", "--show-pod", "--include needle", "--hide-probes"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %q in %s", expected, joined)
		}
	}
}

func TestProbeLogsAreShownByDefaultAndCanBeHiddenExplicitly(t *testing.T) {
	message := `[11:24:33 INF] HTTP GET /ready responded 200 in 0.3566 ms {"SourceContext":"Microsoft.Extensions.Diagnostics.HealthChecks.DefaultHealthCheckService"}`

	patterns, err := core.CompilePatterns(logExcludePatterns(Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if lines := (core.Formatter{Exclude: patterns}).Format("api-pod", "api", message, false); len(lines) != 1 {
		t.Fatalf("probe log was hidden by default: %q", message)
	}

	patterns, err = core.CompilePatterns(logExcludePatterns(Options{HideProbes: true}))
	if err != nil {
		t.Fatal(err)
	}
	if lines := (core.Formatter{Exclude: patterns}).Format("api-pod", "api", message, false); len(lines) != 0 {
		t.Fatalf("probe log was not hidden with --hide-probes: %v", lines)
	}
}
