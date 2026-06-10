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
  import TabBar from "./TabBar.svelte";
  import PanelRouter from "./PanelRouter.svelte";
  import StatusBar from "./StatusBar.svelte";
  import ShortcutOverlay from "./ShortcutOverlay.svelte";

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

    return () => {
      window.removeEventListener("keydown", onActivity, true);
      window.removeEventListener("mousedown", onActivity, true);
      window.removeEventListener("keydown", onShortcut);
      offInsert();
      window.removeEventListener("message", onPluginMessage);
      offTile();
    };
  });

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

  // Drag-reorder state. We move tabs in the array as the dragged element
  // crosses each sibling's midpoint — feels like Chrome / VS Code rather
  // than the default "drop at the end" behavior of the HTML5 DnD API.
  let dragSourceID = $state<string | null>(null);
  let dragOverID = $state<string | null>(null);

  function tabDragStart(e: DragEvent, id: string) {
    dragSourceID = id;
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = "move";
      e.dataTransfer.setData("text/plain", id);
    }
  }
  function tabDragOver(e: DragEvent, id: string) {
    if (!dragSourceID || dragSourceID === id) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
    dragOverID = id;
    const from = tabs.findIndex((t) => t.id === dragSourceID);
    const to = tabs.findIndex((t) => t.id === id);
    if (from === -1 || to === -1 || from === to) return;
    const [moved] = tabs.splice(from, 1);
    tabs.splice(to, 0, moved);
  }
  function tabDragEnd() {
    dragSourceID = null;
    dragOverID = null;
  }

  // Right-click context menu on tabs.
  let tabMenu = $state<{ x: number; y: number; tabID: string } | null>(null);
  function openTabMenu(e: MouseEvent, id: string) {
    e.preventDefault();
    tabMenu = { x: e.clientX, y: e.clientY, tabID: id };
  }
  function closeTabMenu() {
    tabMenu = null;
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

    // Dispatch events so each Terminal component knows which host to connect to.
    // We use a small delay so the Terminal components mount first.
    setTimeout(() => {
      allLeaves.forEach((leaf, i) => {
        if (connectedIDs[i]) {
          bus.emit('connect-terminal-to-host', {
            sessionID: leaf.sessionID,
            hostID: connectedIDs[i],
          });
        }
      });
    }, 200);

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

  const VIEWS: { id: View; label: string; Icon: any }[] = [
    { id: "terminals", label: "Terminals", Icon: TerminalSquare },
    { id: "exec", label: "Multi-host", Icon: Zap },
    { id: "files", label: "Files", Icon: Folder },
    { id: "metrics", label: "Metrics", Icon: Activity },
    { id: "logs", label: "Logs", Icon: ScrollText },
    { id: "forwards", label: "Forwards", Icon: NetworkIcon },
    { id: "recordings", label: "Recordings", Icon: Film },
    { id: "containers", label: "Containers", Icon: Boxes },
    { id: "network", label: "Network", Icon: Radar },
    { id: "processes", label: "Processes", Icon: Cpu },
    { id: "http", label: "HTTP", Icon: Globe2 },
    { id: "database", label: "Database", Icon: Database },
    { id: "snippets", label: "Snippets", Icon: Bookmark },
    { id: "history", label: "History", Icon: HistoryIcon },
    { id: "activity", label: "Activity", Icon: ActivityIcon },
    { id: "topology", label: "Topology", Icon: Share2 },
    { id: "plugins", label: "Plugins", Icon: Puzzle },
    { id: "vault", label: "Vault", Icon: Shield },
    { id: "keys", label: "Keys", Icon: KeyRound },
    { id: "settings", label: "Settings", Icon: SettingsIcon },
  ];

  let activeTab = $derived(tabs.find((t) => t.id === activeTabID));
  let activeLeafCount = $derived(activeTab ? leaves(activeTab.root).length : 0);

  // Per-tab labels from Terminal.svelte via the typed event bus.
  let tabLabels = $state<Record<string, string>>({});

  $effect(() => {
    const off = bus.on('tab-label', (detail) => {
      if (detail.tabID) tabLabels[detail.tabID] = detail.label;
    });
    return () => off();
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
    sidebarWidth = Math.max(160, Math.min(600, e.clientX - 44));
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
  <header class="relative flex h-9 shrink-0 items-center gap-3 border-b hairline surface-1 px-3" style="backdrop-filter: blur(12px);">
    <div class="pointer-events-none absolute inset-x-0 bottom-0 h-px bg-gradient-to-r from-transparent via-[var(--color-accent)]/40 to-transparent"></div>
    <div class="flex items-center gap-1.5 select-none">
      <Logo size={16} />
    </div>

    <div class="h-4 w-px bg-[var(--color-line-strong)]"></div>

    <!-- Breadcrumb — Inter, normal case, readable size -->
    <span class="text-xs text-[var(--color-text-4)] tracking-wide">
      /{app.view}
    </span>

    <div class="ml-auto flex items-center gap-1 text-xs">
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
          <span class="font-mono border border-[var(--color-warn)]/30 bg-[var(--color-warn)]/15 px-1 text-[10px]">{app.broadcastSet.size}</span>
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
        <kbd class="font-mono border border-[var(--color-line-strong)] px-1 text-[10px] opacity-50">⌘K</kbd>
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
  <div class="grid flex-1 grid-cols-[44px_1fr_1fr] overflow-hidden" style="grid-template-columns: 44px {sidebarWidth}px 1fr">
    <!-- ── ICON NAV RAIL ─────────────────────────────── -->
    <NavRail views={VIEWS} pluginPanels={app.pluginPanels} />

    <!-- ── SIDEBAR ─────────────────────────────────────── -->
    <aside class="relative overflow-hidden border-r hairline group/sidebar">
      <HostList />
      <!-- Resize handle -->
      <div 
        role="separator"
        aria-orientation="vertical"
        tabindex="-1"
        class="absolute inset-y-0 -right-1 z-10 w-2 cursor-col-resize hover:bg-[var(--color-accent)]/20 active:bg-[var(--color-accent)]/40 transition-colors"
        onmousedown={startResize}
      ></div>
    </aside>

    <!-- Main + AI drawer -->
    <div
      class="grid overflow-hidden transition-[grid-template-columns] duration-200"
      style:grid-template-columns={app.aiOpen ? 'minmax(400px, 1fr) 360px' : '1fr'}
    >
      <main class="flex flex-col overflow-hidden">
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

  <!-- ── STATUS BAR ──────────────────────────────────────────────── -->
  <StatusBar tabCount={tabs.length} {activeLeafCount} />
</div>

