content: <div align="center">
  <img src="frontend/public/icon.svg" alt="blacknode" width="84" height="84" />

  <h1>blacknode</h1>

  <p><strong>A unified SSH client and infrastructure command platform for DevOps engineers.</strong></p>
  <p>Terminal, multi-host execution, SFTP, observability, container & process management, network diagnostics, and an AI assistant — all in one desktop app.</p>

  <p>
    <img src="https://img.shields.io/badge/status-alpha-orange" alt="alpha" />
    <img src="https://img.shields.io/badge/wails-v3.0.0--alpha-cyan" alt="wails v3 alpha" />
    <img src="https://img.shields.io/badge/go-1.26-00ADD8" alt="go 1.26" />
    <img src="https://img.shields.io/badge/svelte-5-FF3E00" alt="svelte 5" />
    <img src="https://img.shields.io/badge/tailwind-4-38BDF8" alt="tailwind 4" />
    <img src="https://img.shields.io/badge/license-MIT-green" alt="license mit" />
  </p>
</div>

---

## What is it?

A desktop SSH platform that consolidates the dozen things a DevOps engineer
juggles across separate tools. Built as a single Go + Wails v3 binary; no
agent, no cloud account, no telemetry. Your hosts, keys, and recordings live
on disk in `~/.local/share/blacknode/` (Linux/macOS) or
`%LOCALAPPDATA%\blacknode\` (Windows).

## Status

**Alpha.** Built rapidly across many iterations. Core flows work; rough edges
exist. The Wails v3 framework itself is alpha, so expect churn there too. See
[Caveats](#caveats) below for the honest list.

## Features

### Terminal & connectivity
- **SSH** — password, public key, and `ssh-agent` auth; TOFU `known_hosts`
  verification; pooled clients shared across services.
- **Local PTY** by default — every new tab/pane opens a real local shell
  (PowerShell on Windows, `$SHELL` on Unix); switch any pane to remote
  with one click.
- **Tabs + recursive split panes** — horizontal/vertical splits, drag to
  resize, double-click divider to reset 50/50.
- **Multi-cursor broadcast** — keystrokes typed in any pane in the
  broadcast group fan out to every other group member. Master toggle in
  the top bar; per-pane opt-in.
- **Port forwarding** — local, remote, and dynamic SOCKS5 (hand-rolled,
  no-auth) tunnels persisted in the DB; one-click presets for Postgres /
  MySQL / Redis / SOCKS.
- **xterm.js** rendering with proper ANSI color, scrollback, JetBrains
  Mono Variable bundled for offline use.

### Host management
- SQLite-backed registry; groups, tags, environment tagging
  (`dev / staging / production`).
- Production hosts get a colored stripe in the sidebar, a connect-time
  confirmation, and a persistent red `PRODUCTION SESSION` strip across
  the top of the terminal.

### Multi-host execution
- Bounded worker pool (16 concurrent), exponential-backoff retry on
  dial failure, per-host streamed results.
- **Dangerous-command detector** — regex pattern list catches `rm -rf /`,
  `mkfs`, `dd of=/dev/sd*`, fork bombs, etc. Two severity levels: warn
  (proceed button) and block-without-confirm (must type a phrase).
- Production hosts in scope escalate confirmation severity automatically.

### Observability
- **Metrics** — CPU / memory / disk per host via `/proc` + `df` over SSH;
  live sparklines; threshold alerts at 90% with 5-minute debounce per
  (host, metric).
- **Logs** — multi-host live tail, regex / substring filter, pause /
  resume, color-coded per host. Save (command + host set + filter) as a
  named query for one-click recall.
- **Session recording** — every interactive session captured as
  asciinema cast v2 to disk. Output-only (passwords typed at sudo prompts
  never hit disk by design). Search across all recordings; in-app
  playback at 0.5×–8× with seek.

### Files
- **SFTP** browser with drag-and-drop upload, multi-format size display,
  recursive directory navigation.
- **In-app file editor** — CodeMirror 6 with the One Dark theme,
  auto-language detection (JSON / YAML / JS-TS / Python / Markdown /
  HTML / CSS / SQL / XML), `⌘S` save, dirty-state guard, binary heuristic.

### DevOps
- **Containers** — Docker `ps` / `logs` and `kubectl get pods` /
  `logs`, all SSH-driven. No local docker/kubectl install needed; runs
  on whatever the host has.
- **Process manager** — `top`-style sortable table with per-row kill
  (TERM/HUP/KILL/INT, optional sudo); systemd unit list with start /
  stop / restart / status actions.
- **Network diagnostics** — Ping, DNS lookup (dig→host→nslookup
  fallback), 32-way concurrent port scan via the SSH tunnel with banner
  grab, SSL certificate inspector with chain + expiry coloring.

### Productivity
- **AI assistant** — natural language → command (Claude Haiku 4.5,
  one-shot) and pasted output / log explanation (Claude Sonnet 4.6,
  streaming). Prompt caching on system prompts. API key encrypted at
  rest with the vault.
- **Snippets** — saved command templates with `{{name}}` and
  `{{name|default}}` variable substitution. Apply dialog with live
  preview. Snippets are searchable in the command palette.
- **Command history** — every multi-host run, AI insert, and snippet
  apply is captured automatically. Searchable, filterable by host /
  source, one-click re-insert.
- **Command palette** — `⌘K` / `Ctrl+K` from anywhere; jumps between
  panels, hosts, opens AI, applies snippets, locks vault.
- **Notifications** — desktop notifications (cross-platform via
  `gen2brain/beeep`), in-app toasts (always), and JSON webhook (POST,
  optional). Auto-fires on long multi-host runs and 90%+ metric breaches.

### Security
- **Vault** — Argon2id KDF (3 iterations, 64 MiB, 4 lanes) → AES-256-GCM
  for SSH private keys and the Anthropic API key. Master key only in
  memory, zeroed on lock.
- **Auto-lock** — configurable idle timeout, vault locks itself and
  emits an event the UI listens for.
- **Connection pool** with keepalive probe and 5-minute idle TTL; clients
  are reused across SFTP, exec, metrics, logs, and port forwards.

## Quick start

### Run from source

Prerequisites: Go 1.26+, Node 22+, [Wails v3 CLI](https://v3.wails.io/),
[Task](https://taskfile.dev/) (optional, recommended).

```bash
git clone https://github.com/<you>/blacknode
cd blacknode
wails3 dev
```

### Build a Windows .exe

```bash
task windows:build      # produces bin/blacknode.exe (~23 MB, GUI subsystem)
task windows:package    # NSIS installer (requires `choco install nsis`)
```

### Build for Linux / macOS

```bash
task linux:build        # bin/blacknode (Linux)
task darwin:build       # bin/blacknode (macOS)
```

### Run tests

```bash
go test ./internal/...                                # vault, store, recorder
cd frontend && npx svelte-check --tsconfig ./tsconfig.json
```

## First-time setup

1. Launch the app — you'll be prompted to create a vault passphrase.
   This encrypts SSH keys and API tokens at rest. There is no recovery —
   write it down.
2. Add a host in the sidebar (`+` button). Pick auth method; if `key`,
   generate or import one in the **Keys** panel first.
3. *(Optional)* Add an Anthropic API key in **Settings** to enable the AI
   drawer (`⌘I`).
4. Click your host → **Connect** in the active terminal pane.

## Architecture

```
blacknode/
├── main.go                 application bootstrap, service registration
├── *service.go             one Wails-bound service per file (SSH, SFTP,
│                           AI, Container, Network, Process, etc.)
├── internal/
│   ├── db/                 SQLite + schema + migrations
│   ├── store/              repos: hosts, keys, snippets, history,
│   │                       recordings, log queries, port forwards
│   ├── vault/              Argon2id + AES-256-GCM
│   ├── sshconn/            shared dialer + connection pool
│   └── recorder/           asciinema cast v2 writer + searcher
├── frontend/
│   ├── src/
│   │   ├── App.svelte      → VaultGate → Workspace
│   │   └── lib/            14 panels + Pane / Terminal / Toaster /
│   │                       Palette / RemoteEditor / etc.
│   └── public/icon.svg     branded mark
├── build/
│   ├── appicon.png         1024x1024 source for icon generation
│   ├── windows/icon.ico    multi-size .ico baked into the exe
│   └── ...                 Wails per-platform Taskfiles
└── cmd/icongen/            SVG → PNG renderer (oksvg + rasterx)
```

### How a feature is wired

Every visible feature follows the same shape:

1. **Go service** with public methods → declared in `main.go` as
   `application.NewService(...)`.
2. **Wails generates TypeScript bindings** at `frontend/bindings/...`.
3. **Svelte panel** imports the binding and calls methods directly.
4. **Long-running operations** emit typed events (`metrics:update`,
   `logs:line`, `notification:toast`) that the panel listens to via
   `Events.On`.

For a new feature: write the Go service, run `wails3 generate bindings`,
write the panel, register the view in `state.svelte.ts` and the nav
entry in `Workspace.svelte`. Every existing panel is a working template.

## Stack

| Layer | Tech |
| --- | --- |
| Desktop runtime | Wails v3 (alpha) |
| Backend | Go 1.26 |
| SSH | `golang.org/x/crypto/ssh`, `pkg/sftp`, `aymanbagabas/go-pty` |
| Storage | SQLite (`modernc.org/sqlite`, no CGo) + on-disk cast files |
| Crypto | Argon2id + AES-256-GCM (`crypto/aes`, `crypto/cipher`, `crypto/x/crypto/argon2`) |
| Frontend | Svelte 5 (runes), Tailwind v4, `@tailwindcss/vite` |
| Terminal | xterm.js + addon-fit + addon-web-links |
| Editor | CodeMirror 6 + per-language packs |
| AI | `anthropic-sdk-go` — Claude Haiku 4.5 + Sonnet 4.6 with prompt caching |
| Notifications | `gen2brain/beeep` (cross-platform) |
| Icons | `@lucide/svelte` |
| Fonts | Inter Variable + JetBrains Mono Variable (bundled) |

## Caveats

The honest list — features either intentionally narrow or straight-up unfinished.

- **Host-key TOFU is silent on first connect.** Defends against passive
  eavesdropping, not against an active MITM during the first-ever
  connect to a host. Worth adding a confirmation dialog before v1.
- **Metrics command is Linux-only** (`/proc` + `df`). macOS, BSD, and
  Windows hosts will fail.
- **Session recording is output-only by design.** Stdin (passwords typed
  at `sudo` prompts) is intentionally not captured. Some sensitive
  *output* still ends up in recordings — `cat ~/.ssh/id_rsa`, `env`,
  etc. Treat recordings as sensitive.
- **SFTP loads whole files** (50 MB cap) for both download and the
  in-app editor. Larger files need a streaming path.
- **Multi-host exec on password-auth hosts** uses cached passwords from
  prior interactive sessions. Hosts you've never connected interactively
  to will fail with empty password.
- **Vault holds the master key in memory** until lock or app exit.
- **`wails3 generate icons` requires the source PNG;** the
  `cmd/icongen` tool re-rasterizes from the SVG when you change the
  brand mark.
- **NSIS installer is optional** — production .exe is a single-file
  self-contained binary, no installer required to run it.
- **Tests are sparse** — vault crypto, known-hosts mismatch, hosts
  store, recorder, port forwards. Most services have no test coverage.
- **Wails v3 is in alpha-74**. APIs may move; migration churn likely
  before v3 GA.

## Roadmap

Done from the original product spec:

- Phase 0 — foundation, build system
- Phase 1 — security & identity (vault, key auth, agent, TOFU)
- Phase 2 — terminal system (tabs, splits, drag-resize, command palette)
- Phase 3 — host management
- Phase 4 — AI operations layer
- Phase 5 — multi-host execution (worker pool, retry)
- Phase 6 — observability dashboard (CPU/MEM/DISK, threshold alerts)
- Phase 7 — log system (streaming, filter, saved queries)
- Phase 8 — file transfer (SFTP + in-app editor)
- Phase 9 — automation (snippets with var substitution)
- Phase 14 — notifications (desktop, in-app, webhook)
- Phase 16 — release engineering (Windows packaging)

Plus: session recording, port forwards, container management, network
diagnostics, process manager, command history, multi-cursor broadcast,
custom branded icon.

Not done (with reasons):

- **Phase 10 — automation playbooks (multi-step, scheduled)** — design
  decision pending.
- **Phase 11 — plugin system (sandbox, SDK)** — needs an architectural
  call on isolation model (WASM? subprocess + capability tokens?).
- **Phase 12 — cloud sync** — needs a sync server. Out of scope for a
  local-only app.
- **Phase 13 — team features (RBAC, shared vault)** — needs a backend
  service.
- **Phase 15 — comprehensive testing** — sparse today.
- **Light theme** — design pass needed.
- **Auto-update** — needs a release server.
- **Network stats in metrics** — `rx/tx` bytes, not yet collected.
- **macOS metrics** — would need a `vm_stat` / `iostat` collector.

## Privacy

- No telemetry. No analytics. No phone-home.
- Only outbound traffic is direct SSH to your hosts and (if configured)
  Anthropic API calls and webhook POSTs.
- All credentials encrypted at rest with the vault. Master key never
  leaves memory.

## License

MIT — see [LICENSE](LICENSE).

## Acknowledgements

- [Wails v3](https://v3.wails.io/) for the desktop runtime.
- [xterm.js](https://xtermjs.org/), [CodeMirror 6](https://codemirror.net/),
  [Lucide](https://lucide.dev/) for the UI primitives.
- [asciinema](https://asciinema.org/) for the cast v2 format.
- [Anthropic](https://www.anthropic.com/) for the Claude API.


