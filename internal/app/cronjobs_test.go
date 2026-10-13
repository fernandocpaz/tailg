package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

func TestPickerCronJobCreatePinsDisplayedScopeAndReportsFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX kubectl fixture")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	t.Setenv("TAILG_PICKER_CRON_ARGS", argsFile)
	binary := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$TAILG_PICKER_CRON_ARGS\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runner := kube.Runner{Binary: binary, Context: "wrong-context", Namespace: "wrong-namespace"}
	app := core.AppChoice{Name: "cronjob/weekly", CronJob: &core.CronJobInfo{Name: "weekly"}}
	notice := runPickerCronJob(context.Background(), runner, "tkgs-qa", "cronjob", app)
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(args), "--context\ntkgs-qa\n-n\ncronjob\ncreate\njob\n") || !strings.Contains(notice, "Created job/") {
		t.Fatalf("args=%s notice=%s", args, notice)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$TAILG_PICKER_CRON_ARGS\"\necho 'Forbidden' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	notice = runPickerCronJob(context.Background(), runner, "tkgs-qa", "cronjob", app)
	args, err = os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice, "not confirmed") || !strings.Contains(notice, "Check for job/") || strings.Count(string(args), "create\njob") != 2 {
		t.Fatalf("create retried or failure lost: %s args=%s", notice, args)
	}
}
