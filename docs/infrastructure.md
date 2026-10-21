# Infrastructure tools

The tools appear in the sidebar and command palette. Settings → Navigation can
show hidden sections. Profiles, histories, schedules and runbooks are stored in
the Blacknode database. Changes to saved profiles use revision checks; if a
profile changed elsewhere, reload it before saving.

## Service health

Add HTTP/HTTPS, TCP, DNS or TLS monitors in **Service health**. HTTP checks assert
the status code; DNS checks can assert an IP address. HTTPS and TLS checks verify
the certificate and report its expiration date. Choose a failure threshold,
certificate warning period, interval and timeout. Scheduled maintenance suppresses
checks and new alerts during the window. Pause stops automatic checking; **Check
now** still permits a manual probe.

The dashboard refreshes every five seconds and retains the latest 1,000 samples
per monitor. It shows status, latency, uptime and certificate expiry, with a
latency chart and sample history. Uptime excludes maintenance samples. Results
older than three intervals plus the timeout appear stale. Probes run from the
desktop machine while the app is open, or from the runner when imported there.

## Directory sync

Save an SSH host, existing absolute local and remote directories, direction and
exclusions. **Compare saved profile** hashes contents without copying files.
Patterns match relative paths or individual path components; `.git`,
`node_modules` and `*.log` are typical exclusions. Symbolic links must be excluded.

The review lists uploads, downloads, destination-only deletions and conflicts.
Choose local, remote or skip for each conflict. Two-way sync never deletes missing
files. One-way mirror mode explicitly includes destination-only files for review.
Skipped conflicts remain conflicts in later comparisons until both copies agree.

Reviews expire after ten minutes and can be used once. Changed profiles or file
contents require a new comparison. Each replacement is atomic; a later failure
can leave earlier files completed. The result lists completed and failed changes;
compare again before continuing. Comparisons are limited to 10,000 entries and
64 directory levels per profile.

## Service dependencies

Create services and dependencies in **Service map**, and attach hosts and health
checks. An arrow points from a consumer to its dependency. The map shows current
health and propagates failures through transitive dependencies, including cycles.
Select a service to see failing dependencies and affected consumers.

Optional discovery uses a saved Prometheus integration exposing
`traces_service_graph_request_total` with `client` and `server` labels, such as
metrics produced by an OpenTelemetry service graph connector. Discovery creates
an editable preview. Assign health checks and save it to keep the map.

## Security scans

Use **Check tools** to detect Trivy on the selected machine. Local and SSH scans
require Trivy and a POSIX shell; no scanner is downloaded by Blacknode. Save an
image, project filesystem (`fs`) or host filesystem (`rootfs`) profile, then
explicitly run the saved scan. Trivy may download vulnerability databases and
images when a scan runs.

Reports include vulnerabilities, failed configuration checks and secret locations.
Matched secret values are discarded. Filter by severity or search package/CVE/path,
inspect available fixed versions, export a report, or compare two scans of the same
machine and target. Comparison shows new, resolved and remaining findings. Scans
report errors instead of treating missing tools or malformed reports as clean.

## Runbooks in Git

Use an existing local checkout with Git installed. Configure the repository root,
relative YAML file, remote and branch. Export previews definitions, then commits
only that file. Existing changes in the runbook file must be committed or moved
first; changes to other files remain in place. Fetch and push are explicit actions
and use the machine's existing Git credentials.

The YAML format uses version 1 and the same field names as runbook definitions:

```yaml
version: 1
runbooks:
  - id: app-health
    name: Application health
    timeoutSeconds: 30
    stopOnFailure: true
    steps:
      - name: Check local health endpoint
        command: curl --fail --silent http://127.0.0.1:8080/health
```

Load a ref to resolve its exact commit. Import/restore and execution are reviewed
against that immutable commit. Import rejects a newer saved runbook revision;
export rejects changed HEAD or file contents. Executions retain source repository
and commit metadata and appear in runbook execution history. The YAML file is
limited to 1 MiB, 500 runbooks, and one YAML document.

## Standalone runner

The runner hosts authenticated team APIs, durable jobs, schedules, webhooks and
monitoring without a desktop session. Build it independently:

```bash
task build:runner
# Or:
go build -tags server -o bin/blacknode-runner ./cmd/blacknode-runner
```

Create private files containing a vault passphrase and an administrator token
(at least 32 characters), each with mode `0600`. Supply file paths, not secrets,
on the command line:

```bash
./bin/blacknode-runner \
  --database /path/to/runner/blacknode.db \
  --token-file /path/to/runner/admin-token \
  --vault-password-file /path/to/runner/vault-password \
  --listen 127.0.0.1:9250
```

Use a database separate from the desktop database, with one runner process per
database. Non-loopback listeners require `--tls-cert` and `--tls-key`. The runner
does not expose the desktop's Wails bridge. The bootstrap administrator token is
replaced from its file at startup; rotate it by changing the file and restarting.

Connect in **Team operations** with the runner URL and token. The desktop encrypts
the token in its vault. Team → Configuration exports a versioned seed containing
SSH hosts, runbooks and monitors. Import through the team UI or startup `--seed`.
Import is additive. It never replaces existing records. Host credentials and
environment variables are not exported: runner hosts use an SSH agent available
to the runner process. Verify and approve host keys through the configuration UI.

Schedules save the reviewed runbook definition and encrypted variable values.
They survive restart, skip missed intervals and avoid overlapping their previous
run. Jobs interrupted by process termination are marked interrupted, rather than
automatically replaying potentially completed commands. Run the process under a
service manager for continuous operation; an example is included in
[blacknode-runner.service](blacknode-runner.service). Adjust paths and the service
user before installing it. Supply `SSH_AUTH_SOCK` for the service's SSH agent.

Incoming triggers are created by administrators against a saved runbook revision
and explicit hosts. Each has a separate bearer token, expiry and cooldown. Call
`POST /hooks/<trigger-id>` with `Authorization: Bearer <trigger-token>`. Bodies are
limited to 64 KiB and are ignored rather than interpolated into commands. Variables
must have defaults. A successful call returns `202` and a job ID; duplicate calls
inside the cooldown return `429`. Editing the runbook invalidates that revision
until a new trigger is reviewed. Disable a trigger to revoke access.

## Command policy in multi-host runs

The **Multi-host run** page and the Ops console matrix show a `RO` badge for
hosts marked `read-only` / `readonly` through their existing environment, group
or tag metadata. Read-only hosts accept inspection commands only; mutating and
destructive commands are rejected by the backend policy. The selection banner
lists those hosts and can remove them from the run before retrying.

Commands that include production hosts open a review dialog before execution.
Destructive commands require the explicit production phrase shown in the dialog;
the reviewed execution path is sent to the backend only after that confirmation.
If a host policy rejects a command, the result stays visible in the host result
row and the policy banner explains whether to choose an inspection command,
remove a read-only host, or review and retry the production command.

Broadcast mode also shows the number of read-only panes in its `RO` badge. A
destructive command is held before Enter reaches any pane, and the approval
dialog lists both production and read-only hosts in the broadcast scope.

## Team roles and shared sessions

The runner checks every request. Viewer and operator grants include explicit host
scope and expire within 30 days. Administrators can manage all hosts and settings.

| Role | View scoped hosts/jobs/monitors | Run jobs | Manage grants/configuration |
| --- | --- | --- | --- |
| Viewer | Yes | No | No |
| Operator | Yes | Yes | No |
| Administrator | All hosts | Yes | Yes |

Shared SSH sessions have separate observer and peer membership. Observers receive
output; peers with an operator/admin grant can type. Session owners and admins
manage invitations and closure. Viewer grants cannot become peers. Revocation and
expiry block subsequent requests, and sessions end when their owner's access
expires or after eight hours. Output retains a bounded 2 MiB live buffer and
reports when earlier output was dropped. Sessions do not survive runner restart.
Audit records identify grant, job, trigger, session and contributor actions without
recording typed terminal input or tokens.

## Helm

**Check tools** verifies Helm availability on the selected local or SSH machine.
Helm workflows also require a POSIX shell, `mktemp` and `sha256sum`. Load releases,
inspect values and history, and select a release. Choose an explicit Kubernetes
context before reviewing mutations; the release fixes the namespace.

Upgrade requires an exact chart version. Blacknode downloads the chart into a
temporary directory, hashes it, and runs a server dry run with secret manifests
hidden. Execution rechecks the release revision and artifact hash. It uses the
same archive and automatic rollback on failure. A Helm version supporting
`--dry-run=server`, `--hide-secret`, and atomic/rollback-on-failure upgrades is
required. Rollback previews the chosen revision and rejects changed release
history. Reviews expire after ten minutes and are single-use; chart artifacts are
removed on execution, cancellation, expiry or shutdown.

## AWS Session Manager

Install AWS CLI and the AWS Session Manager plugin on the desktop machine. The UI
shows which dependency is missing. Create a profile with an instance or managed-node
ID, region, optional AWS profile and environment. **Check AWS identity** uses STS;
**Connect saved profile** runs `aws ssm start-session` in a local PTY. Input, resize
and disconnection follow the interactive terminal lifecycle.

Credentials stay in the AWS provider chain. Sign in using your existing AWS/SSO
workflow; no AWS access keys are copied into Blacknode profiles. The target needs
SSM Agent/connectivity and your identity needs the appropriate Session Manager
permissions. Production profiles show a confirmation before connecting.

## Development verification

Tests use temporary databases, loopback HTTP/SSH fixtures, an in-memory SFTP
server, local Git repositories and mocked Helm/Trivy/AWS executables. They do not
use saved infrastructure profiles or production credentials.

```bash
go test ./internal/... ./cmd/blacknode-runner
go test -race ./internal/service -run 'TestInfrastructure|TestRunner'
cd frontend
npm run check
npm run check:test
npm test
npm run build
```

`frontend/scripts/infrastructure-smoke.mjs` uses Chrome's debugging protocol.
Start a server build with **temporary** `XDG_DATA_HOME` and `XDG_CONFIG_HOME`, a Vite
frontend on port 9248, and `FRONTEND_DEVSERVER_URL=http://127.0.0.1:9248` on the
backend at `127.0.0.1:9247`. Use an isolated Chrome profile with debugging port
9333, then run `node frontend/scripts/infrastructure-smoke.mjs` from the repository
root. The script checks both themes at 800/1280 pixels, exercises UI workflows,
and writes a report and screenshot under `/tmp`. Cloud/scan/sync/Helm mutations
are mocked in this browser test. Never point it at a personal database.
