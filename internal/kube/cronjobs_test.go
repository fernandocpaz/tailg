package kube

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func cronJobFixture(t *testing.T, pods, jobs, cronJobs map[string]any, denied string) Runner {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX kubectl fixture")
	}
	dir := t.TempDir()
	for name, object := range map[string]map[string]any{"pods": pods, "jobs": jobs, "cronjobs": cronJobs, "replicasets": {"items": []any{}}} {
		data, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TAILG_CRON_FIXTURE", dir)
	t.Setenv("TAILG_CRON_DENIED", denied)
	binary := filepath.Join(dir, "kubectl")
	script := `#!/bin/sh
while [ "$1" != "get" ] && [ "$#" -gt 0 ]; do shift; done
shift
if [ "$1" = "$TAILG_CRON_DENIED" ]; then echo "Forbidden: cannot list $1" >&2; exit 1; fi
cat "$TAILG_CRON_FIXTURE/$1.json"
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return Runner{Binary: binary, Context: "tkgs-qa", Namespace: "cronjob"}
}

func TestAppsKeepCronJobsWithNoPodsAndGroupVerifiedOwners(t *testing.T) {
	pods := map[string]any{"items": []any{
		map[string]any{"metadata": map[string]any{"name": "api-pod", "labels": map[string]any{"app": "shared"}}},
		map[string]any{"metadata": map[string]any{"name": "monthly-pod", "labels": map[string]any{"app": "shared"}, "ownerReferences": []any{map[string]any{"kind": "Job", "name": "monthly-run", "uid": "job-1", "controller": true}}}},
		map[string]any{"metadata": map[string]any{"name": "stale-pod", "labels": map[string]any{"app": "stale"}, "ownerReferences": []any{map[string]any{"kind": "Job", "name": "monthly-run", "uid": "old-job", "controller": true}}}},
	}}
	jobs := map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "monthly-run", "uid": "job-1", "ownerReferences": []any{map[string]any{"kind": "CronJob", "name": "monthly", "uid": "cron-1", "controller": true}}}}}}
	cronJobs := map[string]any{"items": []any{
		map[string]any{"metadata": map[string]any{"name": "monthly", "uid": "cron-1"}, "spec": map[string]any{"schedule": "0 4 1 * *", "timeZone": "America/New_York"}, "status": map[string]any{"lastScheduleTime": "2026-10-01T08:00:00Z", "active": []any{map[string]any{"name": "monthly-run"}}}},
		map[string]any{"metadata": map[string]any{"name": "never-ran"}, "spec": map[string]any{"schedule": "0 0 * * 0", "suspend": true}},
	}}
	runner := cronJobFixture(t, pods, jobs, cronJobs, "")
	apps, err := runner.Apps(context.Background())
	if err != nil || len(apps) != 4 {
		t.Fatalf("apps=%+v, err=%v", apps, err)
	}
	for _, app := range apps {
		switch app.Name {
		case "cronjob/monthly":
			if app.Selector != "" || !reflect.DeepEqual(app.Pods, []string{"monthly-pod"}) || len(app.PodChoices) != 1 {
				t.Fatalf("wrong CronJob scope: %+v", app)
			}
			job := app.CronJob
			if job == nil || job.Schedule != "0 4 1 * *" || job.TimeZone != "America/New_York" || job.Active != 1 || job.LastScheduledAt.IsZero() {
				t.Fatalf("wrong CronJob metadata: %+v", job)
			}
		case "cronjob/never-ran":
			if len(app.Pods) != 0 || !app.CronJob.Suspended || !app.CronJob.LastScheduledAt.IsZero() {
				t.Fatalf("missing podless CronJob: %+v", app)
			}
		case "shared":
			if !reflect.DeepEqual(app.Pods, []string{"api-pod"}) {
				t.Fatalf("CronJob was grouped by shared label: %+v", app)
			}
		case "stale":
			if !reflect.DeepEqual(app.Pods, []string{"stale-pod"}) {
				t.Fatalf("stale ownership was accepted: %+v", app)
			}
		default:
			t.Fatalf("unexpected app: %+v", app)
		}
	}
	podsFound, selectors, _, err := runner.MatchApps(context.Background(), "monthly*")
	if err != nil || !reflect.DeepEqual(podsFound, []string{"monthly-pod"}) || len(selectors) != 0 {
		t.Fatalf("CronJob wildcard changed scope: pods=%v selectors=%v err=%v", podsFound, selectors, err)
	}
}

func TestCronJobDiscoveryErrorsKeepPartialInventory(t *testing.T) {
	for _, denied := range []string{"cronjobs", "jobs"} {
		t.Run(denied, func(t *testing.T) {
			pods := map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "api", "labels": map[string]any{"app": "api"}}}}}
			cronJobs := map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "weekly"}, "spec": map[string]any{"schedule": "0 0 * * 0"}}}}
			runner := cronJobFixture(t, pods, map[string]any{"items": []any{}}, cronJobs, denied)
			apps, err := runner.Apps(context.Background())
			want := 1
			if denied == "jobs" {
				want = 2
			}
			if err == nil || !strings.Contains(err.Error(), "Forbidden") || len(apps) != want {
				t.Fatalf("apps=%+v, err=%v", apps, err)
			}
			podsFound, selectors, _, matchErr := runner.MatchApps(context.Background(), "api*")
			if matchErr != nil || len(podsFound) != 0 || !reflect.DeepEqual(selectors, []string{"app=api"}) {
				t.Fatalf("partial app match: pods=%v selectors=%v err=%v", podsFound, selectors, matchErr)
			}
		})
	}
}

func TestPodlessCronJobMatchesDoNotFallBackToNamespaceLogs(t *testing.T) {
	runner := cronJobFixture(t, map[string]any{"items": []any{}}, map[string]any{"items": []any{}},
		map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "weekly"}}}}, "")
	_, _, _, err := runner.MatchApps(context.Background(), "cronjob/*")
	if err == nil || !strings.Contains(err.Error(), "no retained pods") {
		t.Fatalf("err=%v", err)
	}
}

func TestRunCronJobUsesScopedCreateAndUniqueBoundedNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX kubectl fixture")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	t.Setenv("TAILG_CRON_ARGS", argsFile)
	binary := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TAILG_CRON_ARGS\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runner := Runner{Binary: binary, Context: "tkgs-qa", Namespace: "cronjob"}
	cronName := strings.Repeat("a", 52)
	first, err := runner.RunCronJob(context.Background(), cronName)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runner.RunCronJob(context.Background(), cronName)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) > 63 || !cronJobNamePattern.MatchString(first) {
		t.Fatalf("invalid/nonunique Job names: %q %q", first, second)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--context", "tkgs-qa", "-n", "cronjob", "create", "job", second, "--from=cronjob/" + cronName}
	if got := strings.Split(strings.TrimSpace(string(data)), "\n"); !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%v want=%v", got, want)
	}
	if _, err := runner.RunCronJob(context.Background(), "--all"); err == nil {
		t.Fatal("accepted invalid CronJob")
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'Forbidden: cannot create jobs' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	name, err := runner.RunCronJob(context.Background(), "weekly")
	if err == nil || name == "" || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("failed create name=%q err=%v", name, err)
	}
	if got := manualJobName(strings.Repeat("a", 30)+"-"+strings.Repeat("b", 21), time.Now(), "12345678"); strings.Contains(got, "--manual") {
		t.Fatalf("bad truncated prefix: %q", got)
	}
}
