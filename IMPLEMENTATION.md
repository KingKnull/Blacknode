# Blacknode operations platform implementation

Scope: implement the ten requested platform capabilities and the file-transfer and background-monitoring improvements. Existing user changes and configuration are preserved.

## Delivery checklist

- [x] Streaming file transfer queue, progress, cancellation, retries and reviewed directory synchronization.
- [x] Background metrics with retained history, polling state, alerts and service-health monitors.
- [x] Persistent SSH sessions, agent forwarding, ProxyJump/bastion topology and durable workspace reattachment.
- [x] Bash/Zsh/Fish command integration, contextual completion, snippets, history search and terminal search.
- [x] Saved service workspaces with environment, hosts, Kubernetes, database, deployment and tunnel context.
- [x] Kubernetes contexts, resources, events, exec, forwarding, manifest review, rollouts and Helm review/rollback.
- [x] Durable runbook executions, revisions, scheduling, conditions, retries, health-gated batches and Git-pinned runbooks.
- [x] Saved incidents with evidence, annotations, deployment markers, comparison and export; Prometheus/Loki queries.
- [x] Terraform/OpenTofu plan review, Ansible execution and Git-based deployment integration.
- [x] Fleet inventory refresh, dynamic groups, baselines and configuration/package/service/image drift.
- [x] Optional runner/control plane with shared runbooks, server-enforced roles, expiring access, audit and collaborative terminal control.
- [x] AWS Session Manager profiles and interactive sessions using the local AWS provider chain.
- [x] Global command-palette search across operational records with bounded, credential-redacted indexing.
- [x] Built-in and local credential-free service-workspace templates.
- [x] Navigation, documentation, generated bindings, regression tests and server/frontend builds.

### Desktop build note

The application targets Wails beta.34 with GTK4/WebKitGTK 6.0 on Linux. The server-tag build and all application tests pass. A normal native Linux build still requires a system GTK4 development environment newer than the container's GTK 4.8.3 because beta.34 uses newer GTK APIs such as `GtkFileDialog`.

## Verification

Run focused tests for new persistence, command construction, execution recovery, authorization and parsers, then the full Go and frontend checks/build. External infrastructure and provider flows are exercised using local fixtures; production credentials and deployments are not required to build or test the platform.

## Progress

- Repository and existing service/frontend lifecycles reviewed. Implementation in progress.
