# Blacknode operations platform implementation

Scope: implement the ten requested platform capabilities and the file-transfer and background-monitoring improvements. Existing user changes and configuration are preserved.

## Delivery checklist

- [ ] Streaming file transfer queue, progress, cancellation, verified resumability.
- [ ] Background metrics with retained history and polling state across views.
- [ ] Persistent SSH sessions with tmux/Zellij and workspace reattachment.
- [ ] Bash/Zsh/Fish command integration, contextual completion, remote file links.
- [ ] Saved service workspaces with environment, hosts, Kubernetes and tunnel context.
- [ ] Kubernetes contexts, resources, events, exec, forwarding, manifest review, rollouts.
- [ ] Durable runbook executions, revisions, scheduling, conditions, retries and health-gated batches.
- [ ] Saved incidents with evidence, annotations, deployment markers, comparison and export; Prometheus/Loki queries.
- [ ] Terraform/OpenTofu plan review, Ansible execution and GitHub/GitLab pipeline integration.
- [ ] Fleet inventory refresh, dynamic groups, baselines and configuration/package/service/image drift.
- [ ] Optional runner/control plane with shared runbooks, server-enforced roles, temporary access, OIDC, audit and collaborative terminal control.
- [ ] Navigation, documentation, generated bindings, regression tests and builds.

## Verification

Run focused tests for new persistence, command construction, execution recovery, authorization and parsers, then the full Go and frontend checks/build. External infrastructure and provider flows are exercised using local fixtures; production credentials and deployments are not required to build or test the platform.

## Progress

- Repository and existing service/frontend lifecycles reviewed. Implementation in progress.
