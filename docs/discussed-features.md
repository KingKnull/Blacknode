# Discussed feature status

This tracks the additions discussed after the initial roadmap. “Present” means a code path and local checks, **not** that every subfeature is complete or that live infrastructure has been exercised. The GTK4 desktop build and UI click-through remain unverified in this container. The container has GTK4 4.8.3, while Wails beta.34's supported native build requires GTK4 4.14 or newer; server-tagged builds and tests remain available here.

| Area | Present | Still open / not claimed |
| --- | --- | --- |
| Bastions | Recursive ProxyJump, SSH-config import and topology, pooled direct bastion clients (`internal/sshconn/pool.go`). | Proxied *target* clients are not pooled; live multi-hop smoke test. |
| Credentials | Local encrypted vault, SSH agent, FIDO2 registration, SSH certificates, 1Password host discovery and advisory key-rotation dates (`internal/service/keyservice.go`). | General Bitwarden/Vault credential-provider support, native OS keychain, automatic rotation. |
| Shared sessions | Scoped runner, observer/peer access, approval of control requests and ephemeral annotations (`internal/service/runnerhttp.go`). | Durable notes, encrypted recordings, presence and two-desktop smoke test. |
| Terminal | Command palette, snippets, history, search, broadcast review, configurable shortcut editor with validation/conflict handling, live-pane output bookmarks and reviewed text export (`frontend/src/lib/`). | Shortcut values persist only in versioned localStorage on this device; bookmarks are memory-only and do not persist when the pane closes. |
| Runbooks | Git-pinned definitions, durable jobs, schedules and health-gated steps (`internal/service/jobservice.go`, `internal/service/infra_git.go`). | Live provider and rollback validation. |
| Kubernetes | Resource browser, exec/logs, reviewed mutations and Helm review/rollback. | Live cluster validation and wider resource/action coverage. |
| Health | Metrics, HTTP/TCP/DNS/TLS monitors, maintenance windows, notifications and dependency map. | More host-platform collectors and target-environment alert validation. |
| Files | SFTP, safe remote editing, verified file resume, reviewed directory sync, bounded filename search and reviewed, recoverable regular-file deletion. | Archive handling; deletion requires an SFTP hard-link extension and recovery receipts do not survive backend restart; directory retries do not resume partial copies. |
| Tunnels | Local/remote/SOCKS5 profiles, listener status, port-conflict feedback and cleanup (`internal/service/portforwardservice.go`). | Automatic recovery after SSH loss; a local listener is not proof of remote health. |
| Audit | Append-only hash-chain verification, signed exports, runner authorization and redacted metadata. | Independent compliance certification and deployment-specific retention audits. |
| Global search | Bounded operational palette results across hosts, snippets, history, recordings, workspaces, runbooks, incidents, deployments, monitors, scans and activity. One-shot direct selection for monitors, incidents, deployments, scans and runbooks. | Activity remains navigate-only; opening a workspace is intentionally an explicit action. |
| Notifications | In-app/desktop/webhook output and monitoring/job alerts; bounded in-memory webhook delivery with deduplication, offline queueing, and retries for network transport errors plus HTTP 408/429/5xx responses (five total attempts, exponential backoff capped at 30 seconds). | The queue is process-local and bounded to 128 entries/4 MiB (256 KiB per payload), drops the oldest queued item when full, and is lost on process exit; there is no durable offline persistence or retry for ordinary HTTP 4xx responses. |
| Workspace templates | Built-in, local-only and encrypted-sync catalogs of inert service-workspace starting points; manual push merges the remote template catalog first. | Two-device smoke test and server-side conditional writes: the current GET → merge → PUT has no CAS, so simultaneous devices can still race. Synced templates omit host IDs, commands, contexts, profiles and tunnels. |
| Onboarding | First-run guide, tool checks, SSH diagnostics and allowlisted local diagnostic JSON preview/export. | Automated GTK repair is intentionally not attempted; desktop download smoke test. |
| Packaging | Cross-platform tasks, release-check UI and GTK4 Linux CI/package configuration. | Native GTK4 build and package runtime smoke test, signing and automatic update installation. |

## Latest implemented slices

- Owner/admin-reviewed control requests, rechecked server-side before terminal input.
- Scoped session notes with size limits and server-side credential redaction; notes are only in runner memory.
- Per-key due date with persistence and count-only reminder in Keys; rotation is never automatic.
- Tunnel listener status, failure feedback and local port-conflict checks; no remote health probe.
- Bounded palette search and local workspace templates.
- Reviewed terminal command-block text export and in-memory bookmarks; bounded SFTP filename search and recoverable regular-file deletion.
- Configurable terminal shortcuts with conflict/unsafe-chord validation and versioned localStorage persistence; bookmarks remain memory-only.
- Bounded, process-local webhook queue with offline delivery, deduplication, drop-oldest overflow handling, and capped retries; undelivered items are not persisted.
- Explicit palette result selection and a local diagnostic preview/export.
- A synced workspace-template catalog through the existing encrypted configuration snapshot flow.

## Verification boundary

`go test -tags server ./...`, including notification queue/retry/shutdown tests, plus frontend checks/tests/build, including shortcut normalization, conflict, migration and schema-version tests, and local fixtures cover the new slices. Run untagged `go test ./...`, `go vet ./...`, and a native `wails3 build` on the supported GTK4 environment before calling a desktop release ready. No live infrastructure is used for development tests; shortcut persistence is only checked against localStorage, and webhook offline delivery is only checked in process memory.
