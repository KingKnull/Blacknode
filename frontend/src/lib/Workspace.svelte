<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import {
    VaultService,
    PluginService,
    PortForwardService,
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
  import WorkspacesMenu from "./WorkspacesMenu.svelte";
  import { captureWorkspace, parseWorkspace, readWorkspace, restoreWorkspace, SESSION_KEY, type WorkspaceSnapshot } from "./workspaces";

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
  import { pluginIDForSource, parseHostMessage } from "./pluginBridge";
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

  // Read before mounting terminals or enabling autosave, so an initial empty
  // tab cannot overwrite the saved workspace during startup.
  const previous = readWorkspace(localStorage);
  const restored = previous ? restoreWorkspace(previous, () => crypto.randomUUID()) : null;
  const firstTab = makeTab();
  let tabs = $state<Tab[]>(restored?.tabs ?? [firstTab]);
  let activeTabID = $state(restored?.activeTabID ?? firstTab.id);
  if (restored) app.sessionTargets = restored.targets;
  if (previous) {
    app.view = (app.isViewVisible(previous.view) ? previous.view : "terminals") as View;
    app.selectedHostID = previous.selectedHostID;
  }
  let forwardIDs = $state<string[]>(previous?.forwardIDs ?? []);
  let reconnectPending = $state(!!restored && (Object.values(restored.targets).some(Boolean) || !!previous?.forwardIDs.length));
  let workspaceReady = $state(false);
  let reconnectingWorkspace = $state(false);
  let sessionSaveError = $state(false);

  function capture(): WorkspaceSnapshot {
    return captureWorkspace(tabs, activeTabID, app.sessionTargets, {
      view: app.view, sidebarWidth, selectedHostID: app.selectedHostID, forwardIDs,
    });
  }

  function layoutFits(candidate: Tab[]): boolean {
    const valid = parseWorkspace(captureWorkspace(candidate, activeTabID, app.sessionTargets, {
      view: app.view, sidebarWidth, selectedHostID: app.selectedHostID, forwardIDs,
    }));
    if (!valid) app.toast("warn", "Workspace layout limit reached", "Close a tab or pane before adding another.");
    return !!valid;
  }

  function addTab(tab: Tab): boolean {
    if (!layoutFits([...tabs, tab])) return false;
    tabs.push(tab);
    activeTabID = tab.id;
    return true;
  }

  $effect(() => {
    const data = JSON.stringify(capture());
    // Debounce splitter drags while still tracking all nested pane changes.
    const timer = setTimeout(() => {
      try { localStorage.setItem(SESSION_KEY, data); sessionSaveError = false; }
      catch { sessionSaveError = true; }
    }, 150);
    const flush = () => { try { localStorage.setItem(SESSION_KEY, data); } catch { /* visible error on next save */ } };
    window.addEventListener("pagehide", flush);
    return () => { clearTimeout(timer); window.removeEventListener("pagehide", flush); };
  });

  async function reconnectWorkspace() {
    if (!workspaceReady || reconnectingWorkspace) return;
    reconnectPending = false;
    reconnectingWorkspace = true;
    try {
      for (const tab of tabs) for (const leaf of leaves(tab.root)) {
        const target = app.sessionTargets[leaf.sessionID];
        if (!target || app.sessionHosts[leaf.sessionID]) continue;
        const host = app.hosts.find((h) => h.id === target.hostID);
        if (!host) {
          app.toast("warn", "Saved host no longer exists", target.hostID);
          app.sessionTargets[leaf.sessionID] = null;
          continue;
        }
        app.requestConnect(leaf.sessionID, target.hostID, target.via);
      }
      if (forwardIDs.length) {
        const forwards = await PortForwardService.List() ?? [];
        for (const id of forwardIDs) {
          const forward = forwards.find((f) => f.id === id);
          if (!forward) { app.toast("warn", "Saved tunnel no longer exists", id); continue; }
          if (forward.active) continue;
          try { await PortForwardService.Start(id); }
          catch (e) { app.toast("error", `Could not start ${forward.name}`, String(e)); }
        }
      }
    } catch (e) { app.toast("error", "Could not restore tunnels", String(e)); }
    finally { reconnectingWorkspace = false; }
  }

  function openWorkspace(snapshot: WorkspaceSnapshot) {
    const next = restoreWorkspace(snapshot, () => crypto.randomUUID());
    app.broadcastEnabled = false;
    app.broadcastSet = new Set();
    app.pendingBroadcastDanger = null;
    app.sessionTargets = next.targets;
    tabs = next.tabs;
    activeTabID = next.activeTabID;
    sidebarWidth = snapshot.sidebarWidth;
    app.selectedHostID = snapshot.selectedHostID;
    app.view = (app.isViewVisible(snapshot.view) ? snapshot.view : "terminals") as View;
    forwardIDs = [...snapshot.forwardIDs];
    reconnectPending = true;
    void reconnectWorkspace();
  }

  let vaultLockOff: (() => void) | undefined;

  onMount(() => {
    void app.refreshAll().then(() => { workspaceReady = true; }).catch((e) => {
      app.toast("error", "Could not load workspace hosts", String(e));
    });

    // Activity tracking for vault auto-lock.
    const onActivity = () => app.touchActivity();
    window.addEventListener("keydown", onActivity, true);
    window.addEventListener("mousedown", onActivity, true);

    // Keyboard shortcuts for workspace navigation.
    //
    // Registered in the CAPTURE phase deliberately. xterm cancels every key it
    // maps to a control code, and its cancel() does preventDefault *and*
    // stopPropagation — so a bubble-phase listener never sees Ctrl+Tab (HT),
    // Ctrl+T (^T) or Ctrl+3..8 (^[ ^\ ^] ^^ ^_ ^?) while a pane is focused,
    // which is exactly where these shortcuts are supposed to work. Capture runs
    // first; each branch then stops the event so the key isn't handled twice,
    // once as a shortcut and once as terminal input.
    //
    // Which keys the app may take is a deliberate line. Bare Ctrl+T and Ctrl+W
    // belong to the shell (transpose-chars and werase) and are not bound here:
    // losing the word you were typing is one thing, closing the tab and its SSH
    // session is another. The app takes the shifted forms instead, matching
    // GNOME Terminal / Konsole / Windows Terminal. On macOS the Cmd forms need
    // no Shift — xterm maps only Cmd+A, so Meta is free.
    const onShortcut = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement)?.closest?.('[role="dialog"]')) return;
      // ? opens shortcut overlay (only when not typing in an input)
      if (e.key === '?' && !(e.target instanceof HTMLInputElement) && !(e.target instanceof HTMLTextAreaElement)) {
        e.preventDefault();
        shortcutOpen = !shortcutOpen;
        return;
      }

      const mod = e.metaKey || e.ctrlKey;
      if (!mod) return;
      const k = e.key.toLowerCase();
      const consume = () => { e.preventDefault(); e.stopPropagation(); };
      // Ctrl has to be shifted to outrank the shell; Cmd doesn't.
      const appMod = e.metaKey || e.shiftKey;

      // Ctrl+Shift+I / Cmd+Shift+I — toggle AI drawer. Bare Ctrl+I is TAB, so
      // the unshifted form could never have worked from inside a pane anyway.
      if (k === "i" && e.shiftKey) {
        consume();
        app.aiOpen = !app.aiOpen;
        return;
      }

      // Tab shortcuts only apply in terminals view
      if (app.view !== "terminals") return;

      // Ctrl+Shift+T / Cmd+T — new tab
      if (k === "t" && appMod) {
        consume();
        newTab();
        return;
      }

      // Ctrl+Shift+W / Cmd+W — close active tab
      if (k === "w" && appMod) {
        consume();
        closeTab(activeTabID);
        return;
      }

      // Ctrl+Tab / Ctrl+Shift+Tab — cycle tabs. Bare Tab is untouched (`mod` is
      // required above), so shell completion still works.
      if (e.key === "Tab") {
        consume();
        const idx = tabs.findIndex((t) => t.id === activeTabID);
        if (e.shiftKey) {
          activeTabID = tabs[(idx - 1 + tabs.length) % tabs.length].id;
        } else {
          activeTabID = tabs[(idx + 1) % tabs.length].id;
        }
        return;
      }

      // Ctrl+1-9 — jump to tab N. Read from `code`, not `key`: Shift turns "3"
      // into "#" on a US layout and parseInt would give NaN, so the shifted
      // form works too and the binding survives non-US layouts.
      //
      // This is the one place the app does take keys off the shell — Ctrl+3..8
      // are alternate encodings of ^[ ^\ ^] ^^ ^_ ^?. The canonical keys for
      // all of them (Esc, Ctrl+\ for SIGQUIT, Ctrl+], Backspace) are on other
      // keycodes and still reach the PTY untouched.
      const digit = /^Digit([1-9])$/.exec(e.code);
      if (digit) {
        consume();
        const target = tabs[Math.min(Number(digit[1]) - 1, tabs.length - 1)];
        if (target) activeTabID = target.id;
        return;
      }
    };
    window.addEventListener("keydown", onShortcut, true);

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

    // Bridge for plugin iframe → host backchannel. Iframes post messages to
    // the parent window; we identify the sender, allow-list the method, and
    // route it through the matching service. Anything else is dropped.
    //
    // The plugin id comes from the sending window, not from the message: a
    // sandboxed panel can put any id it likes in the body, and the backend
    // permission check is only worth anything if the id it checks is the
    // sender's own. PanelRouter registers each panel's window; see
    // pluginBridge.ts for why origin checking cannot do this job.
    const onPluginMessage = (e: MessageEvent) => {
      const pluginID = pluginIDForSource(e.source);
      if (!pluginID) return;
      const msg = parseHostMessage(e.data);
      if (!msg) return;
      switch (msg.type) {
        case "host.notify":
          // Still denied host-side if the manifest didn't request
          // host.notify — this call is Wails-bound and reachable without us.
          PluginService.HostNotify(pluginID, msg.title, msg.body);
          break;
        // Add more allowlisted methods here as the SDK grows, and a matching
        // permission in internal/plugin/permissions.go for each.
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
      window.removeEventListener("keydown", onShortcut, true);
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
    if (!addTab(t)) return;
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
    if (!addTab(t)) return;
    const sid = leaves(t.root)[0]?.sessionID;
    if (!sid) return;
    app.requestConnect(sid, hostID, "mosh");
  }

  onDestroy(() => {
    // Flush before vault lock unmounts the workspace and tears down its panes.
    try { localStorage.setItem(SESSION_KEY, JSON.stringify(capture())); } catch { /* save error already surfaced */ }
    vaultLockOff?.();
  });

  function newTab() {
    const t = makeTab();
    addTab(t);
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
    const root = splitLeaf(t.root, leafID, direction);
    if (layoutFits(tabs.map((tab) => tab.id === t.id ? { ...tab, root } : tab))) t.root = root;
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
    if (!addTab(tab)) return;

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
      { id: "runbooks", label: "Runbooks", Icon: Bookmark },
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
  let visibleSections = $derived(SECTIONS.map((s) => ({ ...s, views: s.views.filter((v) => app.isViewVisible(v.id)) })).filter((s) => s.views.length));

  // Plugin panels become extra tabs under the Plugins section.
  let sectionViews = $derived.by<ViewDef[]>(() => {
    // An explicitly opened hidden tool remains visible for this visit.
    const visible = activeSection.views.filter((v) => app.isViewVisible(v.id) || v.id === app.view);
    if (activeSection.id !== "plugins") return visible;
    const pluginTabs: ViewDef[] = app.pluginPanels.map((p) => ({
      id: `plugin:${p.pluginId}:${p.id}` as View,
      label: p.title,
      Icon: Puzzle,
    }));
    return [...visible, ...pluginTabs];
  });

  function selectSection(s: Section) {
    const last = lastViewPerSection[s.id];
    app.view = last && app.isViewVisible(last) ? last : s.views[0].id;
  }

  // Breadcrumb should say what you're looking at ("Terminals"), not what the
  // router calls it ("terminals"). Resolve the active view against SECTIONS;
  // plugin panels fall back to their registered title.
  let activeViewDef = $derived.by<ViewDef | null>(() => {
    for (const s of SECTIONS) {
      const v = s.views.find((v) => v.id === app.view);
      if (v) return v;
    }
    if (typeof app.view === "string" && app.view.startsWith("plugin:")) {
      const title = app.pluginPanels.find((p) => app.view === `plugin:${p.pluginId}:${p.id}`)?.title;
      return { id: app.view, label: title ?? app.view, Icon: Puzzle };
    }
    return null;
  });

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
    const target = leaves(t.root).map((l) => app.sessionTargets[l.sessionID]).find(Boolean);
    if (target) return app.hosts.find((h) => h.id === target.hostID)?.name ?? "Saved host";
    const idx = tabs.indexOf(t) + 1;
    return `local-${idx}`;
  }

  let sidebarWidth = $state(previous?.sidebarWidth ?? 252);
  let isResizing = $state(false);
  let shortcutOpen = $state(false);

  function startResize(e: MouseEvent) {
    isResizing = true;
    e.preventDefault();
  }

  function onMouseMove(e: MouseEvent) {
    if (!isResizing) return;
    sidebarWidth = Math.max(160, Math.min(600, e.clientX - 64));
  }

  function onMouseUp() {
    if (isResizing) {
      isResizing = false;
      localStorage.setItem('blacknode.sidebar-width', sidebarWidth.toString());
    }
  }
</script>

<svelte:window onmousemove={onMouseMove} onmouseup={onMouseUp} />

<div class="flex h-full w-full flex-col bg-[var(--color-surface-0)] text-[var(--color-text-1)]">
  <!-- ── TOP BAR ─────────────────────────────────────────────────────── -->
  <header class="relative flex h-12 shrink-0 items-center gap-3 border-b hairline surface-1 px-4">
    <div class="flex items-center gap-2 select-none">
      <div class="flex h-7 w-7 items-center justify-center border border-[var(--color-accent)]/35 bg-[var(--color-accent-soft)]" style="border-radius: var(--radius-sm);">
        <Logo size={17} />
      </div>
      <span class="hidden font-mono type-micro font-semibold tracking-[0.16em] text-[var(--color-text-2)] sm:inline">BLACKNODE</span>
    </div>

    <div class="h-5 w-px bg-[var(--color-line-strong)]"></div>

    <!-- Breadcrumb -->
    <span class="flex items-center gap-2 type-caption font-medium text-[var(--color-text-1)]">
      {#if activeViewDef}
        <activeViewDef.Icon size="14" strokeWidth={1.8} class="text-[var(--color-accent)]" />
        {activeViewDef.label}
      {:else}
        {app.view}
      {/if}
    </span>

    <div class="ml-auto flex items-center gap-1.5 type-caption">
      {#if workspaceReady}
        <WorkspacesMenu {capture} onopen={openWorkspace} {forwardIDs} onforwards={(ids) => forwardIDs = ids} disabled={reconnectingWorkspace} />
      {/if}
      <!-- Broadcast -->
      <button
        class="flex items-center gap-1.5 border px-2.5 py-1 rounded-sm transition-all {app.broadcastEnabled
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
        class="flex items-center gap-1.5 border px-2.5 py-1 rounded-sm transition-all {app.aiOpen
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
        class="flex items-center gap-1.5 border border-[var(--color-line)] px-2.5 py-1 rounded-sm text-[var(--color-text-3)] transition-all hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)]"
        onclick={() => (app.paletteOpen = true)}
        title="Command palette (⌘K)"
      >
        <Command size="11" />
        <span>Palette</span>
        <kbd class="font-mono border border-[var(--color-line-strong)] bg-[var(--color-surface-2)] px-1.5 py-0.5 type-micro text-[var(--color-text-3)]">⌘K</kbd>
      </button>

      <div class="mx-1 h-3 w-px bg-[var(--color-line-strong)]"></div>

      <!-- Vault lock -->
      <button
        class="flex items-center gap-1.5 border border-[var(--color-line)] px-2.5 py-1 rounded-sm text-[var(--color-text-3)] hover:border-[var(--color-accent)]/30 hover:text-[var(--color-accent)]"
        onclick={lockVault}
        title="Vault unlocked — click to lock"
      >
        <Lock size="11" class="text-[var(--color-accent)]" />
        <span class="text-[var(--color-accent)]">Lock vault</span>
      </button>
    </div>
  </header>

  <!-- ── BODY ─────────────────────────────────────────────────────────── -->
  <div class="grid flex-1 overflow-hidden" style="grid-template-columns: 64px {sidebarWidth}px 1fr">
    <!-- ── SECTION RAIL ─────────────────────────────── -->
    <NavRail sections={visibleSections} activeSectionId={activeSection.id} onSelect={(id) => selectSection(visibleSections.find((s) => s.id === id)!)} />

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
        {#if reconnectPending}
          <div class="flex shrink-0 items-center gap-3 border-b hairline surface-2 px-3 py-2 type-caption">
            <span>Workspace restored. Reconnect to reopen its saved hosts and tunnels.</span>
            <button class="ml-auto rounded border border-[var(--color-accent)]/40 bg-[var(--color-accent-soft)] px-2.5 py-1.5 font-medium text-[var(--color-accent)] disabled:opacity-40" disabled={!workspaceReady} onclick={reconnectWorkspace}>Reconnect workspace</button>
            <button class="rounded border hairline px-2.5 py-1.5 text-[var(--color-text-2)] hover:border-[var(--color-line-strong)]" onclick={() => { reconnectPending = false; for (const tab of tabs) for (const leaf of leaves(tab.root)) if (!app.sessionHosts[leaf.sessionID]) app.sessionTargets[leaf.sessionID] = null; forwardIDs = []; }}>Use local shells</button>
          </div>
        {/if}
        {#if sessionSaveError}<p role="alert" class="px-3 py-2 type-caption text-[var(--color-danger)]">Workspace changes could not be saved. Local storage may be full.</p>{/if}
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
                    activeLeafID={activeTabID === t.id ? t.activeLeafID : null}
                    leafCount={leaves(t.root).length}
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
      body={`“${p.command}” matches the pattern “${p.danger.matched}” and will run on ${p.targets} other pane${p.targets === 1 ? "" : "s"} at once. It has already run in the pane you typed it in.`}
      severity={p.danger.level}
      productionHosts={p.productionHosts}
      requirePhrase={p.danger.level === "block-without-confirm"
        ? p.productionHosts.length > 0
          ? "destroy production"
          : "I understand"
        : undefined}
      allowEnterConfirm={false}
      onCancel={() => app.cancelBroadcastDanger()}
      onConfirm={() => app.confirmBroadcastDanger()}
    />
  {/if}

  <!-- ── STATUS BAR ──────────────────────────────────────────────── -->
  <StatusBar tabCount={tabs.length} {activeLeafCount} />
</div>
