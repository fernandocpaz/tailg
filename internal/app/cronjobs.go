package app

import (
	"context"
	"fmt"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
	"github.com/fernandocpaz/tailg/internal/kube"
)

func runPickerCronJob(ctx context.Context, runner kube.Runner, kubeContext, namespace string, app core.AppChoice) string {
	if app.CronJob == nil {
		return "Highlight a CronJob to run it on demand."
	}
	// Pin the displayed scope even if another terminal changes kubeconfig.
	runner.Context, runner.Namespace = kubeContext, namespace
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	name, err := runner.RunCronJob(callCtx, app.CronJob.Name)
	if err != nil {
		return fmt.Sprintf("Job creation was not confirmed for %s: %v. Check for job/%s before trying again.", app.CronJob.Name, err, name)
	}
	return fmt.Sprintf("Created job/%s in %s / %s. Press R to refresh as its pod starts, then Enter to open its logs.", name, kubeContext, namespace)
}
