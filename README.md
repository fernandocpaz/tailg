# tailg

Human-friendly Kubernetes log tailing, built in Go.

`tailg` follows logs across deployments, stateful sets, daemon sets, jobs, and
pods by calling your configured `kubectl`. It adds a full-screen filter, shared
filtering across Windows Terminal panes, pod resource inspection, heartbeat
diagnostics, namespace health monitoring, troubleshooting bundles, and bounded
structured diagnostics for scripts and AI agents.

## Requirements

- Go 1.25 or a prebuilt release binary
- `kubectl` installed and configured
- Windows Terminal for `--split-panes`, `--tile-windows`, and namespace tabs
- Optional: `git` for repository inspection offered during interactive status checks

## Install

With Go:

```sh
go install github.com/fernandocpaz/tailg/cmd/tailg@latest
```

Or download the binary for your platform from GitHub Releases and put it on
your `PATH`.

Check the installed version and source commit with `tailg version` or
`tailg --version`. Release binaries also report their build time. To install
the current main branch before a new release is tagged:

```sh
go install github.com/fernandocpaz/tailg/cmd/tailg@main
tailg version
```

## Usage

```sh
tailg example-app default
tailg deployment/example-app default
tailg example-app default --since 4d
tailg example-app default --no-follow
tailg example-app default --include 'request_id=12345'
tailg example-app default --exclude 'debug|trace'
tailg example-app default --buffer-lines 100000
tailg '*' default
tailg 'example-*' default --tile-windows
tailg 'web-api,job-worker' default --split-panes
tailg --namespace default
tailg --status --namespace default
tailg example-app default --dump
tailg deployment/example-app default --deployment-dump
tailg troubleshoot example-app default
tailg issues example-app default
tailg diagnose example-app default --output ndjson
```

Targets may be Kubernetes resources, app names, case-insensitive wildcard app
patterns, or comma-separated app names. Use `*` to select an app interactively.
The namespace can be supplied positionally or with `--namespace`.

Without `--since`, the last 500 lines per container are loaded before following
new logs. Supplying `--since` without an explicit `--tail` reads every retained
line in that window. Day values are converted for `kubectl`, so `--since 4d`
becomes `--since 96h`.

Probe traffic matching `health|ready|live` is hidden by default. Repeat
`--exclude` to hide additional patterns, use `--include` to retain only selected
lines, or pass `--no-default-exclude` to show probe lines. Structured JSON
properties appended to readable text are hidden unless `--detail` is set.

## Live view

Following logs opens the full-screen filter by default. Typing in the filter
searches both the live buffer and the complete history Kubernetes still retains
for the chosen time window. The live buffer retains at most 50,000 lines by
default; use `--buffer-lines` to choose a different positive limit. Interrupted
pod log streams reconnect automatically with a capped backoff.

In the full-screen view, reconnects resume near the last received Kubernetes
timestamp instead of requesting the initial tail again. The overlap is checked
against previously delivered occurrences, so replayed lines do not reappear or
inflate Issue Radar counts. Reconnects read all retained logs after that position,
even when more lines arrived during a disconnect than the initial `--tail` limit.
Replay tracking is bounded to 4,096 timestamp/message identities per container;
untracked or undated lines are preserved even if they might be repeats. Kubernetes
log rotation can still remove history before it is recovered.

The header shows the service, namespace, pod count, and live connection state.
Log rows align timestamps, levels, and pod identifiers when space permits;
matching text is highlighted and complete-history search progress appears next
to the filter mode.

The header also shows receipt age (`recv`) and the age of the last received log
(`log`) for a single stream. An old log received just now still shows its original
age. `WAITING` means no data has confirmed the current attempt; `QUIET` means no
line has arrived for 30 seconds and does not imply that the pod is unhealthy.
Filtered probe and heartbeat lines still count as stream activity. For multiple
streams, press `F4` to inspect each pod/container independently, along with replay
counts, queued events and live buffer usage. The ages continue updating while
the view is paused or the service is quiet.

| Key | Action |
| --- | --- |
| `F1` | Toggle between context view and matching lines only |
| `F2` | Browse ConfigMaps and Secrets mapped into the pod |
| `F3` | Open the Issue Radar for grouped errors and warnings |
| `F4` | Inspect per-container log freshness, replay counts and buffer usage |
| `F5` | Open heartbeat diagnostics |
| `F6` | Follow the selected request's trace across the selected workloads |
| `Up` / `Down` | Move the selected log line |
| `PageUp` / `PageDown` | Move by one screen |
| `Home` / `End` | Jump to the start or resume live tailing |
| `Enter` | Inspect the original log and metadata; press again to copy |
| `Esc` | Close the current detail panel |
| `Ctrl+C` / `Ctrl+Q` | Exit |

F1's matching-only mode and the filter text are synchronized across panes and
tiled windows launched by the same parent process. Secret values are decoded
only after you explicitly open the selected Secret.

The Issue Radar continuously groups error levels, HTTP 5xx responses, panics,
exceptions, timeouts, connection failures, retries, and stream interruptions.
It shows active issue and event counts without hiding the live logs. Select an
issue and press `Enter` to load its complete-history context. HTTP requests
taking **more than 250 ms**, including successful responses, get a `SLOW` flag
and an endpoint group with counts, maximum duration, and a representative trace.
Obvious numeric, UUID, and long hexadecimal path IDs are grouped together.

The radar marks groups `NEW` when their first source timestamp is after the
session baseline; historical or undated records are `KNOWN`. Press `B` in the
radar to mark the currently retained groups known and start a new baseline;
press `C` to clear the groups. Baselines are local to the session and bounded
by the retained issue groups.

### Structured filters and request timelines

Type field filters directly in the live filter box. They also apply to the
complete-history search and synchronize across panes:

```text
level:error service:api
duration:>250ms method:GET path:/orders
status:>=500
trace:11d7729cbf34122f3af8d3c73a47213d
pod:worker timeout
```

All predicates must match. `level`, `method`, and `trace` use exact matches;
`service`, `pod`, and `path` use case-insensitive substrings. `status` and
`duration` support `=`, `!=`, `>`, `>=`, `<`, and `<=`. Durations accept units
such as `250ms` or `2s`; a number without units means milliseconds. Remaining
text is a case-insensitive substring search. Invalid field values show an error.
Fields are extracted from readable HTTP/Serilog messages and JSON properties,
including properties hidden from compact log rows.

Select a log or an Issue Radar group and press `F6` to collect logs with the
same trace ID, ordered by Kubernetes timestamp. In split panes, lookup keeps
the original selected workload scope. Press `Enter` for the original log,
`R` to reload, and `Esc` to close. Missing streams and timeouts are reported as
incomplete results. This is a log timeline: it depends on propagated trace IDs,
your include/exclude filters, the selected time window, Kubernetes retention,
and the configured buffer limit; it cannot reconstruct spans that were not
logged. `F4` includes the running build version for troubleshooting.

Use `--no-live-filter` for plain streaming output.

## Windows Terminal layouts

On Windows, `tailg --namespace default` opens one tab per pod. Add
`--tile-windows` to create and arrange separate terminal windows, or
`--split-panes` on a multi-pod target to create one pane per pod. Child sessions
preserve context, container selection, time window, filters, formatting, and
heartbeat settings.

## Status and diagnostics

`tailg --status` scans the current namespace and waits for unhealthy pods to
recover. Use `--status-interval` and `--status-timeout` to adjust its polling and
deadline. In an interactive terminal it can open consoles for unhealthy pods;
when `git` is available, it can also inspect configured workload repositories.

For a fast human-oriented investigation, run:

```sh
tailg troubleshoot example-app default
```

`troubleshoot` is an alias for `diagnose` that defaults to readable terminal
output. It correlates application log issues with current pod health, container
state, restart counts, the last container termination reason and exit code,
recent Kubernetes Warning events, and evidence-based next actions. Historical
restarts are diagnostic evidence only; a recovered pod is not marked unhealthy
just because it restarted earlier.

`--dump` writes cluster context, namespace events and pod state, resource YAML,
pod descriptions, and current/previous logs. `--deployment-dump` additionally
collects rollout status and history. Each bundle includes `README.md`,
`manifest.json`, and a navigable `index.html`.

## Scripts and AI agents

`tailg issues` and `tailg diagnose` are one-shot, read-only commands. JSON and
NDJSON emit the versioned `tailg.ai/v1` schema and never launch the TUI. The
schema includes pod/container crash evidence and bounded troubleshooting
recommendations. Human-readable text is also available explicitly:

```sh
tailg issues example-app default
tailg issue 8d769df321ca3f17 example-app default
tailg diagnose --namespace default --max-lines 5000 --max-bytes 2097152
tailg diagnose example-app default --output text
```

Issue IDs are stable across dynamic values such as request IDs, so an agent can
request the same issue's bounded context. The commands apply strict limits for
collection time, lines, grouped issues, context lines, and encoded bytes. Common
bearer tokens, JWTs, passwords, API keys, and secrets are redacted before output.
Secret values are never fetched.

Exit codes are designed for automation: `0` is healthy, `1` means warnings,
`2` means errors or unhealthy pods, and `3` means collection or output failed.

`tailg mcp` runs a read-only MCP server over stdio. It exposes
`tailg_list_issues`, `tailg_diagnose`, and `tailg_get_issue_context`, using the
same collection and classification engine as the CLI. A typical client entry is:

```json
{
  "mcpServers": {
    "tailg": {
      "command": "tailg",
      "args": ["mcp", "--namespace", "default"]
    }
  }
}
```

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/tailg
```
