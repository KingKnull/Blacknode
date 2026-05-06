<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import {
    VaultService,
    PluginService,
  } from "../../bindings/github.com/blacknode/blacknode";
  import { app, type View } from "./state.svelte";
  import HostList from "./HostList.svelte";
  import Pane from "./Pane.svelte";
  import ExecPanel from "./ExecPanel.svelte";
  import SFTPPanel from "./SFTPPanel.svelte";
  import MetricsPanel from "./MetricsPanel.svelte";
  import KeysPanel from "./KeysPanel.svelte";
  import LogsPanel from "./LogsPanel.svelte";
  import ForwardsPanel from "./ForwardsPanel.svelte";
  import RecordingsPanel from "./RecordingsPanel.svelte";
  import ContainersPanel from "./ContainersPanel.svelte";
  import NetworkPanel from "./NetworkPanel.svelte";
  import ProcessesPanel from "./ProcessesPanel.svelte";
  import HTTPPanel from "./HTTPPanel.svelte";
  import SnippetsPanel from "./SnippetsPanel.svelte";
  import HistoryPanel from "./HistoryPanel.svelte";
  import TopologyPanel from "./TopologyPanel.svelte";
  import PluginsPanel from "./PluginsPanel.svelte";
  import ActivityPanel from "./ActivityPanel.svelte";
  import SettingsPanel from "./SettingsPanel.svelte";
  import OnboardingCard from "./OnboardingCard.svelte";
  import Palette from "./Palette.svelte";

  // Heavy panels (CodeMirror, AI SDK glue) are lazy-loaded so the code
  // they pull in doesn't sit in the main bundle.
  const loadDBPanel = () =>
    import("./DBPanel.svelte").then((m) => m.default);
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
  import {
    TerminalSquare,
    Zap,
    Folder,
    Activity,
    KeyRound,
    Network,
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
    Radio,
    Settings as SettingsIcon,
    Lock,
    Unlock,
    Plus,
    X,
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

  let vaultLockOff: (() => void) | undefined;

  onMount(() => {
    void app.refreshAll();

    // Activity tracking for vault auto-lock.
    const onActivity = () => app.touchActivity();
    window.addEventListener("keydown", onActivity, true);
    window.addEventListener("mousedown", onActivity, true);

    // Cmd+I toggles AI drawer; Cmd+T new terminal tab.
    const onShortcut = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey;
      if (!mod) return;
      const k = e.key.toLowerCase();
      if (k === "i") {
        e.preventDefault();
        app.aiOpen = !app.aiOpen;
      } else if (k === "t" && app.view === "terminals") {
        e.preventDefault();
        newTab();
      }
    };
    window.addEventListener("keydown", onShortcut);

    vaultLockOff = Events.On("vault:locked", () => {
      void app.refreshVault();
      app.aiOpen = false;
    });

    // Snippets and History panels emit a DOM CustomEvent rather than calling
    // into the workspace directly (they don't know which leaf is active).
    // Bridge it to the existing pending-insert channel.
    const onInsertReq = (e: Event) => {
      const ce = e as CustomEvent<string>;
      aiInsert(ce.detail);
    };
    window.addEventListener(
      "blacknode:insert-into-active-terminal",
      onInsertReq as EventListener,
    );

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

    return () => {
      window.removeEventListener("keydown", onActivity, true);
      window.removeEventListener("mousedown", onActivity, true);
      window.removeEventListener("keydown", onShortcut);
      window.removeEventListener(
        "blacknode:insert-into-active-terminal",
        onInsertReq as EventListener,
      );
      window.removeEventListener("message", onPluginMessage);
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

  const VIEWS: { id: View; label: string; Icon: any }[] = [
    { id: "terminals", label: "Terminals", Icon: TerminalSquare },
    { id: "exec", label: "Multi-host", Icon: Zap },
    { id: "files", label: "Files", Icon: Folder },
    { id: "metrics", label: "Metrics", Icon: Activity },
    { id: "logs", label: "Logs", Icon: ScrollText },
    { id: "forwards", label: "Forwards", Icon: Network },
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
    { id: "keys", label: "Keys", Icon: KeyRound },
    { id: "settings", label: "Settings", Icon: SettingsIcon },
  ];

  let activeTab = $derived(tabs.find((t) => t.id === activeTabID));
  let activeLeafCount = $derived(activeTab ? leaves(activeTab.root).length : 0);

  // Derive a human-readable label for each tab: prefer the connected host
  // name if the terminal state has one, fall back to a short session ID.
  // Terminal.svelte exposes connected host via the shared `app.selectedHostID`
  // when connecting — but that's global. For per-tab labels we track a map
  // from tab ID → host name that Terminal.svelte updates via a custom event.
  let tabLabels = $state<Record<string, string>>({});

  $effect(() => {
    const onLabel = (e: Event) => {
      const ce = e as CustomEvent<{ tabID: string; label: string }>;
      if (ce.detail?.tabID) tabLabels[ce.detail.tabID] = ce.detail.label;
    };
    window.addEventListener('blacknode:tab-label', onLabel as EventListener);
    return () => window.removeEventListener('blacknode:tab-label', onLabel as EventListener);
  });

  function tabLabel(t: Tab): string {
    return tabLabels[t.id] || 'local';
  }
</script>

<div
  class="flex h-screen w-screen flex-col bg-[var(--color-surface-0)] text-[var(--color-text-1)]"
>
  <!-- ── TOP BAR ─────────────────────────────────────────────────────── -->
  <header class="relative flex h-9 shrink-0 items-center gap-3 border-b hairline surface-1 px-3">
    <!-- Phosphor glow line -->
    <div class="pointer-events-none absolute inset-x-0 bottom-0 h-px bg-gradient-to-r from-transparent via-[var(--color-accent)]/40 to-transparent"></div>
    <!-- Bracket logo mark -->
    <div class="flex items-center gap-1.5 select-none">
      <Logo size={16} />
    </div>

    <!-- Divider -->
    <div class="h-4 w-px bg-[var(--color-line-strong)]"></div>

    <!-- Path breadcrumb showing current view -->
    <span class="font-mono text-[10px] tracking-widest text-[var(--color-text-3)] uppercase">
      /{app.view}
    </span>

    <div class="ml-auto flex items-center gap-1 font-mono text-[10px]">
      <!-- Broadcast -->
      <button
        class="flex items-center gap-1.5 border px-2 py-0.5 transition-all {app.broadcastEnabled
          ? 'border-[var(--color-warn)]/40 bg-[var(--color-warn)]/8 text-[var(--color-warn)]'
          : 'border-[var(--color-line)] text-[var(--color-text-4)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)]'}"
        onclick={() => {
          if (!app.broadcastEnabled && app.broadcastSet.size === 0) {
            alert('Broadcast is on but no panes are in the group yet.\n\nClick the \'cast\' button on each pane you want to include.');
          }
          app.broadcastEnabled = !app.broadcastEnabled;
        }}
        title={app.broadcastEnabled ? `Broadcasting to ${app.broadcastSet.size} panes` : 'Enable multi-pane keystroke broadcast'}
      >
        <Radio size="10" class={app.broadcastEnabled ? 'pulse-soft' : ''} />
        <span>CAST</span>
        {#if app.broadcastEnabled}
          <span class="border border-[var(--color-warn)]/30 bg-[var(--color-warn)]/15 px-1 text-[9px]">{app.broadcastSet.size}</span>
        {/if}
      </button>

      <!-- AI -->
      <button
        class="flex items-center gap-1.5 border px-2 py-0.5 transition-all {app.aiOpen
          ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent)]/8 text-[var(--color-accent)]'
          : 'border-[var(--color-line)] text-[var(--color-text-4)] hover:border-[var(--color-accent)]/30 hover:text-[var(--color-accent)]'}"
        onclick={() => (app.aiOpen = !app.aiOpen)}
        title="AI assistant (⌘I)"
      >
        <Sparkles size="10" />
        <span>AI</span>
      </button>

      <!-- Command palette -->
      <button
        class="flex items-center gap-1.5 border border-[var(--color-line)] px-2 py-0.5 text-[var(--color-text-4)] transition-all hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)]"
        onclick={() => (app.paletteOpen = true)}
        title="Command palette (⌘K)"
      >
        <Command size="10" />
        <span>CMD</span>
        <kbd class="border border-[var(--color-line-strong)] px-1 text-[8px] opacity-50">⌘K</kbd>
      </button>

      <div class="mx-1 h-3 w-px bg-[var(--color-line-strong)]"></div>

      <!-- Vault -->
      <button
        class="flex items-center gap-1.5 border border-[var(--color-line)] px-2 py-0.5 text-[var(--color-text-4)] hover:border-[var(--color-accent)]/30 hover:text-[var(--color-accent)]"
        onclick={lockVault}
        title="Vault unlocked — click to lock"
      >
        <Unlock size="10" class="text-[var(--color-accent)]" />
        <span class="text-[var(--color-accent)]">UNLOCKED</span>
      </button>
    </div>
  </header>

  <!-- ── BODY ─────────────────────────────────────────────────────────── -->
  <div class="grid flex-1 grid-cols-[44px_252px_1fr] overflow-hidden">
    <!-- ── ICON NAV RAIL ─────────────────────────────── -->
    <nav class="flex flex-col items-center gap-px border-r hairline surface-1 py-2">
      {#each VIEWS as v (v.id)}
        <button
          title={v.label}
          class="group relative flex h-8 w-8 items-center justify-center transition-all {app.view === v.id
            ? 'text-[var(--color-accent)]'
            : 'text-[var(--color-text-4)] hover:text-[var(--color-text-2)]'}"
          onclick={() => (app.view = v.id)}
        >
          {#if app.view === v.id}
            <!-- Active: left accent bar + phosphor glow bg -->
            <span class="absolute inset-0 bg-[var(--color-accent)]/5"></span>
            <span class="absolute left-0 inset-y-0 w-[2px] bg-[var(--color-accent)] shadow-[0_0_6px_var(--color-accent)]"></span>
          {/if}
          <v.Icon size="14" strokeWidth={app.view === v.id ? 1.5 : 1.5} />
        </button>
      {/each}
      {#if app.pluginPanels.length > 0}
        <div class="my-1.5 h-px w-5 bg-[var(--color-line)]"></div>
        {#each app.pluginPanels as panel (panel.pluginId + ':' + panel.id)}
          {@const viewID = `plugin:${panel.pluginId}:${panel.id}` as View}
          <button
            title={panel.title}
            class="group relative flex h-8 w-8 items-center justify-center transition-all {app.view === viewID
              ? 'text-[var(--color-accent)]'
              : 'text-[var(--color-text-4)] hover:text-[var(--color-text-2)]'}"
            onclick={() => (app.view = viewID)}
          >
            {#if app.view === viewID}
              <span class="absolute inset-0 bg-[var(--color-accent)]/5"></span>
              <span class="absolute left-0 inset-y-0 w-[2px] bg-[var(--color-accent)] shadow-[0_0_6px_var(--color-accent)]"></span>
            {/if}
            <Puzzle size="14" />
          </button>
        {/each}
      {/if}
    </nav>

    <!-- ── SIDEBAR ─────────────────────────────────────── -->
    <aside class="overflow-hidden border-r hairline">
      <HostList />
    </aside>

    <!-- Main + AI drawer -->
    <div
      class="grid overflow-hidden transition-[grid-template-columns] duration-200"
      style:grid-template-columns={app.aiOpen ? '1fr 360px' : '1fr'}
    >
      <main class="flex flex-col overflow-hidden">
        {#if app.view === 'terminals'}
          <div class="relative flex h-full flex-col">
            <OnboardingCard />
            <!-- Tab bar -->
            <div class="flex h-8 shrink-0 items-center gap-px border-b hairline surface-1 px-2">
              {#each tabs as t (t.id)}
                {@const label = tabLabel(t)}
                {@const isActive = activeTabID === t.id}
                <div
                  role="tab"
                  tabindex="0"
                  draggable="true"
                  aria-selected={isActive}
                  class="group flex max-w-[160px] cursor-pointer items-center gap-1.5 border-r border-[var(--color-line)] px-2.5 py-1 font-mono text-[10px] select-none transition-colors {isActive
                    ? 'bg-[var(--color-surface-2)] text-[var(--color-text-1)] border-t border-t-[var(--color-accent)]/60'
                    : 'text-[var(--color-text-4)] hover:bg-[var(--color-surface-2)]/50 hover:text-[var(--color-text-3)]'}"
                  class:opacity-40={dragSourceID === t.id}
                  class:outline={dragOverID === t.id && dragSourceID !== t.id}
                  class:outline-[var(--color-accent)]={dragOverID === t.id && dragSourceID !== t.id}
                  onclick={() => (activeTabID = t.id)}
                  onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); activeTabID = t.id; } }}
                  oncontextmenu={(e) => openTabMenu(e, t.id)}
                  ondragstart={(e) => tabDragStart(e, t.id)}
                  ondragover={(e) => tabDragOver(e, t.id)}
                  ondragend={tabDragEnd}
                  ondrop={(e) => e.preventDefault()}
                >
                  {#if isActive}
                    <span class="h-1.5 w-1.5 shrink-0 bg-[var(--color-accent)] phosphor-flicker shadow-[0_0_4px_var(--color-accent)]"></span>
                  {:else}
                    <span class="h-1 w-1 shrink-0 bg-[var(--color-text-4)]"></span>
                  {/if}
                  <span class="truncate">{label}</span>
                  <span
                    role="button"
                    tabindex="0"
                    class="ml-auto shrink-0 p-0.5 opacity-0 group-hover:opacity-40 hover:!opacity-100 hover:text-[var(--color-danger)]"
                    onclick={(e) => { e.stopPropagation(); closeTab(t.id); }}
                    onkeydown={(e) => { if (e.key === 'Enter') { e.stopPropagation(); closeTab(t.id); } }}
                  ><X size="9" /></span>
                </div>
              {/each}
              <button
                class="ml-1 flex h-6 w-6 shrink-0 items-center justify-center border border-[var(--color-line)] text-[var(--color-text-4)] hover:border-[var(--color-accent)]/40 hover:text-[var(--color-accent)] transition-colors"
                onclick={newTab}
                title="New terminal (⌘T)"
              >
                <Plus size="10" />
              </button>
            </div>
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
        {:else if app.view === 'exec'}
          <ExecPanel />
        {:else if app.view === 'files'}
          <SFTPPanel />
        {:else if app.view === 'metrics'}
          <MetricsPanel />
        {:else if app.view === 'logs'}
          <LogsPanel />
        {:else if app.view === 'forwards'}
          <ForwardsPanel />
        {:else if app.view === 'recordings'}
          <RecordingsPanel />
        {:else if app.view === 'containers'}
          <ContainersPanel />
        {:else if app.view === 'network'}
          <NetworkPanel />
        {:else if app.view === 'processes'}
          <ProcessesPanel />
        {:else if app.view === 'http'}
          <HTTPPanel />
        {:else if app.view === 'database'}
          {#await loadDBPanel() then DBPanel}
            <DBPanel />
          {/await}
        {:else if app.view === 'snippets'}
          <SnippetsPanel />
        {:else if app.view === 'history'}
          <HistoryPanel />
        {:else if app.view === 'topology'}
          <TopologyPanel />
        {:else if app.view === 'activity'}
          <ActivityPanel />
        {:else if app.view === 'plugins'}
          <PluginsPanel />
        {:else if typeof app.view === 'string' && app.view.startsWith('plugin:')}
          {@const parts = (app.view as string).split(':')}
          {@const pluginID = parts[1]}
          {@const panelID = parts[2]}
          {@const found = app.pluginPanels.find((p) => p.pluginId === pluginID && p.id === panelID)}
          {#if found}
            <iframe title={found.title} class="h-full w-full border-0 bg-transparent" sandbox="allow-scripts" srcdoc={found.html}></iframe>
          {:else}
            <div class="flex h-full items-center justify-center text-xs text-[var(--color-text-3)]">Plugin panel not loaded.</div>
          {/if}
        {:else if app.view === 'keys'}
          <KeysPanel />
        {:else if app.view === 'settings'}
          <SettingsPanel />
        {/if}
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

  {#if tabMenu}
    <div
      class="fixed inset-0 z-40"
      role="presentation"
      onclick={closeTabMenu}
      oncontextmenu={(e) => { e.preventDefault(); closeTabMenu(); }}
    ></div>
    <div
      class="fade-up fixed z-50 min-w-[160px] overflow-hidden rounded-lg border hairline-strong surface-2 py-1 text-[11px] shadow-xl shadow-black/40"
      style="left: {tabMenu.x}px; top: {tabMenu.y}px"
    >
      <button
        class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={() => { if (tabMenu) closeTab(tabMenu.tabID); closeTabMenu(); }}
      ><X size="10" class="text-[var(--color-text-4)]" /> Close tab</button>
      <button
        class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)] disabled:opacity-30"
        disabled={tabs.length <= 1}
        onclick={() => { if (tabMenu) closeOthers(tabMenu.tabID); closeTabMenu(); }}
      ><X size="10" class="text-[var(--color-text-4)]" /> Close others</button>
    </div>
  {/if}

  <!-- Status bar -->
  <footer class="flex h-6 shrink-0 items-center gap-4 border-t hairline px-3 font-mono text-[10px] text-[var(--color-text-4)]">
    <span class="flex items-center gap-1">
      <Server size="9" /> {app.hosts.length} host{app.hosts.length === 1 ? '' : 's'}
    </span>
    <span class="flex items-center gap-1">
      <TerminalSquare size="9" /> {tabs.length} tab{tabs.length === 1 ? '' : 's'}{activeLeafCount > 1 ? ` · ${activeLeafCount} panes` : ''}
    </span>
    {#if app.settings.hasAnthropicKey}
      <span class="flex items-center gap-1 text-[var(--color-accent)] opacity-60">
        <Sparkles size="9" /> AI
      </span>
    {/if}
    {#if app.broadcastEnabled}
      <span class="flex items-center gap-1 text-[var(--color-warn)]">
        <Radio size="9" class="pulse-soft" /> broadcasting
      </span>
    {/if}
    <span class="ml-auto opacity-40">v0.1-alpha</span>
  </footer>
</div>
