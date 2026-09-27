## Summary

Brings Blacknode to feature parity with Termius across security, fleet management,
and operations, plus an operations console for scripted multi-host work. All
features are built without new module dependencies (stdlib HTTP + hand-rolled
AWS SigV4) so the sandboxed/offline build stays reproducible.

## Features

**Security**
- **FIDO2 / hardware security keys** — register `sk-ssh-ed25519@openssh.com` and
  `sk-ecdsa-sha2-nistp256@openssh.com` public keys; the dialer filters agent
  identities to the matching key so a touch authorizes the right credential. No
  private material is ever stored for hardware keys.
- **Session-scoped vault PIN** — a short numeric PIN re-unlocks the vault within a
  session. The PIN-wrapped key lives in memory only; nothing is written to disk,
  so there is no offline artifact to brute-force. (This is a convenience re-unlock,
  not biometrics — see README for the rationale.)

**Fleet management**
- **Per-host & per-group environment variables** — validated names
  (`^[A-Za-z_][A-Za-z0-9_]*$`, the shell-injection boundary), rendered as an
  `export` prelude at connect time.
- **Group config inheritance** — a host inherits any field it leaves empty from
  its group (host always wins per-field; agent forwarding is one-way ON only;
  env vars merge group-first with host override by name).
- **Cloud host import** — read-only discovery from AWS EC2 (SigV4 +
  DescribeInstances), DigitalOcean, and Azure (OAuth2 + Resource Graph). Discover
  → review → import; credentials are used for the single call and never stored.
- **Platform detection & OS badges** — host-list badges with collapsible groups.

**Operations**
- **Ops console / runbook DAGs / command blocks / focus mode** — scripted
  multi-host execution with per-block timing.

## Testing

- `gofmt` clean; `go vet ./...` clean; `go test -race ./internal/...` all
  packages pass; `go build .` links a working binary.
- Frontend: 123/123 unit tests pass; `svelte-check` 0 errors / 0 warnings (4068
  files); production `vite build` succeeds.
- **CI** — `.github/workflows/test.yml` is added by this branch, so this PR is
  its first run. Its `go` job needed a frontend build step: `main.go` carries
  `//go:embed all:frontend/dist` and that directory is gitignored, so `go build .`
  failed with `pattern all:frontend/dist: no matching files found`. Building the
  real production bundle there (rather than stubbing the directory) also makes it
  the one place CI verifies `vite build` still works — the frontend job
  type-checks and tests but never builds. Whole job replayed green in a pristine
  checkout.
- **Merge with `origin/main` verified.** `main` has since gained the #1 merge,
  which touches files this branch also changes (the generated service bindings,
  `HostEditor.svelte`, `KeysPanel.svelte`, `README.md`, `TERMIUS_PARITY.md`).
  Merging it in is conflict-free, and the merged tree was re-checked green on
  every item above — so no rebase is needed.

## Caveats

- **Cloud import is verified against recorded provider responses, not live
  endpoints** (no network egress in the build sandbox). Azure's Resource Graph KQL
  query is the least-certain path; first live run is the true integration test.
- Pre-existing and unrelated to this branch: `go build ./...` fails at link time
  because `build/ios/app_options_default.go` declares `package main` with no
  `func main` (Wails iOS scaffolding). `go build .`, `go vet ./...` and
  `go test ./...` are unaffected.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
