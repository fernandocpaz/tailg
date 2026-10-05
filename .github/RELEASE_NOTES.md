# tailg v0.1.2

Brings the outstanding feature stack and newer fixes into `main` and the latest release.

## Highlights

- `C` switches Kubernetes context; `N` switches namespace. Frequently used choices appear first, with **More** for the rest and namespace history per context.
- `Space` selects multiple applications; `P` selects exact pods. Windows Terminal opens balanced panes for multi-selections, and picker shortcuts use high-contrast badges.
- Authentication errors keep context, namespace, and retry actions available.
- Selected errors show complete multiline exception blocks, wrap long entries, reload retained Kubernetes logs, and copy the full entry. Log readers accept physical lines beyond the old 4 MiB limit.
- Persistent AI monitoring includes stable incident IDs, change replay, coverage-aware recovery, and paginated evidence.
- Azure DevOps integration creates or reuses incident work items and attaches complete evidence, including large chunked uploads.
- Windows-compatible private-file checks and bounded shared-filter lock retries.
- Hardened credential redaction and native Windows CI.
- Day-based `--since` durations retain millisecond precision.
- Retains the newest-20 application picker, severity colors, selectable status errors, date grouping, request investigation, Issue Radar, replica explanations, health-check filtering, and multi-context image comparison.

## Validation

Release publication requires Linux race tests and Windows native tests, plus vet. Binaries are built for Linux, Windows, and macOS on amd64 and arm64.
Kubernetes interactions are tested with fake runners; no live-cluster manual validation was performed.

## Install

```sh
go install github.com/fernandocpaz/tailg/cmd/tailg@latest
tailg version
```

Pinned install:

```sh
go install github.com/fernandocpaz/tailg/cmd/tailg@v0.1.2
```

Prebuilt binaries are attached below for machines without Go.
