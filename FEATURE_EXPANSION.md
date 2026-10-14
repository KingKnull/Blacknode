# Infrastructure feature expansion

User request: implement all nine recommended additions. Existing working-tree changes are preserved.

## Delivery

- [ ] HTTP/TCP/DNS monitoring, latency/history, TLS expiry, maintenance windows, status dashboard.
- [ ] Local/remote directory comparison, reviewed synchronization, exclusions, conflict choices, saved profiles.
- [ ] Service dependency graph, health mapping, downstream impact; optional trace-derived discovery.
- [ ] Trivy scans, severity/filtering, available fixes, historical comparison.
- [ ] YAML runbooks in Git, change review, commit-pinned execution and restore/import.
- [ ] Standalone runner, durable schedules and monitoring, authenticated incoming runbook triggers.
- [ ] Server-enforced team roles, expiring credentials, audit events, observer/peer terminal sessions.
- [ ] Helm list/values/history, upgrade preview and reviewed rollback.
- [ ] AWS Session Manager profiles and interactive sessions using AWS CLI and Session Manager plugin.
- [ ] Navigation, bindings, documentation, regression tests, UI verification and local builds.

## Verification

Use isolated databases, local HTTP/SSH fixtures and mocked external CLIs. Never run scans, deployments, synchronization or commands against the user's infrastructure during development. External dependencies are detected and explained in the UI. Backend mutations use revision checks or expiring, single-use reviews where appropriate.
