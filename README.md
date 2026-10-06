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

### Latest complete release

With Go 1.25 or newer installed:

```sh
go install github.com/fernandocpaz/tailg/cmd/tailg@latest
tailg version
```

Use the same command to update. Every tested update merged into `main`
automatically publishes a new stable release, so `@latest` follows released
features. Go's module proxy can briefly cache an older version immediately
after publication; rerun the command after it refreshes.

If `tailg` is not found after installation, add Go's installed-binary directory
to your `PATH`. On Linux or macOS, run this in your terminal and add the same
line to your shell startup file (`~/.bashrc` or `~/.zshrc`) to make it permanent:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
tailg version
```

On Windows PowerShell, run this for the current session, then add the path
printed by `Join-Path (go env GOPATH) 'bin'` to your user `Path` environment
variable to make it permanent:

```powershell
$tailgBin = Join-Path (go env GOPATH) 'bin'
$env:Path += ";$tailgBin"
tailg version
```

### New machine without Go

1. Install Go 1.25 or newer using the official [Go downloads](https://go.dev/dl/)
   and [OS-specific installation instructions](https://go.dev/doc/install).
   Use the Windows MSI, macOS package, or Linux archive for your architecture.
2. Open a new terminal and verify `go version` reports Go 1.25 or newer.
3. Run the `@latest` install command above, add Go's binary directory to
   `PATH` as shown for your OS, and verify with `tailg version`.
4. Install and configure `kubectl` and Kubernetes credentials before using
   Tailg against a cluster.

If you do not want to install Go, download a prebuilt binary for your OS and
architecture from [the latest GitHub release](https://github.com/fernandocpaz/tailg/releases/latest)
and put it on your `PATH`. Release binaries report their version and build time.

## Usage

```sh
tailg example-app default
tailg deployment/example-app default
tailg example-app default --since 4d
tailg example-app default --no-follow
tailg example-app default --include 'request_id=12345'
tailg example-app default --exclude 'debug|trace'
tailg example-app default --buffer-lines 100000
tailg
tailg 'example-*' default --tile-windows
tailg 'web-api,job-worker' default --split-panes
tailg --namespace default
tailg --status --namespace default
tailg --status 20 --namespace default
tailg --versions
tailg --versions --context tkgs-qa --context tkgs-dev
tailg example-app default --dump
tailg deployment/example-app default --deployment-dump
tailg troubleshoot example-app default
tailg issues example-app default
tailg diagnose example-app default --output ndjson
```

Running `tailg` with no target opens the interactive application picker in the
current Kubernetes namespace.
Press `M` in this main application list to toggle log monitoring (off by
default). It scans all containers in every pod belonging to the displayed
applications immediately, then polls the previous minute of logs every 60
seconds. The menu always shows `M Monitor`, and the status line shows
`Monitor: OFF` or `Monitor: ON · every 1m`, scan progress, the last scan time,
and any failed pod scans.

Application names reflect the combined log activity of their pods: green for
clean activity, gray for little activity or incomplete clean scans, orange for
warnings, red for errors, and flashing red for at least five errors per minute
across the application. Warning trends compare consecutive scans. The cursor
and selection checkbox remain visible alongside these colors. Monitoring also
works in the `P` exact-pod picker, where each pod has its own color. Press `M`
again to stop; leaving either picker cancels its active scan. Scans use at most
four concurrent workers with a 20-second timeout.

Press `C` in the picker to choose another
kubeconfig context, or `N` to choose a namespace in that context. The namespace
picker also accepts a typed name with `/` when listing namespaces is blocked by
cluster permissions. These switches last for this tailg session and do not
change the context or namespace used by other terminals.
The current selection stays at the top of each picker, followed by the most
used choices (five rows total). Select **More** to browse less used choices. tailg remembers
usage locally when you open an application, and tracks namespaces separately
for each context.
After closing a log view, the picker returns in the selected context. Targets
may still be Kubernetes resources, app names, case-insensitive wildcard app
patterns such as `example-*`, or
comma-separated app names. The old standalone `*` picker target has been removed.
The namespace can be supplied positionally or with `--namespace`.
In the application picker, press `Space` to check multiple applications, then
`Enter` to open them. In Windows Terminal, a multi-selection automatically opens
one split pane per resolved pod; on other platforms the selected logs share the
combined live view. Application selections in the combined view follow new
replica pods after a rollout, and the selection stays checked when you return
to the picker. With nothing checked, `Enter` opens the highlighted application
as before. Uncheck applications and press `P` to choose individual pod names across applications instead:
`Space` checks pods, `A` toggles all pods for the highlighted application, and
`Enter` opens the checked pods, using the same automatic split-pane behavior.
Exact pod selections stay pinned to those names.
Switching context or namespace clears checked applications.
If credentials have expired, the picker shows the kubectl error so you can
press `C` to choose another context or log in and press `R` to retry.

Without `--since`, tailg loads up to 500 visible lines per container before
following new logs. When include/exclude rules remove most of the raw Kubernetes
tail, tailg progressively scans older lines until it fills that visible history,
reaches the start of retained logs, or reaches the `--buffer-lines` scan limit.
Supplying `--since` without an explicit `--tail` reads every retained line in
that window. Day values are converted for `kubectl`, so `--since 4d` becomes
`--since 96h`.

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
Below it, a source bar shows the full pod and container for the selected log,
wrapping long names. Before logs arrive, a single-pod view shows its pod name.
In multi-pod views, row identifiers include the workload name and replica suffix.
Log rows align timestamps, levels, and pod identifiers when space permits;
matching text is highlighted and complete-history search progress appears next
to the filter mode.

The log timeline inserts non-selectable local-calendar separators such as
`Today · 2026-09-18`, `1 day ago · 2026-09-17`, and
`2 days ago · 2026-09-16`. The active date remains visible when the viewport
starts inside a group, while live and paused views preserve their anchors.

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
| `F7` | Explain the selected pod's replica count and controller |
| `Up` / `Down` | Move the selected log line |
| `PageUp` / `PageDown` | Move by one screen |
| `Home` / `End` | Jump to the start or resume live tailing |
| `Enter` | Inspect the full log entry and stack trace; press again to copy |
| `Esc` | Close the current detail panel |
| `Ctrl+C` / `Ctrl+Q` | Exit |

F1's matching-only mode and the filter text are synchronized across panes and
tiled windows launched by the same parent process. Secret values are decoded
only after you explicitly open the selected Secret.

The selected-log inspector includes adjacent exception and stack-trace lines
from the same pod/container, even when a filter hides those lines. It reloads
the entry from Kubernetes without the live buffer or `--tail` limits; selecting
a stack frame also finds the error header. A new timestamped/leveled/JSON entry
ends the block. Common .NET, Java and Python exception formats are recognized;
unrecognized unindented output stays separate.

Long lines and payloads wrap without ellipses, and log readers have no fixed
per-line size limit. Use `Up`/`Down`, `PageUp`/`PageDown`, or `Home`/`End` to read
the full entry. `Enter` copies all its text, including off-screen lines, after
loading finishes. `R` reloads the entry if the application is still writing it.
If retrieval fails or the selected log has rotated away, the inspector keeps
the buffered text and shows a warning. Text already shortened by the source
application (for example, a truncated `SqlPreview`) cannot be reconstructed.

The Issue Radar continuously groups error levels, HTTP 5xx responses, panics,
exceptions, timeouts, connection failures, retries, and stream interruptions.
It shows active issue and event counts without hiding the live logs. Select an
issue and press `Enter` to load its complete-history context. Issue text and
service names wrap without truncation. Use `Up`/`Down` to select an issue and
`PageUp`/`PageDown` to read an issue longer than the screen. HTTP requests
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

For a single-pod view, the source bar also summarizes why replicas exist. Press
`F7` for the observed controller chain, desired/current/ready counts, rollout
surge evidence, and any matching HorizontalPodAutoscaler range and conditions.
In multi-pod views, first select a log so `tailg` knows which pod to explain.
Replica explanations report what Kubernetes currently exposes; they do not
guess the operator's intent, and restricted RBAC is shown as incomplete data.

Use `--no-live-filter` for plain streaming output.

## Windows Terminal layouts

On Windows, `tailg --namespace default` opens one tab per pod. Add
`--tile-windows` to create and arrange separate terminal windows, or
`--split-panes` on a multi-pod target to create one pane per pod. Child sessions
preserve context, container selection, time window, filters, formatting, and
heartbeat settings.

## Image versions

`tailg --versions` prints the configured tag and the running image ID for every
regular, init, and ephemeral container in the current namespace. The image ID is
normalized to its immutable digest when the runtime provides one. Use
`--namespace` or `--context` to select another namespace or cluster.

Repeat `--context` to compare environments. The comparison groups replica pods
by workload and container. If none of the matching pods uses `latest`, tags
are compared and displayed directly. If any matching pod in any context uses
`latest` (including an omitted tag), all pods in that row are compared by
running Image ID. This keeps normal rows readable while correctly comparing a
numbered tag against `latest`. The grid includes a `COMPARE` column and
prints only differing versions, mixed rollout versions, unavailable Image IDs,
or containers missing from a context:

```sh
tailg --versions --context tkgs-qa --context tkgs-dev
```

## Status and diagnostics

`tailg --status` scans pod health, reports grouped errors from the previous two
hours, and waits for unhealthy pods to recover. Pass a positive whole number of
minutes as the optional positional value—for example, `tailg --status 20` scans
the previous 20 minutes. Pod health and recent errors are shown in timestamped,
wrapped tables; error groups include their exact and relative last-seen time,
pod/container sources, and complete classified message when available.
Interactive terminals use adaptive severity colors and rounded borders, with a
stacked layout on narrow screens. Recent-error rows include a `SELECT` number.
After the scan, interactive sessions show a selector: use Up/Down + Enter or
click a row with the mouse. Opening an error launches the normal tailg log view
for that pod/container, keeps the same status lookback window, and preloads the
issue search term so the matching error is brought into view. Esc returns to the
status flow. Redirected output remains plain ASCII, and `--no-color` keeps the
interactive layout without ANSI colors.
The error scan runs once; recent errors are evidence and do not keep an otherwise
healthy namespace in the recovery loop. Use
`--status-interval` and `--status-timeout` to adjust polling and deadline. In an
interactive terminal it can open consoles for unhealthy pods;
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
This includes quoted credential values and Basic/Bearer authorization headers.
Redaction is best-effort, not a guarantee for arbitrary secrets or encodings;
review diagnostics before sharing them. Kubernetes Secret values are never fetched.

Exit codes are designed for automation: `0` is healthy, `1` means warnings,
`2` means errors or unhealthy pods, and `3` means collection or output failed.

`tailg monitor` maintains persistent workload-scoped incidents and emits one
JSON record per poll. Its cursor survives restarts; incomplete coverage pauses
resolution, and an incident resolves only after a complete quiet period. Pin
the Kubernetes context and namespace explicitly:

```sh
tailg monitor --context staging --namespace payments --state .tailg/staging-payments.json
tailg monitor --context staging --namespace payments --state .tailg/staging-payments.json --once
```

Monitor results include both a health status and collection coverage. Treat
`unknown`, `partial`, and `unavailable` as “health could not be established,”
not as healthy; incomplete coverage pauses incident resolution. A follow-up
agent can create or reuse an Azure DevOps work item for an open incident when
the MCP server is configured with `--state`, `--ado-organization`, and
`--ado-project`, plus the `TAILG_AZDO_TOKEN` environment variable. The token
should have only the Azure DevOps Work Items permission needed to create and
query work items. Tailg tags each item with its incident ID so retries reuse
the same work item. For an Azure DevOps PAT, grant the `vso.work_write` scope
needed to query and create work items.

To include the complete retained error block in the work item, call
`tailg_capture_issue_evidence` with the incident's `issueId`, then pass its
returned `evidenceId` alongside the incident's `id` as `incidentId` to
`tailg_create_azure_devops_work_item`. Tailg verifies the saved evidence and
uploads it as a text-file attachment, including on a retry against an existing
work item; it does not paste multi-megabyte logs into the 20,000-character
description. Large files use Azure DevOps chunked upload and remain subject to
the organization's attachment limit. If evidence capture fails, omit
`evidenceId` and the agent can still create the incident work item with a note
explaining the missing evidence.

`tailg mcp` exposes one-shot diagnostics, `tailg_monitor`,
`tailg_list_incidents`, `tailg_get_changes`, and tools to save and page through
full retained issue evidence, plus optional Azure DevOps work-item creation.
Configure `--state`, `--context`, and `--namespace` for persistent monitoring,
and `--evidence-dir` for evidence capture. Set `TAILG_AZDO_TOKEN` in the MCP
server process environment; do not put the token in the agent's tool arguments.
The agent can resume polling from `nextCursor`; monitor output includes
coverage so it can distinguish a healthy environment from an incomplete scan.
Example client entry:

```json
{
  "mcpServers": {
    "tailg": {
      "command": "tailg",
      "args": ["mcp", "--context", "staging", "--namespace", "payments", "--state", ".tailg/staging-payments.json", "--evidence-dir", ".tailg/evidence", "--ado-organization", "your-organization", "--ado-project", "your-project"]
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
