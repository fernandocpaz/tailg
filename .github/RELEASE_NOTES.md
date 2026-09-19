# tailg v0.1.0

The first packaged release of `tailg` brings the complete Kubernetes incident-response console to Linux, Windows, and macOS.

## Highlights

### Live Kubernetes log console

- Follow deployments, StatefulSets, DaemonSets, Jobs, pods, wildcard app selections, and multiple workloads.
- Search retained Kubernetes history while continuing to preserve incoming live logs.
- Filter structured fields such as level, service, pod, trace, HTTP method, path, status, and duration.
- Keep health and readiness probe noise out of the visible buffer by default while still tracking stream activity.
- Group logs with sticky local-calendar separators such as `Today`, `1 day ago`, and the exact date.
- Show clear pod/container identity, reconnect state, log age, receipt age, and per-stream freshness.

### Request and error investigation

- Follow a trace across the selected workloads with the F6 request timeline.
- Preserve partial trace results when pods disappear during rollouts.
- Flag HTTP requests slower than 250 ms and group slow endpoints in Issue Radar.
- Group errors, warnings, panics, exceptions, timeouts, connection failures, retries, and stream interruptions.
- Mark issue groups as `NEW` or `KNOWN` against a session baseline.
- Wrap and page complete issue text and selected raw log events without truncating the useful root cause.
- Correctly hide SSRS diagnostic JSON followed by fields such as `ResponseBodyExcerpt=null` in compact mode while retaining it in raw details.

### Kubernetes diagnostics

- Explain replica counts through controller, rollout, and HorizontalPodAutoscaler evidence with F7.
- Inspect mapped ConfigMaps and Secrets, heartbeat health, and per-container stream state.
- Use `tailg --status [minutes]` for timestamped health and recent-error tables; the default lookback is two hours.
- Use `tailg diagnose` or `tailg troubleshoot` for bounded, structured incident reports suitable for people and AI agents.
- Compare configured and running image versions across contexts with `tailg --versions`, using image IDs when any compared workload uses `latest`.

### Reliability and operations

- Resume reconnecting streams near the last Kubernetes timestamp and suppress bounded replay overlap.
- Backfill older retained logs when default exclusions would otherwise leave the visible history empty.
- Synchronize filter text and matches-only mode across Windows Terminal panes and tiled windows.
- Report version, source commit, and build time through `tailg version` and the F4 diagnostics view.

## Install

```sh
go install github.com/fernandocpaz/tailg/cmd/tailg@v0.1.0
tailg version
```

Prebuilt binaries are attached for Linux, Windows, and macOS on amd64 and arm64.
