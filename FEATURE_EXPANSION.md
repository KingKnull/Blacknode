# Infrastructure feature expansion

User request: implement all nine recommended additions. Existing working-tree changes are preserved.

## Delivery

- [x] HTTP/TCP/DNS monitoring, latency/history, TLS expiry, maintenance windows and status dashboard.
- [x] Local/remote directory comparison, reviewed synchronization, exclusions, conflict choices and saved profiles.
- [x] Service dependency graph, health mapping, downstream impact and optional trace-derived discovery.
- [x] Trivy scans, severity/filtering, available fixes and historical comparison.
- [x] YAML runbooks in Git, change review, commit-pinned execution and restore/import.
- [x] Standalone runner, durable schedules and monitoring, authenticated incoming runbook triggers.
- [x] Server-enforced team roles, expiring credentials, audit events and observer/peer terminal sessions.
- [x] Helm list/values/history, upgrade preview and reviewed rollback.
- [x] AWS Session Manager profiles and interactive sessions using AWS CLI and Session Manager plugin.
- [x] Global operational search and credential-free service-workspace templates.
- [x] Navigation, bindings, documentation, regression tests, UI verification and server/frontend builds.

## Explicit environment limitation

The remaining native Linux desktop limitation is outside the feature implementation: Wails beta.34's GTK4 backend needs newer GTK development headers than the current Debian container provides. No production infrastructure is contacted by the test suite; external tools and provider integrations are checked through local fixtures or availability diagnostics.

## Verification

Use isolated databases, local HTTP/SSH fixtures and mocked external CLIs. Never run scans, deployments, synchronization or commands against the user's infrastructure during development. External dependencies are detected and explained in the UI. Backend mutations use revision checks or expiring, single-use reviews where appropriate.
