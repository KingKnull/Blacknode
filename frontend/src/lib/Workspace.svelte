<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import {
    VaultService,
    PluginService,
  } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import { app, type View } from "./state.svelte";
  import HostList from "./HostList.svelte";
  import Pane from "./Pane.svelte";
  import OnboardingCard from "./OnboardingCard.svelte";
  import Palette from "./Palette.svelte";
  import NavRail from "./NavRail.svelte";
  import SectionTabs from "./SectionTabs.svelte";
  import HostDetail from "./HostDetail.svelte";
  import TabBar from "./TabBar.svelte";
  import PanelRouter from "./PanelRouter.svelte";
  import StatusBar from "./StatusBar.svelte";
  import ShortcutOverlay from "./ShortcutOverlay.svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";

  // Heavy panels (AI SDK glue) are lazy-loaded so the code
  // they pull in doesn't sit in the main bundle.
  const loadAIDrawer = () =>
    import("./AIDrawer.svelte").then((m) => m.default);
  import Toaster from "./Toaster.svelte";
  import Logo from "./logo/Logo.svelte";
  import {
    closeLeaf,
    leaves,
    newLeaf,
    setRatio,
    splitLeaf,
    type Direction,
    type PaneNode,
  } from "./panes";
  import { bus } from "./events";
  import {
    Radio,
    Lock,
    Server,
    Command,
    Sparkles,
  } from "@lucide/svelte";

  type Tab = { id: string; root: PaneNode; activeLeafID: string };

  function makeTab(): Tab {
    const leaf = newLeaf();
    return { id: leaf.id + "-tab", root: leaf, activeLeafID: leaf.id };
  }

  const firstTab = makeTab();
  let tabs = $state<Tab[]>([firstTab]);
  let activeTabID = $state(firstTab.id);

  // Session persistence — save tab layout info to localStorage.
  // We save tab count, active view, and connected host IDs for restoration.
  const SESSION_KEY = 'blacknode.session';

  function saveSession() {
    try {
      const data = {
        tabCount: tabs.length,
        view: app.view,
        sidebarWidth: sidebarWidth,
      };
      localStorage.setItem(SESSION_KEY, JSON.stringify(data));
    } catch { /* ignore quota errors */ }
  }

  function restoreSession() {
    try {
      const raw = localStorage.getItem(SESSION_KEY);
      if (!raw) return;
      const data = JSON.parse(raw);
      // Restore extra tabs beyond the initial one
      if (data.tabCount > 1) {
        for (let i = 1; i < data.tabCount; i++) {
          tabs.push(makeTab());
        }
      }
      if (data.view) app.view = data.view;
      if (data.sidebarWidth) sidebarWidth = data.sidebarWidth;
    } catch { /* ignore parse errors */ }
  }

  // Save session whenever tabs change
  $effect(() => {
    // Access reactive deps
    tabs.length;
    app.view;
    saveSession();
  });

  let vaultLockOff: (() => void) | undefined;

  onMount(() => {
    restoreSession();
    void app.refreshAll();

    // Activity tracking for vault auto-lock.
    const onActivity = () => app.touchActivity();
    window.addEventListener("keydown", onActivity, true);
    window.addEventListener("mousedown", onActivity, true);

    // Keyboard shortcuts for workspace navigation.
    const onShortcut = (e: KeyboardEvent) => {
      // ? opens shortcut overlay (only when not typing in an input)
      if (e.key === '?' && !(e.target instanceof HTMLInputElement) && !(e.target instanceof HTMLTextAreaElement)) {
        e.preventDefault();
        shortcutOpen = !shortcutOpen;
        return;
      }

      const mod = e.metaKey || e.ctrlKey;
      if (!mod) return;
      const k = e.key.toLowerCase();

      // Cmd+I — toggle AI drawer
      if (k === "i") {
        e.preventDefault();
        app.aiOpen = !app.aiOpen;
        return;
      }

      // Tab shortcuts only apply in terminals view
      if (app.view !== "terminals") return;

      // Cmd+T — new tab
      if (k === "t") {
        e.preventDefault();
        newTab();
        return;
      }

      // Cmd+W — close active tab
      if (k === "w") {
        e.preventDefault();
        closeTab(activeTabID);
        return;
      }

      // Ctrl+Tab / Ctrl+Shift+Tab — cycle tabs
      if (e.key === "Tab") {
        e.preventDefault();
        const idx = tabs.findIndex((t) => t.id === activeTabID);
        if (e.shiftKey) {
          activeTabID = tabs[(idx - 1 + tabs.length) % tabs.length].id;
        } else {
          activeTabID = tabs[(idx + 1) % tabs.length].id;
        }
        return;
      }

      // Ctrl+1-9 — jump to tab N
      const num = parseInt(e.key);
      if (num >= 1 && num <= 9) {
        e.preventDefault();
        const target = tabs[Math.min(num - 1, tabs.length - 1)];
        if (target) activeTabID = target.id;
        return;
      }
    };
    window.addEventListener("keydown", onShortcut);

    vaultLockOff = Events.On("vault:locked", () => {
      // Idle lock must stick: without this flag refreshVault would auto-unlock
      // via the remember-me token and the vault would never stay locked.
      app.suppressAutoUnlock = true;
      void app.refreshVault();
      app.aiOpen = false;
    });

    // Snippets and History panels emit events via the typed bus rather than
    // calling into the workspace directly (they don't know which leaf is active).
    // Bridge it to the existing pending-insert channel.
    const offInsert = bus.on('insert-into-active-terminal', (text) => {
      aiInsert(text);
    });

    // Bridge for plugin iframe → host backchannel. Iframes post messages
    // to the parent window; we whitelist a handful of methods and route
    // them through the matching service. Anything else is dropped.
    const onPluginMessage = (e: MessageEvent) => {
      const data = e.data;
      if (!data || typeof data !== "object" || typeof data.type !== "string") {
        return;
      }
      if (!data.type.startsWith("host.")) return;
      const pluginID =
        typeof data.pluginId === "string" ? data.pluginId : "";
      switch (data.type) {
        case "host.notify":
          PluginService.HostNotify(
            pluginID,
            String(data.title ?? ""),
            String(data.body ?? ""),
          );
          break;
        // Add more allowlisted methods here as the SDK grows.
      }
    };
    window.addEventListener("message", onPluginMessage);

    void app.refreshPluginPanels();

    // Tile active hosts — build a grid of all connected hosts in one tab.
    const offTile = bus.on('tile-active-hosts', () => tileActiveHosts());

    // Connect a host from the detail panel / palette — open a fresh terminal
    // tab and route it to the chosen host once the Terminal has mounted.
    const offConnect = bus.on('connect-host', ({ hostID }) => connectHost(hostID));

    // Same flow, but via Mosh — opens a fresh tab and tells the Terminal to
    // switch into Mosh mode once mounted.
    const offConnectMosh = bus.on('connect-host-mosh', ({ hostID }) => connectHostMosh(hostID));

    return () => {
      window.removeEventListener("keydown", onActivity, true);
      window.removeEventListener("mousedown", onActivity, true);
      window.removeEventListener("keydown", onShortcut);
      offInsert();
      window.removeEventListener("message", onPluginMessage);
      offTile();
      offConnect();
      offConnectMosh();
    };
  });

  // "+ New" dropdown actions from the section tab bar.
  function onNew(what: "host" | "terminal" | "shell" | "database" | "http") {
    switch (what) {
      case "host":
        bus.emit("new-host");
        break;
      case "terminal":
      case "shell":
        app.view = "terminals";
        newTab();
        break;
      case "database":
        app.view = "database";
        break;
      case "http":
        app.view = "http";
        break;
    }
  }

  // Open a fresh terminal tab and connect it to the given host.
  function connectHost(hostID: string) {
    app.view = "terminals";
    const t = makeTab();
    tabs.push(t);
    activeTabID = t.id;
    const sid = leaves(t.root)[0]?.sessionID;
    if (!sid) return;
    // The pane for this session hasn't mounted yet. Park the intent; it picks
    // it up on mount, whenever that is.
    app.requestConnect(sid, hostID, "ssh");
  }

  // Same as connectHost but routes through Mosh instead of plain SSH.
  function connectHostMosh(hostID: string) {
    app.view = "terminals";
    const t = makeTab();
    tabs.push(t);
    activeTabID = t.id;
    const sid = leaves(t.root)[0]?.sessionID;
    if (!sid) return;
    app.requestConnect(sid, hostID, "mosh");
  }

  onDestroy(() => vaultLockOff?.());

  function newTab() {
    const t = makeTab();
    tabs.push(t);
    activeTabID = t.id;
  }

  function closeTab(id: string) {
    const i = tabs.findIndex((t) => t.id === id);
    if (i === -1) return;
    tabs.splice(i, 1);
    if (activeTabID === id) {
      activeTabID = tabs[Math.max(0, i - 1)]?.id ?? "";
    }
    if (tabs.length === 0) newTab();
  }

  // Close every tab except the one whose id is `keepID`. Useful from the
  // tab right-click menu — prefer this over a flurry of single closes so the
  // active terminal isn't briefly stranded mid-loop.
  function closeOthers(keepID: string) {
    const keep = tabs.find((t) => t.id === keepID);
    if (!keep) return;
    tabs = [keep];
    activeTabID = keep.id;
  }

  function onActivate(tabID: string, leafID: string) {
    const t = tabs.find((t) => t.id === tabID);
    if (t) t.activeLeafID = leafID;
  }

  function onSplit(tabID: string, leafID: string, direction: Direction) {
    const t = tabs.find((t) => t.id === tabID);
    if (!t) return;
    t.root = splitLeaf(t.root, leafID, direction);
  }

  function onCloseLeaf(tabID: string, leafID: string) {
    const t = tabs.find((t) => t.id === tabID);
    if (!t) return;
    const next = closeLeaf(t.root, leafID);
    if (next === null) {
      closeTab(tabID);
      return;
    }
    t.root = next;
    const allLeaves = leaves(t.root);
    if (!allLeaves.find((l) => l.id === t.activeLeafID)) {
      t.activeLeafID = allLeaves[0]?.id ?? "";
    }
  }

  function onResize(tabID: string, splitID: string, ratio: number) {
    const t = tabs.find((t) => t.id === tabID);
    if (!t) return;
    t.root = setRatio(t.root, splitID, ratio);
  }

  async function lockVault() {
    app.suppressAutoUnlock = true;
    await VaultService.Lock();
    await app.refreshAll();
  }

  // Build a grid layout from all currently connected hosts.
  // Creates a new tab with a 2×N grid of terminal panes, one per connected
  // host. Automatically enables broadcast across all of them.
  function tileActiveHosts() {
    const connectedIDs = Array.from(app.connectedHosts);
    if (connectedIDs.length === 0) {
      app.toast('warn', 'NO CONNECTED HOSTS', 'Connect to at least one host before tiling.');
      return;
    }
    app.view = 'terminals';

    // Build a balanced binary tree of leaf panes.
    function buildTree(hostIDs: string[]): PaneNode {
      if (hostIDs.length === 1) {
        return newLeaf();
      }
      const mid = Math.ceil(hostIDs.length / 2);
      return {
        kind: 'split',
        id: crypto.randomUUID(),
        direction: hostIDs.length <= 2 ? 'horizontal' : (hostIDs.length <= 4 ? 'horizontal' : 'vertical'),
        ratio: 0.5,
        a: buildTree(hostIDs.slice(0, mid)),
        b: buildTree(hostIDs.slice(mid)),
      };
    }

    const root = connectedIDs.length === 1 ? newLeaf() : buildTree(connectedIDs);
    const allLeaves = leaves(root);

    const tab: Tab = {
      id: crypto.randomUUID(),
      root,
      activeLeafID: allLeaves[0]?.id ?? '',
    };
    tabs.push(tab);
    activeTabID = tab.id;

    // Add all leaf session IDs to the broadcast set and enable broadcast.
    const broadcastSet = new Set(app.broadcastSet);
    for (const leaf of allLeaves) {
      broadcastSet.add(leaf.sessionID);
    }
    app.broadcastSet = broadcastSet;
    app.broadcastEnabled = true;

    // Park a connect intent per pane. Tiling creates several panes at once, so
    // this is where a mount-delay guess used to hurt most — the slowest pane
    // set the deadline for all of them.
    allLeaves.forEach((leaf, i) => {
      if (connectedIDs[i]) {
        app.requestConnect(leaf.sessionID, connectedIDs[i], "ssh");
      }
    });

    app.toast('ok', `TILED ${connectedIDs.length} HOSTS`, 'Broadcast mode enabled. Type once, execute everywhere.');
  }

  // Find the active terminal leaf so AIDrawer's "insert" lands in the right
  // pane.
  function activeSessionID(): string | null {
    const tab = tabs.find((t) => t.id === activeTabID);
    if (!tab) return null;
    const leaf = leaves(tab.root).find((l) => l.id === tab.activeLeafID);
    return leaf?.sessionID ?? null;
  }

  function aiInsert(text: string) {
    if (app.view !== "terminals") app.view = "terminals";
    const sid = activeSessionID();
    if (!sid) return;
    app.insertIntoTerminal(sid, text);
  }

  import {
    TerminalSquare,
    Zap,
    Folder,
    Activity,
    KeyRound,
    Network as NetworkIcon,
    ScrollText,
    Film,
    Boxes,
    Radar,
    Cpu,
    Globe2,
    Database,
    Bookmark,
    Share2,
    Puzzle,
    Activity as ActivityIcon,
    History as HistoryIcon,
    Shield,
    Settings as SettingsIcon,
  } from "@lucide/svelte";

  type ViewDef = { id: View; label: string; Icon: any };
  type Section = { id: string; label: string; Icon: any; views: ViewDef[] };

  // Hybrid nav: a slim vertical rail of SECTIONS, each opening a horizontal
  // tab bar of its views. Keeps Blacknode's wide surface organised without a
  // 20-icon rail.
  const SECTIONS: Section[] = [
    { id: "sessions", label: "Sessions", Icon: TerminalSquare, views: [
      { id: "terminals", label: "Terminals", Icon: TerminalSquare },
      { id: "exec", label: "Multi-host", Icon: Zap },
      { id: "files", label: "Files", Icon: Folder },
      { id: "snippets", label: "Snippets", Icon: Bookmark },
      { id: "recordings", label: "Recordings", Icon: Film },
      { id: "history", label: "History", Icon: HistoryIcon },
    ] },
    { id: "monitor", label: "Monitor", Icon: ActivityIcon, views: [
      { id: "metrics", label: "Metrics", Icon: Activity },
      { id: "logs", label: "Logs", Icon: ScrollText },
      { id: "processes", label: "Processes", Icon: Cpu },
      { id: "activity", label: "Activity", Icon: ActivityIcon },
    ] },
    { id: "network", label: "Network", Icon: NetworkIcon, views: [
      { id: "forwards", label: "Forwards", Icon: NetworkIcon },
      { id: "network", label: "Scan", Icon: Radar },
      { id: "topology", label: "Topology", Icon: Share2 },
    ] },
    { id: "workloads", label: "Workloads", Icon: Boxes, views: [
      { id: "containers", label: "Containers", Icon: Boxes },
      { id: "database", label: "Database", Icon: Database },
      { id: "http", label: "HTTP", Icon: Globe2 },
    ] },
    { id: "vault", label: "Vault", Icon: Shield, views: [
      { id: "vault", label: "Vault", Icon: Shield },
      { id: "keys", label: "Keys", Icon: KeyRound },
    ] },
    { id: "plugins", label: "Plugins", Icon: Puzzle, views: [
      { id: "plugins", label: "Plugins", Icon: Puzzle },
    ] },
    { id: "settings", label: "Settings", Icon: SettingsIcon, views: [
      { id: "settings", label: "Settings", Icon: SettingsIcon },
    ] },
  ];

  // Remember the last view visited within each section so returning to a
  // section restores where you were.
  let lastViewPerSection = $state<Record<string, View>>({});

  function sectionOf(view: View): Section {
    if (typeof view === "string" && view.startsWith("plugin:")) {
      return SECTIONS.find((s) => s.id === "plugins")!;
    }
    return SECTIONS.find((s) => s.views.some((v) => v.id === view)) ?? SECTIONS[0];
  }

  let activeSection = $derived(sectionOf(app.view));

  // Plugin panels become extra tabs under the Plugins section.
  let sectionViews = $derived.by<ViewDef[]>(() => {
    if (activeSection.id !== "plugins") return activeSection.views;
    const pluginTabs: ViewDef[] = app.pluginPanels.map((p) => ({
      id: `plugin:${p.pluginId}:${p.id}` as View,
      label: p.title,
      Icon: Puzzle,
    }));
    return [...activeSection.views, ...pluginTabs];
  });

  function selectSection(s: Section) {
    app.view = lastViewPerSection[s.id] ?? s.views[0].id;
  }

  $effect(() => {
    lastViewPerSection[activeSection.id] = app.view;
  });

  let activeTab = $derived(tabs.find((t) => t.id === activeTabID));
  let activeLeafCount = $derived(activeTab ? leaves(activeTab.root).length : 0);

  // Per-tab labels from Terminal.svelte via the typed event bus.
  let tabLabels = $state<Record<string, string>>({});

  $effect(() => {
    const off = bus.on('tab-label', (detail) => {
      if (detail.tabID) tabLabels[detail.tabID] = detail.label;
    });
    // Terminals only know their sessionID — resolve which tab owns the
    // session so a connected tab shows the host name instead of local-N.
    const offSession = bus.on('session-label', (detail) => {
      const tab = tabs.find((t) => leaves(t.root).some((l) => l.sessionID === detail.sessionID));
      if (tab) tabLabels[tab.id] = detail.label;
    });
    return () => { off(); offSession(); };
  });

  function tabLabel(t: Tab): string {
    const label = tabLabels[t.id];
    if (label) return label;
    const idx = tabs.indexOf(t) + 1;
    return `local-${idx}`;
  }

  let sidebarWidth = $state(Number(localStorage.getItem('blacknode.sidebar-width') || 252));
  let isResizing = $state(false);
  let shortcutOpen = $state(false);

  function startResize(e: MouseEvent) {
    isResizing = true;
    e.preventDefault();
  }

  function onMouseMove(e: MouseEvent) {
    if (!isResizing) return;
    sidebarWidth = Math.max(160, Math.min(600, e.clientX - 60));
  }

  function onMouseUp() {
    if (isResizing) {
      isResizing = false;
      localStorage.setItem('blacknode.sidebar-width', sidebarWidth.toString());
    }
  }
</script>

<svelte:window onmousemove={onMouseMove} onmouseup={onMouseUp} />

<div
  class="flex h-full w-full flex-col bg-[var(--color-surface-0)] text-[var(--color-text-1)]"
>
  <!-- ── TOP BAR ─────────────────────────────────────────────────────── -->
  <header class="relative flex h-10 shrink-0 items-center gap-3 border-b hairline surface-1 px-3">
    <div class="flex items-center gap-1.5 select-none">
      <Logo size={16} />
    </div>

    <div class="h-4 w-px bg-[var(--color-line-strong)]"></div>

    <!-- Breadcrumb -->
    <span class="type-caption font-medium capitalize text-[var(--color-text-2)]">
      {app.view}
    </span>

    <div class="ml-auto flex items-center gap-1 type-caption">
      <!-- Broadcast -->
      <button
        class="flex items-center gap-1.5 border px-2 py-0.5 rounded-sm transition-all {app.broadcastEnabled
          ? 'border-[var(--color-warn)]/40 bg-[var(--color-warn)]/8 text-[var(--color-warn)]'
          : 'border-[var(--color-line)] text-[var(--color-text-4)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)]'}"
        onclick={() => {
          if (!app.broadcastEnabled && app.broadcastSet.size === 0) {
            app.toast('warn', 'No panes in broadcast', 'Click the Cast button on each pane you want to include before enabling broadcast.');
          }
          app.broadcastEnabled = !app.broadcastEnabled;
        }}
        title={app.broadcastEnabled ? `Broadcasting to ${app.broadcastSet.size} panes` : 'Enable multi-pane keystroke broadcast'}
      >
        <Radio size="11" class={app.broadcastEnabled ? 'pulse-soft' : ''} />
        <span>Cast</span>
        {#if app.broadcastEnabled}
          <span class="font-mono border border-[var(--color-warn)]/30 bg-[var(--color-warn)]/15 px-1 type-micro">{app.broadcastSet.size}</span>
        {/if}
      </button>

      <!-- AI -->
      <button
        class="flex items-center gap-1.5 border px-2 py-0.5 rounded-sm transition-all {app.aiOpen
          ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent)]/8 text-[var(--color-accent)]'
          : 'border-[var(--color-line)] text-[var(--color-text-4)] hover:border-[var(--color-accent)]/30 hover:text-[var(--color-accent)]'}"
        onclick={() => (app.aiOpen = !app.aiOpen)}
        title="AI assistant (⌘I)"
      >
        <Sparkles size="11" />
        <span>AI</span>
      </button>

      <!-- Command palette -->
      <button
        class="flex items-center gap-1.5 border border-[var(--color-line)] px-2 py-0.5 rounded-sm text-[var(--color-text-4)] transition-all hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)]"
        onclick={() => (app.paletteOpen = true)}
        title="Command palette (⌘K)"
      >
        <Command size="11" />
        <span>Palette</span>
        <kbd class="font-mono border border-[var(--color-line-strong)] px-1 type-micro opacity-50">⌘K</kbd>
      </button>

      <div class="mx-1 h-3 w-px bg-[var(--color-line-strong)]"></div>

      <!-- Vault lock -->
      <button
        class="flex items-center gap-1.5 border border-[var(--color-line)] px-2 py-0.5 rounded-sm text-[var(--color-text-4)] hover:border-[var(--color-accent)]/30 hover:text-[var(--color-accent)]"
        onclick={lockVault}
        title="Vault unlocked — click to lock"
      >
        <Lock size="11" class="text-[var(--color-accent)]" />
        <span class="text-[var(--color-accent)]">Lock vault</span>
      </button>
    </div>
  </header>

  <!-- ── BODY ─────────────────────────────────────────────────────────── -->
  <div class="grid flex-1 overflow-hidden" style="grid-template-columns: 60px {sidebarWidth}px 1fr">
    <!-- ── SECTION RAIL ─────────────────────────────── -->
    <NavRail sections={SECTIONS} activeSectionId={activeSection.id} onSelect={(id) => selectSection(SECTIONS.find((s) => s.id === id)!)} />

    <!-- ── SIDEBAR ─────────────────────────────────────── -->
    <aside class="relative overflow-hidden border-r hairline group/sidebar">
      <HostList />
      <!-- Resize handle — a separator is the correct role for a drag-to-resize
           divider, and pointer-drag is its native interaction. A small grip
           indicator fades in on hover/drag so the affordance is discoverable
           without being visible all the time. -->
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <div
        role="separator"
        aria-orientation="vertical"
        tabindex="-1"
        class="absolute inset-y-0 -right-1 z-10 flex w-2 cursor-col-resize items-center justify-center transition-colors {isResizing ? 'bg-[var(--color-accent)]/40' : 'hover:bg-[var(--color-accent)]/20'}"
        onmousedown={startResize}
      >
        <span
          class="h-8 w-[3px] rounded-full bg-[var(--color-line-strong)] opacity-0 transition-opacity duration-150 group-hover/sidebar:opacity-100 {isResizing ? 'opacity-100 bg-[var(--color-accent)]' : ''}"
        ></span>
      </div>
    </aside>

    <!-- Main + AI drawer -->
    <div
      class="grid overflow-hidden transition-[grid-template-columns] duration-200"
      style:grid-template-columns={app.aiOpen ? 'minmax(400px, 1fr) 360px' : '1fr'}
    >
      <main class="relative flex flex-col overflow-hidden">
        <SectionTabs
          views={sectionViews}
          activeView={app.view}
          onSelect={(id) => (app.view = id)}
          onNew={onNew}
        />
        <HostDetail />
        <PanelRouter>
          <!-- terminals view: tab bar + pane grid -->
          <div class="relative flex h-full flex-col">
            <OnboardingCard />
            <TabBar
              {tabs}
              {activeTabID}
              {tabLabel}
              onNewTab={newTab}
              onCloseTab={closeTab}
              onCloseOthers={closeOthers}
              onSelectTab={(id) => (activeTabID = id)}
            />
            <div class="flex-1 overflow-hidden">
              {#each tabs as t (t.id)}
                <div class="h-full w-full" class:hidden={activeTabID !== t.id}>
                  <Pane
                    node={t.root}
                    activeLeafID={t.activeLeafID}
                    onactivate={(id) => onActivate(t.id, id)}
                    onsplit={(id, d) => onSplit(t.id, id, d)}
                    onclose={(id) => onCloseLeaf(t.id, id)}
                    onresize={(splitID, ratio) => onResize(t.id, splitID, ratio)}
                  />
                </div>
              {/each}
            </div>
          </div>
        </PanelRouter>
      </main>

      {#if app.aiOpen}
        {#await loadAIDrawer() then AIDrawer}
          <AIDrawer onInsertCommand={aiInsert} />
        {/await}
      {/if}
    </div>
  </div>

  <Palette onNewTab={newTab} />
  <Toaster />

  {#if shortcutOpen}
    <ShortcutOverlay onclose={() => (shortcutOpen = false)} />
  {/if}

  <!-- A dangerous command caught on its way into every broadcast pane. Held
       here rather than in the source Terminal because the command targets the
       whole group, not one pane — and the answer has to apply to all of them. -->
  {#if app.pendingBroadcastDanger}
    {@const p = app.pendingBroadcastDanger}
    <ConfirmDanger
      title={p.danger.level === "block-without-confirm"
        ? `Dangerous broadcast — ${p.danger.reason}`
        : `Risky broadcast — ${p.danger.reason}`}
      body={`\`${p.command}\` matches \`${p.danger.matched}\` and will run on ${p.targets} other pane${p.targets === 1 ? "" : "s"} at once. It has already run in the pane you typed it in.`}
      severity={p.danger.level}
      productionHosts={p.productionHosts}
      requirePhrase={p.danger.level === "block-without-confirm"
        ? p.productionHosts.length > 0
          ? "destroy production"
          : "I understand"
        : undefined}
      onCancel={() => app.cancelBroadcastDanger()}
      onConfirm={() => app.confirmBroadcastDanger()}
    />
  {/if}

  <!-- ── STATUS BAR ──────────────────────────────────────────────── -->
  <StatusBar tabCount={tabs.length} {activeLeafCount} />
</div>

