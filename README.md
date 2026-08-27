# tailg

Human-friendly Kubernetes log tailing, built in Go.

`tailg` follows logs across deployments, stateful sets, daemon sets, jobs, and
pods by calling your configured `kubectl`. It adds a full-screen filter, shared
filtering across Windows Terminal panes, pod resource inspection, heartbeat
diagnostics, namespace health monitoring, and troubleshooting bundles.

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

The header shows the service, namespace, pod count, and live connection state.
Log rows align timestamps, levels, and pod identifiers when space permits;
matching text is highlighted and complete-history search progress appears next
to the filter mode.

| Key | Action |
| --- | --- |
| `F1` | Toggle between context view and matching lines only |
| `F2` | Browse ConfigMaps and Secrets mapped into the pod |
| `F3` | Open the Issue Radar for grouped errors and warnings |
| `F5` | Open heartbeat diagnostics |
| `Up` / `Down` | Move the selected log line |
| `PageUp` / `PageDown` | Move by one screen |
| `Home` / `End` | Jump to the start or resume live tailing |
| `Enter` | Open the selected line; press again to copy |
| `Esc` | Close the current detail panel |
| `Ctrl+C` / `Ctrl+Q` | Exit |

F1's matching-only mode and the filter text are synchronized across panes and
tiled windows launched by the same parent process. Secret values are decoded
only after you explicitly open the selected Secret.

The Issue Radar continuously groups error levels, HTTP 5xx responses, panics,
exceptions, timeouts, connection failures, retries, and stream interruptions.
It shows active issue and event counts without hiding the live logs. Select an
issue and press `Enter` to load its complete-history context; press `C` in the
radar to clear the current baseline.

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

`--dump` writes cluster context, namespace events and pod state, resource YAML,
pod descriptions, and current/previous logs. `--deployment-dump` additionally
collects rollout status and history. Each bundle includes `README.md`,
`manifest.json`, and a navigable `index.html`.

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/tailg
```
