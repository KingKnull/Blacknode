<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { VaultService } from "../../bindings/github.com/blacknode/blacknode";
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
  import SnippetsPanel from "./SnippetsPanel.svelte";
  import HistoryPanel from "./HistoryPanel.svelte";
  import SettingsPanel from "./SettingsPanel.svelte";
  import Palette from "./Palette.svelte";
  import AIDrawer from "./AIDrawer.svelte";
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
    Bookmark,
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

  let tabs = $state<Tab[]>([makeTab()]);
  let activeTabID = $state(tabs[0].id);

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

    return () => {
      window.removeEventListener("keydown", onActivity, true);
      window.removeEventListener("mousedown", onActivity, true);
      window.removeEventListener("keydown", onShortcut);
      window.removeEventListener(
        "blacknode:insert-into-active-terminal",
        onInsertReq as EventListener,
      );
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
    { id: "snippets", label: "Snippets", Icon: Bookmark },
    { id: "history", label: "History", Icon: HistoryIcon },
    { id: "keys", label: "Keys", Icon: KeyRound },
    { id: "settings", label: "Settings", Icon: SettingsIcon },
  ];

  let activeTab = $derived(tabs.find((t) => t.id === activeTabID));
  let activeLeafCount = $derived(activeTab ? leaves(activeTab.root).length : 0);
</script>

<div
  class="flex h-screen w-screen flex-col bg-[var(--color-surface-0)] text-[var(--color-text-1)]"
>
  <!-- Top bar -->
  <header
    class="relative flex items-center gap-3 border-b hairline surface-1 px-3 py-2"
  >
    <div
      class="absolute inset-x-0 -bottom-px h-px bg-gradient-to-r from-transparent via-[var(--color-accent)]/40 to-transparent"
    ></div>
    <Logo size={22} />

    <div class="ml-auto flex items-center gap-2 text-[11px]">
      <button
        class="flex items-center gap-1.5 rounded-md border px-2 py-1 transition-colors {app.broadcastEnabled
          ? 'border-[var(--color-warn)]/50 bg-[var(--color-warn)]/15 text-[var(--color-warn)]'
          : 'hairline surface-2 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]'}"
        onclick={() => {
          if (!app.broadcastEnabled && app.broadcastSet.size === 0) {
            alert(
              'Broadcast is on but no panes are in the group yet.\n\nClick the "cast" button on each pane you want to broadcast to.\n\nKeystrokes typed in any group member will be sent to every other member — be very careful with destructive commands.',
            );
          }
          app.broadcastEnabled = !app.broadcastEnabled;
        }}
        title={app.broadcastEnabled
          ? `Broadcasting to ${app.broadcastSet.size} pane${app.broadcastSet.size === 1 ? "" : "s"}`
          : "Enable multi-pane keystroke broadcast"}
      >
        <Radio
          size="11"
          class={app.broadcastEnabled ? "pulse-soft" : ""}
        />
        <span>broadcast</span>
        {#if app.broadcastEnabled}
          <span
            class="ml-1 rounded bg-[var(--color-warn)]/30 px-1 font-mono text-[9px]"
            >{app.broadcastSet.size}</span
          >
        {/if}
      </button>
      <button
        class="flex items-center gap-1.5 rounded-md border hairline px-2 py-1 surface-2 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={() => (app.aiOpen = !app.aiOpen)}
        title="AI assistant (⌘I / Ctrl+I)"
      >
        <Sparkles size="11" class={app.aiOpen ? "text-[var(--color-accent)]" : ""} />
        <span>AI</span>
        <kbd
          class="ml-1 rounded border hairline px-1 font-mono text-[9px] text-[var(--color-text-4)]"
          >⌘I</kbd
        >
      </button>
      <button
        class="flex items-center gap-1.5 rounded-md border hairline px-2 py-1 surface-2 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={() => (app.paletteOpen = true)}
        title="Command palette (⌘K / Ctrl+K)"
      >
        <Command size="11" />
        <span>Quick actions</span>
        <kbd
          class="ml-1 rounded border hairline px-1 font-mono text-[9px] text-[var(--color-text-4)]"
          >⌘K</kbd
        >
      </button>
      <div
        class="flex items-center gap-1.5 rounded-md border hairline px-2 py-1 surface-2 text-[var(--color-text-2)]"
      >
        <Unlock size="11" class="text-[var(--color-accent)]" />
        <span>vault unlocked</span>
      </div>
      <button
        class="flex items-center gap-1.5 rounded-md px-2 py-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={lockVault}
        title="Lock vault"
      >
        <Lock size="11" />
        <span>lock</span>
      </button>
    </div>
  </header>

  <!-- Body -->
  <div class="grid flex-1 grid-cols-[44px_280px_1fr] overflow-hidden">
    <!-- Icon nav -->
    <nav
      class="flex flex-col items-center gap-1 border-r hairline surface-1 py-2"
    >
      {#each VIEWS as v (v.id)}
        <button
          title={v.label}
          class="group relative flex h-9 w-9 items-center justify-center rounded-md transition-colors {app.view ===
          v.id
            ? 'text-[var(--color-accent)]'
            : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]'}"
          onclick={() => (app.view = v.id)}
        >
          {#if app.view === v.id}
            <span
              class="absolute left-0 top-1.5 bottom-1.5 w-0.5 rounded-r bg-[var(--color-accent)]"
            ></span>
          {/if}
          <v.Icon size="16" />
        </button>
      {/each}
    </nav>

    <!-- Sidebar -->
    <aside class="overflow-hidden border-r hairline surface-1">
      <HostList />
    </aside>

    <!-- Main + AI drawer -->
    <div
      class="grid overflow-hidden"
      style:grid-template-columns={app.aiOpen ? "1fr 380px" : "1fr"}
    >
      <main class="flex flex-col overflow-hidden">
        {#if app.view === "terminals"}
          <div class="flex h-full flex-col">
            <div
              class="flex items-center gap-1 border-b hairline surface-1 px-2 py-1.5"
            >
              {#each tabs as t (t.id)}
                <button
                  class="group flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[11px] transition-colors {activeTabID ===
                  t.id
                    ? 'bg-[var(--color-surface-3)] text-[var(--color-text-1)]'
                    : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-1)]'}"
                  onclick={() => (activeTabID = t.id)}
                >
                  <TerminalSquare size="11" />
                  <span class="font-mono">{t.id.slice(0, 6)}</span>
                  <span
                    role="button"
                    tabindex="0"
                    class="rounded p-0.5 opacity-50 group-hover:opacity-100 hover:bg-[var(--color-surface-4)]"
                    onclick={(e) => {
                      e.stopPropagation();
                      closeTab(t.id);
                    }}
                    onkeydown={(e) => {
                      if (e.key === "Enter") {
                        e.stopPropagation();
                        closeTab(t.id);
                      }
                    }}
                  >
                    <X size="10" />
                  </span>
                </button>
              {/each}
              <button
                class="ml-1 flex h-6 w-6 items-center justify-center rounded-md text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
                onclick={newTab}
                title="New terminal (⌘T)"
              >
                <Plus size="12" />
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
                    onresize={(splitID, ratio) =>
                      onResize(t.id, splitID, ratio)}
                  />
                </div>
              {/each}
            </div>
          </div>
        {:else if app.view === "exec"}
          <ExecPanel />
        {:else if app.view === "files"}
          <SFTPPanel />
        {:else if app.view === "metrics"}
          <MetricsPanel />
        {:else if app.view === "logs"}
          <LogsPanel />
        {:else if app.view === "forwards"}
          <ForwardsPanel />
        {:else if app.view === "recordings"}
          <RecordingsPanel />
        {:else if app.view === "containers"}
          <ContainersPanel />
        {:else if app.view === "network"}
          <NetworkPanel />
        {:else if app.view === "processes"}
          <ProcessesPanel />
        {:else if app.view === "snippets"}
          <SnippetsPanel />
        {:else if app.view === "history"}
          <HistoryPanel />
        {:else if app.view === "keys"}
          <KeysPanel />
        {:else if app.view === "settings"}
          <SettingsPanel />
        {/if}
      </main>

      {#if app.aiOpen}
        <AIDrawer onInsertCommand={aiInsert} />
      {/if}
    </div>
  </div>

  <Palette onNewTab={newTab} />
  <Toaster />

  <!-- Status bar -->
  <footer
    class="flex items-center gap-3 border-t hairline surface-1 px-3 py-1 text-[10px] text-[var(--color-text-3)]"
  >
    <span class="flex items-center gap-1">
      <Server size="10" /> {app.hosts.length} hosts
    </span>
    <span class="flex items-center gap-1">
      <KeyRound size="10" /> {app.keys.length} keys
    </span>
    <span class="flex items-center gap-1">
      <TerminalSquare size="10" /> {tabs.length} tabs · {activeLeafCount} panes
    </span>
    <span class="flex items-center gap-1">
      <Sparkles
        size="10"
        class={app.settings.hasAnthropicKey
          ? "text-[var(--color-accent)]"
          : "text-[var(--color-text-4)]"}
      />
      AI {app.settings.hasAnthropicKey ? "ready" : "not configured"}
    </span>
    {#if app.broadcastEnabled}
      <span class="flex items-center gap-1 text-[var(--color-warn)]">
        <Radio size="10" class="pulse-soft" />
        BROADCASTING to {app.broadcastSet.size} pane{app.broadcastSet.size === 1 ? "" : "s"}
      </span>
    {/if}
    <span class="ml-auto font-mono opacity-60">v0.1 · alpha</span>
  </footer>
</div>
