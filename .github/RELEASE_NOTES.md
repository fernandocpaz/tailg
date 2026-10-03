# tailg v0.1.1

This release promotes the current feature-complete `main` branch to the latest packaged version.

## Highlights

- Bare `tailg` opens the application picker by default.
- Application picker uses compact aligned columns and shows only the 20 newest deployments.
- Updated terminal palette for better readability on dark backgrounds.
- Full log rows are colored by severity, with INFO de-emphasized.
- `tailg --status` recent errors are selectable and can open directly in the log viewer.
- Includes the Kubernetes diagnostics, multi-context image comparison, trace investigation, issue radar, health-check filtering, full error display, and AI-oriented diagnose/troubleshoot capabilities from the previous release.

## Install

```sh
go install github.com/fernandocpaz/tailg/cmd/tailg@latest
tailg version
```

Equivalent pinned install:

```sh
go install github.com/fernandocpaz/tailg/cmd/tailg@v0.1.1
```
