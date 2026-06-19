# Blacknode → Termius Parity: Look & Feature Plan

> Goal: make Blacknode **look** and **feel** like a modern, Termius-class SSH client
> (clean, approachable, professional) while keeping the depth Blacknode already has.
>
> Reference: <https://termius.com/> · 2025 desktop redesign: <https://termius.com/blog/termius-x>
>
> Status: **DRAFT for review** — no code changed yet. Created 2026-06-14.

---

## 0. TL;DR

**Blacknode already has more raw features than Termius.** It ships containers/k8s, a DB
client, an HTTP client, session recordings, network scan, topology, a plugin system, an AI
assistant, cross-device sync, known-hosts, jump hosts, tags, and keystroke broadcast — most
of which Termius does *not* have.

So this is **not primarily a feature-catch-up**. The gap to Termius is in three areas:

1. **Visual language** — Blacknode is "Phosphor Noir" (neon-green CRT/hacker aesthetic).
   Termius is clean, neutral, calm, and professional. This is the biggest perceived gap.
2. **Information architecture** — Termius organizes data as **Vaults → Groups → Hosts**
   with nested groups, host counts, and tag chips. Blacknode has a single vault and a flat
   `group` string.
3. **A few signature UX patterns** — a rich **host detail panel**, **one-click connect**
   surface, **group counts**, **tag chips**, and the **NEW HOST / TERMINAL / SERIAL** entry
   flow.

The plan below is sequenced so the highest-visual-impact, lowest-risk work lands first.

---

## 1. What Termius actually is (reference analysis)

### 1.1 Design language
- Clean, **minimal, neutral** palette — light and dark, but professional, not neon.
- Generous whitespace; calm typography; restrained accent color.
- Approachable to SSH newcomers: "adding hosts, managing keys, launching terminals feel
  seamless." The UI hides complexity (Keychain / Known Hosts tucked into settings for years).

### 1.2 Information architecture: **Vaults → Groups → Hosts**
- **Vaults** — top level, for *access control*. End-to-end encrypted. Every account has a
  **Personal vault**; teams get a shared **Team vault**; you can add more vaults to separate
  environments. A vault holds Hosts, Groups, Keys, Snippets, Port-forwarding rules, Known Hosts.
- **Groups** — collections of hosts that share something (customer, location, environment).
  **Nestable** to mirror infra topology. Groups carry **protocol settings inherited** by
  their hosts. Shown with **host counts** ("AWS — 92 Hosts").
- **Hosts** — defined by address + metadata (**labels, tags**). Each host can hold multiple
  protocols (SSH w/ port + creds + jump host; Telnet; Serial). Tags + groups + search make
  large fleets navigable.

### 1.3 Signature UX (2025 desktop redesign)
- **Horizontal tabs** for top-level nav (Vaults / SFTP / connections) + a *minimalist*
  sidebar — they explicitly moved *away* from the old vertical sidebar to free terminal space.
- **Command palette (⌘K)** is central: `⌘K` search, `⌘J` jump between tabs, `⌘T` new
  connection. Keyboard-first.
- **Host detail / connection panel**: Address, Port, "Credentials from" (vault), Username,
  Password / key, **Connect** button. One-click connect — nothing re-typed.
- **NEW HOST / TERMINAL / SERIAL** quick entry workflow.
- **Terminal side panel**: themes + command history + snippets.
- Workspace persistence ("restore your workspace exactly as you left it").

### 1.4 Termius features (for the matrix below)
SSH · Mosh · SFTP (drag/drop) · Telnet · Serial · Port forwarding · Snippets (+ AI) ·
Autocomplete/IntelliSense · Command history · Keychain · Identities · Known Hosts ·
Jump hosts/host-chaining · SSH agent forwarding · Vaults (E2E encrypted) · Groups (nested,
inherited settings) · Tags/labels · Teams & sharing · Workspaces (focus/split/broadcast/
templates) · Session logs (synced, bookmarks) · Sync across devices · SSH ID (passkey) ·
Post-quantum crypto · Terminal themes · Terminal side panel.

> Sources: <https://termius.com/> + <https://docs.termius.com/> (docs add Mosh, Identities,
> Workspaces-as-templates, autocomplete sources, and synced session logs not surfaced on the
> marketing site). Checked 2026-06-19.

---

## 2. Blacknode current state

### 2.1 Layout (today)
```
┌──────────────────────────────────────────────────────────────┐
│ TOP BAR  [logo] /view            [Cast] [AI] [Palette ⌘K] [Lock]│
├────┬───────────────┬─────────────────────────────────────────┤
│ 44 │  HOST LIST    │  MAIN PANEL (router)                     │
│ px │  (sidebar,    │  terminals: TabBar + split panes         │
│icon│   resizable)  │  …or Metrics/Logs/Containers/HTTP/etc.   │  + AI
│rail│               │                                          │  drawer
├────┴───────────────┴─────────────────────────────────────────┤
│ STATUS BAR   hosts · tabs · panes                              │
└──────────────────────────────────────────────────────────────┘
```
- **Vertical icon NavRail** (44px) = Termius's *old* (pre-2025) paradigm.
- Resizable **HostList** sidebar: search + group dividers + host rows (mono font, neon).
- Main area routes 20+ panels. Terminals view has tabs + recursive pane splitting.
- **Command palette** (⌘K), **AI drawer** (⌘I), **Cast/broadcast**, vault lock — all present.

### 2.2 Host model (already rich)
`internal/store/models.ts → Host`: `name, host, port, username, authMethod, keyID,
group?, environment?, proxyJump?, tags[], notes?, createdAt, updatedAt, lastConnectedAt`.

→ Tags, a single-level group string, jump host, and environment **already exist in the model.**

### 2.3 Backend service surface (already broad)
`hostservice, sshservice, sftpservice, portforwardservice, snippetservice, keyservice,
vaultservice, syncservice, knownhosts (store), execservice, metricsservice, logsservice,
containerservice, networkservice, processservice, httpservice, dbservice, recordingservice,
activityservice, historyservice, pluginservice, aiservice, autolockservice, notificationservice,
updateservice, localshellservice`.

---

## 3. Feature parity matrix

| Capability | Termius | Blacknode | Gap |
|---|:---:|:---:|---|
| SSH terminal + tabs | ✅ | ✅ | — |
| Split panes / tiling | ⚠️ | ✅ (recursive + tile-all) | Blacknode ahead |
| SFTP / file transfer | ✅ | ✅ (Files + RemoteEditor) | — |
| Port forwarding | ✅ | ✅ (Forwards) | — |
| Snippets | ✅ | ✅ | — |
| Command history | ✅ | ✅ (History) | — |
| AI in terminal (NL→cmd) | ✅ | ✅ (AI drawer, ⌘I) | Blacknode ahead (also explains output) |
| Inline autocomplete / IntelliSense | ✅ (history/path/snippet/sudo) | ✅ (`autocompleteservice` + `AutocompletePopup`) | ≈ parity (no sudo-pw source) |
| Keychain / SSH keys | ✅ | ✅ (Keys) | — |
| Identities (named credential bundles) | ✅ | ❌ (keys only) | minor; no Identity abstraction |
| Known hosts | ✅ | ✅ (`store/knownhosts`) | not surfaced in UI |
| Jump host / chaining | ✅ | ✅ (`proxyJump`) | UI is single field |
| Mosh | ✅ | ✅ (`moshservice`, wraps mosh-client) | ≈ parity |
| Client certificates (SSH) | ✅ | ⚠️ (key/password/agent only) | minor |
| Workspaces / focus / split / broadcast | ✅ | ✅ (tabs + recursive splits + multi-cursor) | ≈ parity |
| Workspace templates (reusable) | ✅ | ❌ | gap — no saved layouts |
| Unified terminal side panel (themes+history+snippets) | ✅ | ✅ (`TerminalSidePanel`) | ≈ parity |
| Session logs (synced, team-shared, bookmarks) | ✅ | ✅ recordings (local only) | local parity; no sharing |
| Auto/background sync | ✅ | ✅ (`syncservice_autosync`, conflict preview, sync-on-unlock) | ≈ parity |
| Vault (E2E encrypted) | ✅ | ✅ (single vault) | **no multi-vault hierarchy** |
| Groups | ✅ nested | ⚠️ flat string | **nesting + counts + inherited settings** |
| Tags / labels | ✅ chips | ⚠️ model only | **not rendered/filterable** |
| Sync across devices | ✅ | ✅ (`syncservice`) | — |
| Serial / Telnet | ✅ | ❌ | minor; out of scope for v1 |
| Teams / multiplayer | ✅ | ❌ | out of scope (local-first app) |
| Containers / k8s | ❌ | ✅ | **Blacknode ahead** |
| DB client | ❌ | ✅ | **Blacknode ahead** |
| HTTP client | ❌ | ✅ | **Blacknode ahead** |
| Session recordings | ❌ | ✅ | **Blacknode ahead** |
| Network scan / topology | ❌ | ✅ | **Blacknode ahead** |
| Plugins | ❌ | ✅ | **Blacknode ahead** |
| Metrics / processes | ⚠️ | ✅ | **Blacknode ahead** |

**Conclusion (verified against HEAD `a85f525`, 2026-06-19):** after the latest commit (Mosh,
autocomplete, background sync), the terminal-UX gap is essentially **closed** — inline
autocomplete, Mosh, the unified side panel, and auto-sync all ship. Remaining gaps are almost
entirely **account/collaboration infrastructure**: multi-vault hierarchy, nested groups with
inherited settings, real team sharing (only a `team_activity` store stub exists today), and
synced/shared session logs — all of which need a sync-server/account backend and are deferred.
Smaller leftovers: Identities abstraction, workspace templates, SSH client certs, Serial/Telnet.
Everywhere else Blacknode is a **superset**. The real remaining gap is **visual identity + IA
polish**, not capability.

---

## 4. The core decision: how "Termius-like" should the look be?

This is the one question that changes everything downstream. Three options:

- **A — New default theme "Termius-clean":** add a calm, neutral, professional theme
  (light + dark) as the *new default*, keep "Phosphor Noir" as an opt-in theme. Lowest risk,
  preserves identity, biggest visual payoff. **Recommended.**
- **B — Full reskin:** replace Phosphor Noir entirely. Highest impact, loses Blacknode's
  identity, largest diff.
- **C — IA/UX only:** keep the neon look, only adopt Termius's *structure* (vaults, groups,
  host panel). Smallest visual change, doesn't really "look like Termius."

The rest of this plan assumes **Option A** unless decided otherwise.

---

## 5. Proposed work, phased

### Phase 1 — Visual language (highest impact)  ▸ token + theme work
*Builds directly on the typography pass already completed (6-step scale, `type-*` tokens).*
- Add a **"Termius-clean" theme** in `app.css`: neutral surfaces (slate/zinc), a single
  restrained accent (Termius-blue), softer hairlines, remove CRT scanline + phosphor flicker
  for this theme.
- Switch UI chrome from **mono → Inter** for host names/labels (reserve mono for terminal,
  code, paths — the scale already intends this).
- Soften the neon accents: status dots, selection glows, focus rings → calm equivalents.
- Slightly increase row padding / whitespace to match Termius's calmer density.
- **Files:** `frontend/src/app.css`, `HostList.svelte`, `NavRail.svelte`, `Workspace.svelte`,
  `StatusBar.svelte`.

### Phase 2 — Host list & detail panel (signature UX)
- **Group headers with host counts**: "AWS · 12" (counts already derivable in `HostList`).
- **Tag chips** on host rows + **filter by tag** (tags already in model; wire into the
  existing `visible` filter and add clickable chips).
- **Host detail panel**: clicking a host opens a Termius-style detail surface (Address, Port,
  Credentials/vault, Username, auth, jump host, tags, notes) with a prominent **Connect**
  button — instead of jumping straight to edit. Reuse/extend `HostEditor.svelte`.
- **Quick actions**: Connect · SFTP · Edit · Duplicate · Delete on each host.
- **Files:** `HostList.svelte`, `HostEditor.svelte` (→ split a read-only `HostDetail.svelte`),
  `state.svelte.ts`.

### Phase 3 — Information architecture: Vaults → Groups → Hosts
- **Nested groups**: allow `group` to be a path ("AWS/Prod/db") or add a `parentGroup` —
  render as a collapsible tree with counts. (Model/store change — backend touch.)
- **Multiple vaults** (stretch): extend `vaultservice` + UI to list/switch vaults
  (Personal + named). Larger backend change; can defer.
- **Inherited connection settings** at the group level (proxy jump, key, username defaults).
- **Files:** `internal/store/models.go`, `internal/service/hostservice.go`,
  `internal/service/vaultservice.go`, `HostList.svelte`, `VaultPanel.svelte`.

### Phase 4 — Navigation & entry flow
- Evaluate **horizontal top tabs** (Termius 2025) vs. the current vertical icon rail. A
  hybrid is viable: keep the rail for power panels, add a Termius-style **NEW HOST /
  TERMINAL / SERIAL** entry button + an empty-state "Start connecting" hero.
- Strengthen **⌘K palette** as the primary connect surface (it already exists): connect to
  host, jump tab (⌘J), new connection (⌘T).
- **Known Hosts** management surfaced in Settings (data already exists in `store/knownhosts`).
- **Files:** `Workspace.svelte`, `NavRail.svelte`, `Palette.svelte`, `OnboardingCard.svelte`,
  `SettingsPanel.svelte`.

### Phase 5 — Polish
- Terminal side panel parity: themes + history + snippets in one place.
- Empty states, onboarding hero, micro-interactions tuned to Termius's calmer feel.
- Optional: Serial connection support (new `sshservice` sibling) — only if desired.

---

## 6. Out of scope (for now)
- **Teams / multiplayer / real-time collaboration** — Blacknode is a local-first desktop app;
  this is a server/account product surface.
- **SSH ID passkey / post-quantum crypto** — security R&D, separate track.
- **Web/mobile clients** — Termius is multi-platform; Blacknode is Wails desktop.

---

## 7. Suggested sequencing & effort

| Phase | Impact | Risk | Touch | Order |
|---|---|---|---|---|
| 1 Visual theme | ★★★★★ | low | frontend only | **1st** |
| 2 Host list + detail | ★★★★☆ | low | frontend only | 2nd |
| 4 Nav/entry + palette | ★★★☆☆ | low-med | frontend | 3rd |
| 3 Vaults/nested groups | ★★★☆☆ | med-high | **backend + FE** | 4th |
| 5 Polish | ★★☆☆☆ | low | frontend | last |

Phases 1, 2, 4, 5 are **frontend-only** and safe. Phase 3 is the only one that changes the
Go store/services and the data model — do it last, behind its own review.

---

## 8. Decisions (LOCKED 2026-06-14)
1. **Look: Option B — full reskin.** Replace Phosphor Noir entirely. No neon CRT aesthetic.
2. **Default mode: Dark (navy/charcoal).** Light (white/zinc) also fully polished.
   - Dark: `surface-0 #0b0e14 · 1 #11151d · 2 #161b24 · 3 #1d2430 · line #232a36 ·
     text-1 #e6e9ef · text-2 #9aa4b2`
   - Light: `surface-0 #ffffff · 1 #f7f8fa · 2 #eef0f4 · 3 #e3e7ed · line #dfe3ea ·
     text-1 #0f1722 · text-2 #4a5568`
3. **Accent: Termius blue** — `#3b82f6` (dark) / `#2563eb` (light). Used on Connect, focus
   ring, selection, active nav, links.
4. **Top nav: horizontal tabs (Termius 2025).** Restructure `Workspace.svelte` from the
   vertical 44px icon rail to horizontal top tabs + minimalist sidebar.
5. **Remove all neon effects:** CRT scanline background, `phosphor-flicker`, `glow-pulse`,
   accent box-shadows/glows. Replace with calm, flat, professional surfaces.
6. Nested groups / multi-vault (Phase 3): still **deferred** to last, behind its own review.

### Build order (Option B — revised after design review 2026-06-14)
Two external reviews converged on three corrections to the locked plan; all accepted:
- **Nav = HYBRID, not full horizontal tabs.** Blacknode has ~20 panels vs Termius's ~5;
  pure top-tabs hurts discoverability. Use a slim vertical **section** rail
  (Infrastructure / Operations / Tools / AI) + **horizontal tabs inside** each section.
- **Phosphor Noir deleted** (not kept). Default dark navy + light only.
- **Fold in high-value behavioral features now:** Favorites (★, new `favorite` field),
  Recent Connections (from existing `lastConnectedAt`), Quick Connect (⌘T → `user@host` /
  `ssh://…`), and a **`+ New` dropdown** (Host / Terminal / Local Shell / DB / HTTP) instead
  of Termius's SSH-only "NEW HOST / TERMINAL / SERIAL". Keep **AI first-class** (⌘I drawer).

**Waves & status:**
1. ✅ **Reskin** — `app.css` navy/blue, killed CRT/neon, recolored all glows, terminal palette.
2. ✅ **De-neon + host row redesign** — Inter for chrome, host row = status dot · name · env
   badge · auth icon / `user@host:port` (mono) / **tag chips** / metric bars; group headers
   with **counts**; tag-aware search; calm StatusBar + top bar.
3. ✅ **List features** — ⏱ **Recent** section (from `lastConnectedAt`) + ★ **Favorites**
   section. Backend: `favorite bool` on Go `Host` (`store/hosts.go`), idempotent DB migration
   (`db.go`), `SetFavorite` store + service methods, TS bindings regenerated (`wails3 generate
   bindings -ts`). UI: ★ toggle in the host action menu and the detail-panel header; pinned
   Favorites section atop the host list. Host store tests pass.
4. ✅ **Host detail panel** (`HostDetail.svelte`) — click host → rich object + big **Connect**
   + quick actions (Connect / SFTP / Edit / Duplicate / Delete). New `connect-host` bus event;
   Workspace opens a fresh tab and routes the session.
5. ✅ **Hybrid nav shell** — `NavRail` is now a slim **section rail** (Sessions / Monitor /
   Network / Workloads · Vault / Plugins / Settings); new `SectionTabs.svelte` = in-section
   horizontal tabs + **`+ New` dropdown** (Host / Terminal / Local shell / DB / HTTP). Plugin
   panels appear as tabs under the Plugins section. Per-section last-view memory.
6. ✅ **Quick Connect** (`Palette`) — parses `user@host[:port]` / `ssh://…`; reuses or creates
   a saved host then connects. Palette host entries now **connect** (not just select).
7. 🟡 **Polish — partial** (audited 2026-06-19 against HEAD `a85f525`):
   - ✅ Uppercase cleanup — done; only 1 stray badge remains (`SettingsPanel.svelte:797`, a
     `{c.kind}` connection-type chip — arguably intentional).
   - ✅ Onboarding hero — wired (`OnboardingCard` rendered at `Workspace.svelte:671`).
   - 🟡 **Empty states — 5 / 14 panels.** Have them: Containers, DB, Forwards, Processes, SFTP.
     **Missing:** History, Recordings, Snippets, Keys, Logs, Network, Activity, HTTP, Metrics.
   - 🟡 Mono sweep — `font-mono` still heavy in Settings (20), Processes/DB (13 each); much is
     legitimate (IPs, ports, hashes, paths) but data-table chrome could be reviewed.
8. *(deferred)* nested groups, group inheritance, multi-vault.

Wave 7 is the only open UI work, and it's **empty-state coverage** plus a light mono review —
no structural risk. All shipped waves pass `svelte-check` (0 errors) and `vite build`.

---

## 9. Missing features — verified against code (HEAD `a85f525`, 2026-06-19)

Everything Termius documents that Blacknode does **not** have, with the real status in code:

| Missing capability | Status in Blacknode | Bucket |
|---|---|---|
| **Multi-vault hierarchy** | Single local vault only | Account infra — deferred |
| **Nested groups + inherited settings** | Flat `group` string | IA — deferred |
| **Team sharing / RBAC / shared vault** | Only a `team_activity` store stub (`Record`/`Recent`); no UI, no RBAC | Account infra — deferred |
| **Synced / team-shared session logs + bookmarks** | Recordings are local-only | Account infra — deferred |
| **Identities** (named credential bundles) | Keys only; no Identity abstraction (the `IdentityFile` refs are SSH-config import) | Minor |
| **Serial / Telnet protocols** | Absent (the `Serial` refs are SSL-cert serial numbers) | Minor / out of scope |
| **SSH client certificates** | key / password / agent auth only | Minor |
| **Workspace templates** (reusable saved layouts) | Tabs + splits exist; not persistable as templates | Minor |
| **Known Hosts management UI** | Data lives in `store/knownhosts`; no settings surface | Minor — quick win |
| **Sudo-password autocomplete source** | Autocomplete pulls history/snippets/builtins only | Trivial |

**Two buckets:** (a) **account/collaboration infrastructure** — multi-vault, nested groups,
team sharing, synced logs — all need a sync-server/account backend and are correctly deferred;
(b) **minor/quick wins** — Identities, Serial/Telnet, client certs, workspace templates, and
surfacing Known Hosts in Settings (the data already exists). Nothing here is core SSH-client
capability; Blacknode remains a superset everywhere else.
