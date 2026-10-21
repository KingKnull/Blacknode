<script lang="ts">
  import Dialog from "./Dialog.svelte";
  import { getShortcuts, shortcutLabel, shortcutMatchesEvent, subscribeShortcuts } from "./shortcuts";
  import { onMount, tick } from "svelte";
  import { app, type View } from "./state.svelte";
  import {
    VaultService,
    SnippetService,
    HostService,
    HistoryService,
    RecordingService,
    PlatformService,
    InfrastructureService,
    RunbookService,
    ActivityService,
  } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Snippet, Host, HistoryEntry, Recording, OperationDocument } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import type { DeploymentProject, ScanProfile, ServiceWorkspace, VulnerabilityScan } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { boundedPaletteRecords, parsePaletteRecord, PALETTE_ACTIVITY_LIMIT, redactPaletteText } from "./paletteRecords";
  import { bus } from "./events";
  import { discardOperationTargetOutside, openOperationTarget } from "./operationSelection.svelte";
  import { matchingOperationID, type OperationTarget } from "./operationTargets";
  import {
    TerminalSquare,
    Server,
    Zap,
    Folder,
    Activity,
    KeyRound,
    ScrollText,
    Settings as SettingsIcon,
    Lock,
    Search,
    ShieldCheck,
    Network,
    Film,
    Boxes,
    Radar,
    Cpu,
    Globe2,
    Database,
    Bookmark,
    History as HistoryIcon,
    LayoutGrid,
    Radio,
    Plug,
    Share2,
    Puzzle,
  } from "@lucide/svelte";

  type Action = {
    id: string;
    label: string;
    hint?: string;
    icon: any;
    category: string;
    run: () => void | Promise<void>;
    keywords?: string;
  };

  type Props = {
    onNewTab: () => void;
  };
  let { onNewTab }: Props = $props();

  let input = $state("");
  let highlighted = $state(0);
  let shortcuts = $state(getShortcuts());

  // Keep the keyboard-highlighted row visible inside the scrolling list.
  async function scrollHighlightedIntoView() {
    await tick();
    document
      .querySelector(`[data-palette-idx="${highlighted}"]`)
      ?.scrollIntoView({ block: "nearest" });
  }
  let inputEl: HTMLInputElement | undefined = $state();
  let snippets = $state<Snippet[]>([]);
  let historyEntries = $state<HistoryEntry[]>([]);
  let recordings = $state<Recording[]>([]);
  let operationActions = $state<Action[]>([]);
  let operationRefreshID = 0;

  $effect(() => { discardOperationTargetOutside(app.view); });

  function recordValue<T extends Record<string, unknown>>(record: OperationDocument): T | null {
    return parsePaletteRecord<T>(record.body);
  }

  function navigateRecord(
    id: string,
    label: string,
    hint: string,
    icon: any,
    category: string,
    view: View,
    keywords = "",
    target?: OperationTarget,
  ): Action {
    return { id, label, hint, icon, category, keywords, run: () => {
      if (target) openOperationTarget(target.kind, target.id);
      else app.view = view;
    } };
  }

  function fulfilledArray<T>(result: PromiseSettledResult<T[]>): T[] {
    return result.status === "fulfilled" ? result.value ?? [] : [];
  }

  async function refreshOperationActions() {
    const refreshID = ++operationRefreshID;
    const results = await Promise.allSettled([
      PlatformService.ListRecords("workspace"),
      RunbookService.List(),
      PlatformService.ListRecords("incident"),
      PlatformService.ListRecords("project"),
      InfrastructureService.Monitors(),
      PlatformService.ListRecords("scan-profile"),
      PlatformService.ListRecords("vulnerability-scan"),
      ActivityService.List({ limit: PALETTE_ACTIVITY_LIMIT }),
    ]);
    if (refreshID !== operationRefreshID) return;

    const [workspaceResult, runbookResult, incidentResult, projectResult, monitorResult, profileResult, scanResult, activityResult] = results;
    const next: Action[] = [];
    for (const record of boundedPaletteRecords(fulfilledArray(workspaceResult))) {
      const workspace = recordValue<ServiceWorkspace & Record<string, unknown>>(record);
      if (!workspace || !matchingOperationID(record.id, workspace.id) || !workspace.name) continue;
      const name = redactPaletteText(workspace.name, 100);
      const environment = redactPaletteText(workspace.environment, 40);
      const hostCount = Array.isArray(workspace.hostIDs) ? workspace.hostIDs.length : 0;
      next.push({
        id: `record:workspace:${record.id}`,
        label: `Workspace: ${name}`,
        hint: `${environment || "workspace"} · ${hostCount} hosts`,
        icon: Share2,
        category: "Service workspaces",
        keywords: `${name} ${environment} ${redactPaletteText(workspace.kubeContext, 80)}`,
        // Workspace activation restores sessions and starts monitoring. Search only navigates.
        run: () => { app.view = "service-workspaces"; },
      });
    }

    for (const runbook of boundedPaletteRecords(fulfilledArray(runbookResult))) {
      if (!runbook?.id || !runbook.name) continue;
      const name = redactPaletteText(runbook.name, 100);
      const steps = Array.isArray(runbook.steps) ? runbook.steps.length : 0;
      next.push(navigateRecord(
        `record:runbook:${runbook.id}`,
        `Runbook: ${name}`,
        `${steps} steps`,
        Bookmark,
        "Runbooks",
        "runbooks",
        `${name} ${redactPaletteText(runbook.sourceRepository, 100)} ${redactPaletteText(runbook.workspaceID, 80)}`,
        { kind: "runbook", id: runbook.id },
      ));
    }

    for (const record of boundedPaletteRecords(fulfilledArray(incidentResult))) {
      const incident = recordValue<Record<string, unknown>>(record);
      if (!incident || !matchingOperationID(record.id, incident.id) || typeof incident.name !== "string") continue;
      const name = redactPaletteText(incident.name, 100);
      const status = redactPaletteText(incident.status, 40);
      next.push(navigateRecord(`record:incident:${record.id}`, `Incident: ${name}`, status || "incident", Activity, "Incidents", "incidents", `${name} ${status}`, { kind: "incident", id: record.id }));
    }

    for (const record of boundedPaletteRecords(fulfilledArray(projectResult))) {
      const project = recordValue<DeploymentProject & Record<string, unknown>>(record);
      if (!project || !matchingOperationID(record.id, project.id) || !project.name) continue;
      const name = redactPaletteText(project.name, 100);
      const tool = redactPaletteText(project.tool, 40);
      const repository = redactPaletteText(project.repository, 100);
      next.push(navigateRecord(`record:deployment:${record.id}`, `Deployment: ${name}`, `${tool || "project"}${repository ? ` · ${repository}` : ""}`, Zap, "Deployments", "deployments", `${name} ${tool} ${repository}`, { kind: "deployment", id: record.id }));
    }

    for (const monitor of boundedPaletteRecords(fulfilledArray(monitorResult))) {
      const serviceMonitor = monitor?.monitor;
      if (!serviceMonitor?.id || !serviceMonitor.name) continue;
      const name = redactPaletteText(serviceMonitor.name, 100);
      const target = redactPaletteText(serviceMonitor.target, 100);
      const latest = monitor.state?.samples?.at(-1);
      next.push(navigateRecord(`record:monitor:${serviceMonitor.id}`, `Monitor: ${name}`, `${redactPaletteText(serviceMonitor.kind, 30) || "check"}${target ? ` · ${target}` : ""}`, Activity, "Monitors", "uptime", `${name} ${target} ${redactPaletteText(latest?.status, 30)}`, { kind: "monitor", id: serviceMonitor.id }));
    }

    for (const record of boundedPaletteRecords(fulfilledArray(profileResult))) {
      const profile = recordValue<ScanProfile & Record<string, unknown>>(record);
      if (!profile || !matchingOperationID(record.id, profile.id) || !profile.name) continue;
      const name = redactPaletteText(profile.name, 100);
      const target = redactPaletteText(profile.target, 100);
      next.push(navigateRecord(`record:scan-profile:${record.id}`, `Scan profile: ${name}`, `${redactPaletteText(profile.kind, 30) || "scan"}${target ? ` · ${target}` : ""}`, ShieldCheck, "Security scans", "security-scans", `${name} ${target}`, { kind: "scan-profile", id: record.id }));
    }

    for (const record of boundedPaletteRecords(fulfilledArray(scanResult))) {
      const scan = recordValue<VulnerabilityScan & Record<string, unknown>>(record);
      const profile = scan?.profile;
      if (!scan || !matchingOperationID(record.id, scan.id) || !profile?.name) continue;
      const name = redactPaletteText(profile.name, 100);
      const findings = Array.isArray(scan.findings) ? scan.findings.length : 0;
      next.push(navigateRecord(`record:security-scan:${record.id}`, `Security scan: ${name}`, `${findings} findings`, ShieldCheck, "Security scans", "security-scans", `${name} ${redactPaletteText(profile.target, 100)}`, { kind: "security-scan", id: record.id }));
    }

    for (const activity of boundedPaletteRecords(fulfilledArray(activityResult), PALETTE_ACTIVITY_LIMIT)) {
      if (!activity?.id || !activity.title) continue;
      const title = redactPaletteText(activity.title, 100);
      const source = redactPaletteText(activity.source, 40);
      const kind = redactPaletteText(activity.kind, 80);
      next.push(navigateRecord(`record:activity:${activity.id}`, `Activity: ${title}`, `${source || "activity"}${kind ? ` · ${kind}` : ""}`, Activity, "Activity / audit", "activity", `${title} ${source} ${kind}`));
    }

    operationActions = next;
  }

  const VIEW_ACTIONS: { id: View; label: string; icon: any }[] = [
    { id: "uptime", label: "Go to Uptime monitoring", icon: Activity },
    { id: "directory-sync", label: "Go to Directory sync", icon: Folder },
    { id: "service-map", label: "Go to Service map", icon: Share2 },
    { id: "security-scans", label: "Go to Security scans", icon: ShieldCheck },
    { id: "git-runbooks", label: "Go to Git runbooks", icon: Bookmark },
    { id: "helm", label: "Go to Helm releases", icon: Boxes },
    { id: "ssm", label: "Go to AWS Session Manager", icon: TerminalSquare },
    { id: "service-workspaces", label: "Go to Service workspaces", icon: Share2 },
    { id: "kubernetes", label: "Go to Kubernetes", icon: Boxes },
    { id: "fleet", label: "Go to Fleet inventory", icon: Server },
    { id: "incidents", label: "Go to Incidents", icon: Activity },
    { id: "deployments", label: "Go to Deployments", icon: Zap },
    { id: "team", label: "Go to Team operations", icon: Share2 },
    { id: "terminals", label: "Go to Terminals", icon: TerminalSquare },
    { id: "exec", label: "Go to Multi-host", icon: Zap },
    { id: "ops", label: "Go to Ops Console", icon: ShieldCheck },
    { id: "runbooks", label: "Go to Runbooks", icon: Bookmark },
    { id: "files", label: "Go to Files", icon: Folder },
    { id: "metrics", label: "Go to Metrics", icon: Activity },
    { id: "logs", label: "Go to Logs", icon: ScrollText },
    { id: "forwards", label: "Go to Forwards", icon: Network },
    { id: "recordings", label: "Go to Recordings", icon: Film },
    { id: "containers", label: "Go to Containers", icon: Boxes },
    { id: "network", label: "Go to Network diagnostics", icon: Radar },
    { id: "processes", label: "Go to Processes", icon: Cpu },
    { id: "http", label: "Go to HTTP", icon: Globe2 },
    { id: "database", label: "Go to Database", icon: Database },
    { id: "snippets", label: "Go to Snippets", icon: Bookmark },
    { id: "history", label: "Go to History", icon: HistoryIcon },
    { id: "activity", label: "Go to Activity", icon: Activity },
    { id: "topology", label: "Go to Topology", icon: Share2 },
    { id: "plugins", label: "Go to Plugins", icon: Puzzle },
    { id: "keys", label: "Go to Keys", icon: KeyRound },
    { id: "vault", label: "Go to Vault", icon: Lock },
    { id: "settings", label: "Go to Settings", icon: SettingsIcon },
  ];

  let actions = $derived<Action[]>([
    ...VIEW_ACTIONS.map((v) => ({
      id: `view:${v.id}`,
      label: v.label,
      hint: "view",
      icon: v.icon,
      category: "Navigate",
      run: () => {
        app.view = v.id;
      },
    })),
    {
      id: "tab:new",
      label: "New terminal tab",
      hint: "⌘T · open a fresh local shell",
      icon: TerminalSquare,
      category: "Terminal",
      run: () => {
        app.view = "terminals";
        onNewTab();
      },
    },
    ...app.hosts.map(
      (h): Action => ({
        id: `host:${h.id}`,
        label: `Connect: ${h.name}`,
        hint: `${h.username}@${h.host}:${h.port || 22}`,
        icon: Server,
        category: "Hosts",
        keywords: `${h.host} ${h.username} ${h.group} ${h.alias ?? ""} ${(h.tags ?? []).join(" ")}`,
        run: () => {
          app.selectedHostID = h.id;
          bus.emit("connect-host", { hostID: h.id });
        },
      }),
    ),
    {
      id: "vault:lock",
      label: "Lock vault",
      hint: "clear master key from memory",
      icon: Lock,
      category: "System",
      run: async () => {
        app.suppressAutoUnlock = true;
        await VaultService.Lock();
        await app.refreshAll();
      },
    },
    {
      id: "cmd:focus",
      label: "Toggle focus mode",
      hint: "Ctrl+Shift+Z · terminal-only chrome",
      icon: LayoutGrid,
      category: "Terminal",
      keywords: "zen fullscreen focus hide chrome",
      run: () => bus.emit("toggle-focus-mode"),
    },
    {
      id: "cmd:tile",
      label: "Tile active hosts",
      hint: "grid split all connected sessions",
      icon: LayoutGrid,
      category: "Multi-host",
      keywords: "split grid pane tile layout",
      run: () => {
        bus.emit('tile-active-hosts');
        app.view = "terminals";
      },
    },
    {
      id: "cmd:broadcast",
      label: app.broadcastEnabled ? "Disable broadcast mode" : "Enable broadcast mode",
      hint: "type once, send to all panes",
      icon: Radio,
      category: "Multi-host",
      keywords: "cast multi broadcast",
      run: () => {
        if (!app.broadcastEnabled && app.broadcastSet.size === 0) {
          app.toast('warn', 'NO PANES IN BROADCAST', 'Click the \'cast\' button on each pane first.');
        }
        app.broadcastEnabled = !app.broadcastEnabled;
      },
    },
    ...historyEntries.slice(0, 200).map((entry): Action => ({
      id: `history:${entry.id}`,
      label: `Run: ${entry.command}`,
      hint: entry.hostName || "history",
      icon: HistoryIcon,
      category: "History",
      keywords: `${entry.command} ${entry.hostName ?? ""} ${entry.source}`,
      run: () => {
        bus.emit("insert-into-active-terminal", entry.command);
        app.view = "terminals";
      },
    })),
    ...recordings.slice(0, 200).map((recording): Action => ({
      id: `recording:${recording.id}`,
      label: `Recording: ${recording.title}`,
      hint: `${Math.round(recording.durationSeconds)}s`,
      icon: Film,
      category: "Recordings",
      keywords: recording.title,
      run: () => {
        app.view = "recordings";
      },
    })),
    ...snippets.map(
      (s): Action => ({
        id: `snippet:${s.id}`,
        label: `Snippet: ${s.name}`,
        hint: s.description || s.body.slice(0, 60),
        icon: Bookmark,
        category: "Snippets",
        keywords: `${s.body} ${(s.tags ?? []).join(" ")}`,
        run: async () => {
          const rendered = (await SnippetService.Apply(
            s.id,
            {},
            "",
            "",
            true,
          )) as string;
          bus.emit('insert-into-active-terminal', rendered);
          app.view = "terminals";
        },
      }),
    ),
    ...operationActions,
  ]);

  // ── Quick Connect ───────────────────────────────────────────────
  // Parse `user@host`, `user@host:port`, `ssh://user@host:port`, or a bare
  // `host` (when it looks like a domain/IP or carries a port) into a target.
  function parseConnect(raw: string): { user: string; host: string; port: number } | null {
    let s = raw.trim().replace(/^ssh:\/\//i, "");
    const m = s.match(/^(?:([^@\s]+)@)?([a-zA-Z0-9._-]+)(?::(\d+))?$/);
    if (!m) return null;
    const host = m[2];
    // Avoid matching ordinary search words: require an explicit user@, a dotted
    // host (domain/IP), or an explicit :port.
    if (!m[1] && !host.includes(".") && !m[3]) return null;
    return { user: m[1] || "root", host, port: m[3] ? parseInt(m[3], 10) : 22 };
  }

  async function quickConnect(p: { user: string; host: string; port: number }) {
    app.paletteOpen = false;
    // Reuse a matching saved host rather than creating duplicates.
    let target = app.hosts.find(
      (h) => h.host === p.host && h.username === p.user && h.port === p.port,
    );
    let id = target?.id;
    if (!id) {
      try {
        const created = (await HostService.Create({
          name: `${p.user}@${p.host}`,
          host: p.host,
          port: p.port,
          username: p.user,
          authMethod: "password",
          keyID: "",
          group: "Quick Connect",
          environment: "",
          proxyJump: "",
          notes: "",
          tags: ["quick"],
        } as unknown as Host)) as Host;
        id = created.id;
        await app.refreshHosts();
      } catch (e: any) {
        app.toast("error", "Quick connect failed", String(e?.message ?? e));
        return;
      }
    }
    if (id) bus.emit("connect-host", { hostID: id });
  }

  let quickConnectAction = $derived.by<Action | null>(() => {
    const p = parseConnect(input);
    if (!p) return null;
    return {
      id: "quick:connect",
      label: `Connect to ${p.user}@${p.host}:${p.port}`,
      hint: "Quick Connect · saves the host",
      icon: Plug,
      category: "Quick Connect",
      run: () => quickConnect(p),
    };
  });

  function score(a: Action, q: string): number {
    if (!q) return 1;
    const haystack = `${a.label} ${a.hint ?? ""} ${a.keywords ?? ""} ${a.category}`.toLowerCase();
    const needle = q.toLowerCase();
    if (haystack.includes(needle)) return 100 - haystack.indexOf(needle);
    // Cheap fuzzy: every char in order somewhere.
    let i = 0;
    for (const c of haystack) {
      if (c === needle[i]) i++;
      if (i === needle.length) return 50;
    }
    return 0;
  }

  let filtered = $derived([
    ...(quickConnectAction ? [quickConnectAction] : []),
    ...actions
      .map((a) => ({ a, s: score(a, input) }))
      .filter((x) => x.s > 0)
      .sort((x, y) => y.s - x.s)
      .map((x) => x.a),
  ]);

  // Typing more characters shrinks `filtered` from under the cursor — a stale
  // `highlighted` then points past the end and Enter does nothing. Clamp it
  // whenever the result count changes.
  $effect(() => {
    if (highlighted > filtered.length - 1) highlighted = Math.max(0, filtered.length - 1);
  });

  $effect(() => {
    if (app.paletteOpen) {
      input = "";
      highlighted = 0;
      void focusInput();
      void refreshOperationActions();
      // Refresh searchable objects on each open so newly-saved items appear.
      void SnippetService.List().then((s) => {
        snippets = (s ?? []) as Snippet[];
      });
      void HistoryService.List("", "", 200).then((entries) => {
        historyEntries = (entries ?? []) as HistoryEntry[];
      });
      void RecordingService.List().then((items) => {
        recordings = (items ?? []) as Recording[];
      });
    }
  });

  async function focusInput() {
    await tick();
    inputEl?.focus();
  }

  onMount(() => {
    const offShortcuts = subscribeShortcuts((next) => { shortcuts = next; });
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      const dialog = (e.target as HTMLElement)?.closest?.('[role="dialog"]');
      if (dialog && (!app.paletteOpen || dialog.getAttribute("aria-label") !== "Command palette")) return;
      const target = e.target as HTMLElement | null;
      if (!app.paletteOpen && (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement || target?.isContentEditable)) return;
      if (shortcutMatchesEvent(e, shortcuts.palette)) {
        e.preventDefault();
        e.stopPropagation();
        app.paletteOpen = !app.paletteOpen;
        return;
      }
      if (!app.paletteOpen) return;
      if (e.key === "Escape") {
        e.preventDefault();
        app.paletteOpen = false;
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        if (filtered.length > 0) {
          highlighted = Math.min(highlighted + 1, filtered.length - 1);
          scrollHighlightedIntoView();
        }
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        highlighted = Math.max(highlighted - 1, 0);
        scrollHighlightedIntoView();
      } else if (e.key === "Enter") {
        if ((e.target as HTMLElement)?.closest?.("button")) return;
        e.preventDefault();
        const a = filtered[highlighted];
        if (a) {
          void a.run();
          app.paletteOpen = false;
        }
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => { window.removeEventListener("keydown", onKey, true); offShortcuts(); };
  });

  // Group filtered into category buckets in input order.
  let grouped = $derived(() => {
    const groups: { name: string; items: Action[] }[] = [];
    for (const a of filtered) {
      let g = groups.find((g) => g.name === a.category);
      if (!g) {
        g = { name: a.category, items: [] };
        groups.push(g);
      }
      g.items.push(a);
    }
    return groups;
  });

  // Map filtered action -> overall index for keyboard highlight.
  function indexOf(a: Action) {
    return filtered.indexOf(a);
  }
</script>

{#if app.paletteOpen}
  <Dialog
    label="Command palette"
    onclose={() => (app.paletteOpen = false)}
    backdropClass="bg-black/80 !items-start pt-[14vh]"
    panelClass="flex w-[580px] max-h-[72vh] flex-col overflow-hidden border hairline-strong surface-2 shadow-2xl"
    panelStyle="border-radius: var(--radius-md); box-shadow: 0 0 0 1px var(--color-line-strong), 0 0 60px rgba(59,130,246,0.05), 0 40px 80px rgba(0,0,0,0.6);"
  >
      <!-- Search input -->
      <div class="flex shrink-0 items-center gap-3 border-b hairline px-4 py-3">
        <span class="font-mono type-caption text-[var(--color-accent)]/60">&gt;_</span>
        <input
          bind:this={inputEl}
          bind:value={input}
          data-autofocus
          aria-label="Search commands, hosts, and views"
          class="min-w-0 flex-1 bg-transparent type-body text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)]"
          placeholder="Type a command, host, or view..."
        />
        <kbd class="border border-[var(--color-line-strong)] px-1.5 py-0.5 font-mono type-micro text-[var(--color-text-4)]" style="border-radius: var(--radius-sm);">ESC</kbd>
      </div>

      <!-- Results -->
      <div class="min-h-0 max-h-[400px] overflow-y-auto">
        {#each grouped() as group (group.name)}
          <div class="type-eyebrow px-4 pt-3 pb-1 type-micro text-[var(--color-text-4)]">
            {group.name}
          </div>
          {#each group.items as a (a.id)}
            {@const idx = indexOf(a)}
            <button
              data-palette-idx={idx}
              class="flex w-full items-center gap-2.5 border-l-2 px-4 py-2 text-left type-body transition-colors {idx === highlighted ? 'border-[var(--color-accent)] bg-[var(--color-accent)]/6 text-[var(--color-text-1)]' : 'border-transparent text-[var(--color-text-2)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-1)]'}"
              onmouseenter={() => (highlighted = idx)}
              onclick={() => { void a.run(); app.paletteOpen = false; }}
            >
              <a.icon size="13" class={idx === highlighted ? 'text-[var(--color-accent)] shrink-0' : 'text-[var(--color-text-4)] shrink-0'} />
              <span class="flex-1 truncate">{a.label}</span>
              {#if a.hint}
                <span class="shrink-0 font-mono type-micro text-[var(--color-text-4)]">{a.hint}</span>
              {/if}
            </button>
          {/each}
        {/each}
        {#if filtered.length === 0}
          <div class="px-4 py-10 text-center type-body text-[var(--color-text-4)]">
            No matches
          </div>
        {/if}
      </div>

      <!-- Footer -->
      <div class="flex shrink-0 items-center gap-4 border-t hairline px-4 py-2 font-mono type-micro text-[var(--color-text-4)]">
        <span class="flex items-center gap-1.5"><kbd class="border border-[var(--color-line-strong)] px-1" style="border-radius: var(--radius-sm);">↑↓</kbd> navigate</span>
        <span class="flex items-center gap-1.5"><kbd class="border border-[var(--color-line-strong)] px-1" style="border-radius: var(--radius-sm);">↵</kbd> select</span>
         <span class="ml-auto flex items-center gap-1.5"><kbd class="border border-[var(--color-line-strong)] px-1" style="border-radius: var(--radius-sm);">{shortcutLabel("palette", shortcuts)}</kbd> toggle</span>
      </div>
  </Dialog>
{/if}
