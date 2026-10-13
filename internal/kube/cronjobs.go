package kube

import (
	"context"
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/fernandocpaz/tailg/internal/core"
)

// CronJobs are listed independently of their retained Jobs and pods. Job
// ownership is used instead of labels, which can be shared with deployments.
func (r Runner) pickerCronJobs(ctx context.Context) (map[string]*core.CronJobInfo, map[string]map[string]any, error) {
	cronJobs := make(map[string]*core.CronJobInfo)
	jobs := make(map[string]map[string]any)
	payload, err := r.JSON(ctx, "get", "cronjobs")
	if err != nil {
		return cronJobs, jobs, fmt.Errorf("could not list CronJobs: %w", err)
	}
	objects := make(map[string]map[string]any)
	for _, raw := range sliceValue(payload["items"]) {
		object := mapValue(raw)
		name := stringValue(mapValue(object["metadata"])["name"])
		if name == "" {
			continue
		}
		spec, status := mapValue(object["spec"]), mapValue(object["status"])
		cronJobs[name] = &core.CronJobInfo{
			Name: name, Schedule: stringValue(spec["schedule"]), TimeZone: stringValue(spec["timeZone"]),
			Suspended: boolValue(spec["suspend"]), Active: len(sliceValue(status["active"])),
			LastScheduledAt: parseKubeTimestamp(status["lastScheduleTime"]),
		}
		objects[name] = object
	}
	if len(cronJobs) == 0 {
		return cronJobs, jobs, nil
	}
	payload, err = r.JSON(ctx, "get", "jobs")
	if err != nil {
		return cronJobs, jobs, fmt.Errorf("CronJobs loaded; could not associate their pods: %w", err)
	}
	for _, raw := range sliceValue(payload["items"]) {
		job := mapValue(raw)
		owner, ok := controllerReference(ownerReferences(mapValue(job["metadata"])))
		if !ok || stringValue(owner["kind"]) != "CronJob" {
			continue
		}
		cronJob := objects[stringValue(owner["name"])]
		if cronJob != nil && sameUID(owner, cronJob) {
			jobs[stringValue(mapValue(job["metadata"])["name"])] = job
		}
	}
	return cronJobs, jobs, nil
}

func cronJobForPod(pod map[string]any, jobs map[string]map[string]any, cronJobs map[string]*core.CronJobInfo) *core.CronJobInfo {
	owner, ok := controllerReference(ownerReferences(mapValue(pod["metadata"])))
	if !ok || stringValue(owner["kind"]) != "Job" {
		return nil
	}
	job := jobs[stringValue(owner["name"])]
	if job == nil || !sameUID(owner, job) {
		return nil
	}
	cronOwner, _ := controllerReference(ownerReferences(mapValue(job["metadata"])))
	return cronJobs[stringValue(cronOwner["name"])]
}

var cronJobNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// RunCronJob creates one Job from the current template. It never changes the
// schedule or suspension flag, and never retries a potentially accepted create.
func (r Runner) RunCronJob(ctx context.Context, cronJob string) (string, error) {
	if len(cronJob) > 253 || !cronJobNamePattern.MatchString(cronJob) {
		return "", fmt.Errorf("invalid CronJob name %q", cronJob)
	}
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate Job name: %w", err)
	}
	name := manualJobName(cronJob, time.Now(), fmt.Sprintf("%x", random))
	_, err := r.Run(ctx, "create", "job", name, "--from=cronjob/"+cronJob)
	if err != nil {
		return name, err
	}
	return name, nil
}

func manualJobName(cronJob string, now time.Time, random string) string {
	suffix := "-manual-" + now.UTC().Format("20060102-150405") + "-" + random
	prefix := cronJob[:min(len(cronJob), 63-len(suffix))]
	return strings.TrimRight(prefix, ".-") + suffix
}
