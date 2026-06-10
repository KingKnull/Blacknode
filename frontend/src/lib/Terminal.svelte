<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { WebLinksAddon } from "@xterm/addon-web-links";
  import { SearchAddon } from "@xterm/addon-search";
  import { WebglAddon } from "@xterm/addon-webgl";
  import {
    LocalShellService,
    SSHService,
    HostService,
    SnippetService,
  } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Snippet } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { focus } from "./actions";
  import { app } from "./state.svelte";
  import { bus } from "./events";
  import SnippetApplyDialog from "./SnippetApplyDialog.svelte";
  import { envBadge } from "./envColor";
  import {
    TerminalIcon,
    Server,
    Plug,
    Unplug,
    Loader2,
    Lock,
    AlertTriangle,
    Circle,
    Radio,
    Activity,
    ShieldCheck,
    Search,
    ChevronUp,
    ChevronDown,
    X,
    BookmarkIcon,
    Wand,
  } from "@lucide/svelte";

  type Props = { sessionID: string };
  let { sessionID }: Props = $props();

  type Mode = "local" | "remote";
  type Status = "starting" | "running" | "connecting" | "connected" | "idle" | "error";

  let mode: Mode = $state("local");
  let status: Status = $state("starting");
  let errorMsg = $state("");
  let connectedHostID = $state<string | null>(null);
  let promptingPassword = $state(false);
  let runtimePassword = $state("");
  let showHostPicker = $state(false);
  
  let promptingTofu = $state(false);
  let tofuPayload = $state<{host: string, port: number, keyType: string, presentedFp: string, presentedKey: string} | null>(null);

  // Splash screen tracking
  let hasTyped = $state(false);

  // Auto-reconnect state
  let reconnecting = $state(false);
  let reconnectAttempt = $state(0);
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined;
  const MAX_RECONNECT_ATTEMPTS = 3;

  // Debounce helper
  let resizeDebounce: ReturnType<typeof setTimeout> | undefined;

  let containerEl: HTMLDivElement | undefined = $state();
  let term: Terminal | undefined;
  let fit: FitAddon | undefined;
  let searchAddon: SearchAddon | undefined;
  let dataOff: (() => void) | undefined;
  let exitOff: (() => void) | undefined;
  let resizeObs: ResizeObserver | undefined;
  // Latency state — only populated for connected SSH sessions, polled every
  // 5s. Null means "not measured yet" or "ping failed".
  let latencyMs = $state<number | null>(null);
  let latencyTimer: ReturnType<typeof setInterval> | undefined;

  // ── Sudo prompt detection ──────────────────────────────────────────
  // Watches terminal output for sudo-style password prompts and shows
  // a floating pill the user can click to auto-type the stored password.
  let showSudoPill = $state(false);
  let sudoPillTimer: ReturnType<typeof setTimeout> | undefined;
  let sudoInlineInput = $state(false);
  let sudoInlinePassword = $state("");

  // ── Search state ───────────────────────────────────────────────────
  let showSearch = $state(false);
  let searchQuery = $state("");
  let searchResults = $state({ resultIndex: 0, resultCount: 0 });

  // ── Snippets state ─────────────────────────────────────────────────
  let showSnippets = $state(false);
  let snippets = $state<Snippet[]>([]);
  let applyingSnippet: Snippet | null = $state(null);

  async function loadSnippets() {
    if (snippets.length === 0) {
      snippets = ((await SnippetService.List()) ?? []) as Snippet[];
    }
  }

  const SUDO_PATTERNS = [
    /\[sudo\] password for/i,
    /Password:\s*$/i,
    /password for \S+/i,
    /\(current\) UNIX password:/i,
    /Password for \S+@/i,
  ];

  function checkForSudoPrompt(data: string) {
    for (const re of SUDO_PATTERNS) {
      if (re.test(data)) {
        triggerSudoPill();
        return;
      }
    }
  }

  function triggerSudoPill() {
    if (sudoPillTimer) clearTimeout(sudoPillTimer);
    showSudoPill = true;
    // Auto-dismiss after 20 seconds if user doesn't interact.
    sudoPillTimer = setTimeout(() => {
      showSudoPill = false;
      sudoInlineInput = false;
    }, 20_000);
  }

  function dismissSudoPill() {
    if (sudoPillTimer) clearTimeout(sudoPillTimer);
    showSudoPill = false;
    sudoInlineInput = false;
  }

  function getSudoPassword(): string | null {
    // For remote sessions, use the connected host's sudo password.
    if (mode === "remote" && connectedHostID) {
      return app.hostSudoPasswords[connectedHostID] || null;
    }
    // For local sessions, use the special "local" host ID.
    if (mode === "local") {
      return app.hostSudoPasswords["local"] || null;
    }
    return null;
  }

  function sendSudoPassword() {
    const pw = getSudoPassword();
    if (!pw) {
      // No stored password — show inline input.
      sudoInlineInput = true;
      return;
    }
    writeLocal(pw + "\n");
    dismissSudoPill();
  }

  function sendInlineSudoPassword() {
    if (!sudoInlinePassword) return;
    writeLocal(sudoInlinePassword + "\n");
    // Permanently save to the secure vault and update session state
    if (mode === "remote" && connectedHostID) {
      void HostService.SetSudoPassword(connectedHostID, sudoInlinePassword);
      app.setSudoPassword(connectedHostID, sudoInlinePassword);
    } else if (mode === "local") {
      void HostService.SetSudoPassword("local", sudoInlinePassword);
      app.setSudoPassword("local", sudoInlinePassword);
    }
    sudoInlinePassword = "";
    dismissSudoPill();
  }

  // When the AI drawer asks us to insert a command, write it to the active
  // session (local PTY or SSH stdin). Only the matching session reacts.
  $effect(() => {
    const p = app.pendingTerminalInsert;
    if (!p || p.sessionID !== sessionID) return;
    if (mode === "local" && status === "running") {
      void LocalShellService.Write(sessionID, p.text);
    } else if (mode === "remote" && status === "connected") {
      void SSHService.Write(sessionID, p.text);
    }
    // Clear so it can't fire twice.
    app.pendingTerminalInsert = null;
  });

  // Keep global app state updated with which hosts have active connections.
  $effect(() => {
    if (mode === "remote" && connectedHostID) {
      if (status === "connected") {
        app.addConnectedHost(connectedHostID);
      } else {
        app.removeConnectedHost(connectedHostID);
      }
    }
  });

  // Pick the xterm palette to match the app theme. We don't hot-swap — if
  // the user toggles theme, existing sessions keep the theme they spawned
  // with; new sessions pick up the new theme.
  function termTheme() {
    if (app.settings.theme === "light") {
      return {
        background: "#f5f2eb",
        foreground: "#1a1208",
        cursor: "#059669",
        cursorAccent: "#f5f2eb",
        selectionBackground: "rgba(5, 150, 105, 0.18)",
        black: "#2a2010",
        brightBlack: "#6b5e42",
        red: "#9b1c1c",
        brightRed: "#7f1d1d",
        green: "#15803d",
        brightGreen: "#166534",
        yellow: "#92400e",
        brightYellow: "#78350f",
        blue: "#1e3a8a",
        brightBlue: "#1e40af",
        magenta: "#6b21a8",
        brightMagenta: "#581c87",
        cyan: "#0e7490",
        brightCyan: "#0891b2",
        white: "#6b5e42",
        brightWhite: "#1a1208",
      };
    }
    return {
      background: "#020304",
      foreground: "#c8ffe8",
      cursor: "#00ff88",
      cursorAccent: "#020304",
      selectionBackground: "rgba(0, 255, 136, 0.18)",
      black: "#020304",
      brightBlack: "#1f4035",
      red: "#ff3c3c",
      brightRed: "#ff6b6b",
      green: "#00ff88",
      brightGreen: "#4dffa8",
      yellow: "#ffaa00",
      brightYellow: "#ffcc44",
      blue: "#00aaff",
      brightBlue: "#44ccff",
      magenta: "#cc44ff",
      brightMagenta: "#dd88ff",
      cyan: "#00ffcc",
      brightCyan: "#44ffee",
      white: "#6bbf99",
      brightWhite: "#c8ffe8",
    };
  }

  onMount(() => {
    term = new Terminal({
      fontFamily: '"JetBrains Mono Variable", "Cascadia Mono", Menlo, Consolas, monospace',
      fontSize: 13,
      lineHeight: 1.25,
      letterSpacing: 0,
      cursorBlink: true,
      cursorStyle: "bar",
      allowProposedApi: true,
      scrollback: 5000,
      theme: termTheme(),
    });
    fit = new FitAddon();
    searchAddon = new SearchAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    term.loadAddon(searchAddon);

    searchAddon.onDidChangeResults((e) => {
      searchResults = e;
    });

    term.open(containerEl!);
    fit.fit();

    // GPU-accelerated rendering via WebGL. Falls back to canvas if unavailable.
    try {
      const webgl = new WebglAddon();
      webgl.onContextLoss(() => { webgl.dispose(); });
      term.loadAddon(webgl);
    } catch {
      // WebGL not available — canvas renderer is fine
    }

    term.attachCustomKeyEventHandler((e) => {
      // Ctrl+Shift+S → instant sudo password auto-fill.
      if (e.type === "keydown" && e.ctrlKey && e.shiftKey && e.key.toLowerCase() === "s") {
        e.preventDefault();
        sendSudoPassword();
        return false;
      }
      // Ctrl+Shift+F → terminal search
      if (e.type === "keydown" && e.ctrlKey && e.shiftKey && e.key.toLowerCase() === "f") {
        e.preventDefault();
        showSearch = true;
        return false;
      }
      return true;
    });

    term.onData((d) => {
      hasTyped = true;
      writeLocal(d);
      // If broadcast is on and we're in the group, fan out to siblings.
      app.fanOutBroadcast(sessionID, d);
      // Dismiss sudo pill on any manual typing (user is handling it).
      if (showSudoPill && !sudoInlineInput) dismissSudoPill();
    });

    // Register a sink so other terminals can broadcast keystrokes into us
    // without knowing whether we're a local PTY or an SSH session.
    app.registerBroadcastSink(sessionID, writeLocal);
    term.onResize(({ cols, rows }) => {
      if (mode === "local" && status === "running") void LocalShellService.Resize(sessionID, cols, rows);
      if (mode === "remote" && status === "connected") void SSHService.Resize(sessionID, cols, rows);
    });

    // Debounced resize — prevents 50+ resize events during window drag.
    resizeObs = new ResizeObserver(() => {
      clearTimeout(resizeDebounce);
      resizeDebounce = setTimeout(() => fit?.fit(), 80);
    });
    resizeObs.observe(containerEl!);

    dataOff = Events.On("terminal:data", (e: any) => {
      const p = e?.data;
      if (!p || p.sessionID !== sessionID) return;
      term?.write(p.data);
      // Check for sudo prompts in the output.
      checkForSudoPrompt(p.data);
    });
    exitOff = Events.On("terminal:exit", (e: any) => {
      const p = e?.data;
      if (!p || p.sessionID !== sessionID) return;
      const reason = p.reason ?? "";
      term?.writeln(`\r\n\x1b[90m[session closed: ${reason}]\x1b[0m`);
      if (mode === "remote" && connectedHostID) {
        // Auto-reconnect for remote sessions (network drops, keepalive timeout)
        const hostID = connectedHostID;
        connectedHostID = null;
        stopLatencyPolling();
        if (reconnectAttempt < MAX_RECONNECT_ATTEMPTS) {
          reconnecting = true;
          const delay = Math.pow(2, reconnectAttempt + 1) * 1000; // 2s, 4s, 8s
          reconnectAttempt++;
          term?.writeln(`\x1b[33m[reconnecting in ${delay / 1000}s... attempt ${reconnectAttempt}/${MAX_RECONNECT_ATTEMPTS}]\x1b[0m`);
          reconnectTimer = setTimeout(async () => {
            try {
              await actuallyConnect(hostID);
              reconnecting = false;
              reconnectAttempt = 0;
              term?.writeln(`\x1b[32m[reconnected]\x1b[0m`);
            } catch {
              // actuallyConnect sets status = "error" on failure,
              // and the next terminal:exit will trigger another attempt
              reconnecting = false;
              status = "error";
              errorMsg = "Reconnection failed";
            }
          }, delay);
        } else {
          reconnecting = false;
          reconnectAttempt = 0;
          status = "idle";
        }
      } else {
        status = "idle";
      }
    });

    void openLocal();

    // Listen for tile-grid auto-connect events from Workspace.
    const offAutoConnect = bus.on('connect-terminal-to-host', (detail) => {
      if (detail.sessionID === sessionID) {
        switchToRemote(detail.hostID);
      }
    });

    return () => {
      offAutoConnect();
    };
  });

  onDestroy(() => {
    dataOff?.();
    exitOff?.();
    resizeObs?.disconnect();
    stopLatencyPolling();
    if (sudoPillTimer) clearTimeout(sudoPillTimer);
    if (reconnectTimer) clearTimeout(reconnectTimer);
    clearTimeout(resizeDebounce);
    app.unregisterBroadcastSink(sessionID);
    term?.dispose();
    if (mode === "local" && status === "running") void LocalShellService.Close(sessionID);
    if (mode === "remote" && status === "connected") void SSHService.Disconnect(sessionID);
  });

  // Single write path the terminal and the broadcast bus both call. Picks
  // the right backend (local PTY vs SSH stdin) based on current mode/status.
  function writeLocal(d: string) {
    if (mode === "local" && status === "running") {
      void LocalShellService.Write(sessionID, d);
    } else if (mode === "remote" && status === "connected") {
      void SSHService.Write(sessionID, d);
    }
  }

  function toggleBroadcastMember() {
    app.toggleBroadcastMember(sessionID);
  }

  let inBroadcast = $derived(app.broadcastSet.has(sessionID));
  let broadcastActive = $derived(app.broadcastEnabled && inBroadcast);

  async function openLocal() {
    status = "starting";
    errorMsg = "";
    try {
      await LocalShellService.Open(sessionID, term?.cols ?? 80, term?.rows ?? 24);
      mode = "local";
      status = "running";
      term?.focus();
    } catch (e: any) {
      status = "error";
      errorMsg = String(e?.message ?? e);
    }
  }

  async function switchToRemote(hostID: string) {
    showHostPicker = false;
    const host = app.hosts.find((h) => h.id === hostID);
    if (!host) return;
    if ((host.environment ?? "").toLowerCase() === "production") {
      const ok = confirm(
        `⚠️ ${host.name} is tagged PRODUCTION.\n\nConnect anyway?`,
      );
      if (!ok) return;
    }
    if (mode === "local" && status === "running") {
      await LocalShellService.Close(sessionID);
    }
    app.selectedHostID = hostID;
    mode = "remote";

    if (host.authMethod === "password") {
      const cached = app.hostPasswords[host.id];
      if (!cached) {
        promptingPassword = true;
        return;
      }
      runtimePassword = cached;
    } else {
      runtimePassword = "";
    }
    await actuallyConnect(host.id);
  }

  async function submitPassword() {
    if (!runtimePassword || !app.selectedHostID) return;
    app.setPassword(app.selectedHostID, runtimePassword);
    promptingPassword = false;
    await actuallyConnect(app.selectedHostID);
  }

  async function actuallyConnect(hostID: string) {
    status = "connecting";
    errorMsg = "";
    try {
      await SSHService.ConnectByHost(
        sessionID,
        hostID,
        runtimePassword,
        term?.cols ?? 80,
        term?.rows ?? 24,
      );
      status = "connected";
      connectedHostID = hostID;
      term?.focus();
      startLatencyPolling();
    } catch (e: any) {
      let msg = String(e?.message ?? e);

      // Wails v3 wraps Go errors as Error objects whose .message is a
      // serialised JSON envelope: {"message":"…","cause":{},"kind":"…"}.
      // Unwrap to get the real Go error string inside.
      if (msg.trimStart().startsWith("{")) {
        try {
          const parsed = JSON.parse(msg);
          if (parsed.message) msg = parsed.message;
        } catch {}
      }

      if (msg.includes("UNKNOWN_HOST_KEY:")) {
        const jsonStr = msg.split("UNKNOWN_HOST_KEY:")[1];
        try {
          tofuPayload = JSON.parse(jsonStr);
          promptingTofu = true;
          status = "idle";
          return;
        } catch {}
      }
      status = "error";
      errorMsg = msg;
    }
  }

  async function approveTofu() {
    if (!tofuPayload || !app.selectedHostID) return;
    try {
      await HostService.ApproveHostKey(
        tofuPayload.host,
        tofuPayload.port,
        tofuPayload.keyType,
        tofuPayload.presentedKey,
        tofuPayload.presentedFp
      );
      promptingTofu = false;
      await actuallyConnect(app.selectedHostID);
    } catch (e: any) {
      status = "error";
      errorMsg = "TOFU approval failed: " + String(e?.message ?? e);
    }
  }

  // Polls the SSH connection's RTT every 5s. Stops when the session leaves
  // the "connected" state.
  function startLatencyPolling() {
    stopLatencyPolling();
    void measureLatency();
    latencyTimer = setInterval(() => {
      if (status !== "connected") {
        stopLatencyPolling();
        return;
      }
      void measureLatency();
    }, 5_000);
  }

  function stopLatencyPolling() {
    if (latencyTimer) {
      clearInterval(latencyTimer);
      latencyTimer = undefined;
    }
    latencyMs = null;
  }

  async function measureLatency() {
    try {
      const ms = (await SSHService.Latency(sessionID)) as number;
      latencyMs = ms;
    } catch {
      latencyMs = null;
    }
  }

  async function disconnectRemote() {
    try {
      await SSHService.Disconnect(sessionID);
    } finally {
      connectedHostID = null;
      status = "idle";
      stopLatencyPolling();
      await openLocal();
    }
  }

  let connectedHost = $derived(
    connectedHostID ? app.hosts.find((h) => h.id === connectedHostID) : null,
  );
  let connectedEnv = $derived(envBadge(connectedHost?.environment));
</script>

<div class="relative flex h-full w-full flex-col bg-[var(--color-surface-0)]">
  {#if connectedEnv.isProd && status === "connected"}
    <div
      class="flex items-center justify-center gap-1.5 border-b py-0.5 text-[10px] font-semibold uppercase tracking-[0.2em]"
      style:background={connectedEnv.bg}
      style:color={connectedEnv.color}
      style:border-color={connectedEnv.border}
    >
      <AlertTriangle size="10" />
      production session
      <AlertTriangle size="10" />
    </div>
  {/if}
  <div
    class="flex items-center gap-2 border-b hairline px-3 py-1.5 text-xs surface-1"
  >
    {#if mode === "local"}
      <TerminalIcon size="14" class="text-[var(--color-text-3)]" />
      <span class="font-mono text-[13px] font-bold tracking-wide text-[var(--color-text-1)]">local</span>
      <span class="font-mono text-[10px] text-[var(--color-text-4)]"
        >· {sessionID.slice(0, 6)}</span
      >
      {#if status === "starting"}
        <Loader2 size="12" class="animate-spin text-[var(--color-text-3)]" />
        <span class="text-[var(--color-text-3)]">starting…</span>
      {:else if status === "running"}
        <span
          class="ml-1 h-1.5 w-1.5 rounded-full bg-[var(--color-accent)] pulse-soft"
        ></span>
      {/if}
    {:else}
      <Plug size="14" class="text-[var(--color-accent)]" />
      {#if connectedHost}
        <span class="font-mono text-[13px] font-bold tracking-wide text-[var(--color-text-1)]"
          >{connectedHost.username}@{connectedHost.host}</span
        >
        <span class="font-mono text-[10px] text-[var(--color-text-4)]"
          >:{connectedHost.port}</span
        >
        <span
          class="ml-1 h-1.5 w-1.5 rounded-full bg-[var(--color-accent)] pulse-soft"
        ></span>
        {#if latencyMs !== null}
          {@const tone =
            latencyMs < 50
              ? "text-[var(--color-accent)]"
              : latencyMs < 200
                ? "text-[var(--color-warn)]"
                : "text-[var(--color-danger)]"}
          <span
            class="ml-1 inline-flex items-center gap-0.5 rounded border hairline px-1.5 py-0.5 font-mono text-[10px] {tone}"
            title="Round-trip time to the SSH server"
          >
            <Activity size="9" />
            {latencyMs}ms
          </span>
        {/if}
      {:else if status === "connecting"}
        <Loader2 size="12" class="animate-spin text-[var(--color-text-3)]" />
        <span class="text-[var(--color-text-3)]">connecting…</span>
      {/if}
      {#if reconnecting}
        <span class="ml-1 flex items-center gap-1 font-mono text-[10px] text-[var(--color-warn)]">
          <Loader2 size="10" class="animate-spin" />
          reconnecting ({reconnectAttempt}/{MAX_RECONNECT_ATTEMPTS})
        </span>
      {/if}
    {/if}

    <div class="ml-auto flex items-center gap-2 opacity-50 hover:opacity-100 transition-opacity">
      {#if errorMsg}
        <span class="truncate font-mono text-[9px] text-[var(--color-danger)]" title={errorMsg}>
          ERR: {errorMsg}
        </span>
      {/if}

      {#if app.recordingsEnabled && (status === "running" || status === "connected")}
        <span
          class="flex items-center gap-1 border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/8 px-1.5 py-px font-mono text-[9px] font-bold uppercase tracking-widest text-[var(--color-danger)]"
          title="This session is being recorded"
        >
          <Circle size="6" class="fill-[var(--color-danger)] text-[var(--color-danger)] pulse-soft" />
          REC
        </span>
      {/if}

      <button
        class="flex items-center gap-1 border px-1.5 py-px font-mono text-[9px] uppercase tracking-wider transition-all {inBroadcast
          ? broadcastActive
            ? 'border-[var(--color-warn)]/40 bg-[var(--color-warn)]/8 text-[var(--color-warn)]'
            : 'border-[var(--color-line-strong)] text-[var(--color-text-2)]'
          : 'border-[var(--color-line)] text-[var(--color-text-4)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)]'}"
        onclick={toggleBroadcastMember}
        title={inBroadcast ? "Remove this pane from the broadcast group" : "Add this pane to the broadcast group"}
      >
        <Radio size="9" class={broadcastActive ? 'pulse-soft' : ''} />
        CAST
      </button>

      <div class="relative">
        <button
          class="flex items-center gap-1 border border-[var(--color-line)] px-2 py-0.5 font-mono text-[9px] uppercase tracking-wider text-[var(--color-text-3)] transition-all hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)]"
          onclick={() => { showSnippets = !showSnippets; if (showSnippets) loadSnippets(); }}
          title="Insert Snippet"
        >
          <BookmarkIcon size="10" />
          SNIPPET
        </button>

      {#if showSnippets}
        <div class="absolute right-0 top-full z-50 mt-1 w-64 border hairline-strong surface-2 shadow-2xl fade-up" style="backdrop-filter: blur(12px) saturate(1.2); box-shadow: 0 4px 20px rgba(0,0,0,0.5);">
          <div class="border-b hairline px-3 py-2 font-mono text-[10px] font-bold text-[var(--color-text-2)] uppercase tracking-widest">
            Saved Snippets
          </div>
          <div class="max-h-60 overflow-y-auto p-1">
            {#if snippets.length === 0}
              <div class="px-3 py-4 text-center font-mono text-[10px] text-[var(--color-text-4)]">
                No snippets found.
              </div>
            {/if}
            {#each snippets as s (s.id)}
              <button
                class="flex w-full flex-col gap-1 rounded-sm px-2 py-1.5 text-left transition-colors hover:bg-[var(--color-surface-3)]"
                onclick={() => { applyingSnippet = s; showSnippets = false; }}
              >
                <span class="font-mono text-[11px] font-bold text-[var(--color-text-1)] truncate">{s.name}</span>
                <span class="font-mono text-[9px] text-[var(--color-text-3)] truncate">{s.body}</span>
              </button>
            {/each}
          </div>
        </div>
      {/if}
      </div>

      {#if mode === "local"}
        <div class="relative ml-2">
          <button
            class="flex items-center gap-1.5 border border-[var(--color-line)] px-2 py-0.5 font-mono text-[9px] uppercase tracking-wider text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
            onclick={() => (showHostPicker = !showHostPicker)}
          >
            <Server size="10" />
            <span>CONNECT</span>
          </button>
          {#if showHostPicker}
            <div
              class="absolute right-0 top-full z-30 mt-1 w-72 overflow-hidden border hairline-strong surface-2 shadow-2xl"
            >
              <div class="border-b hairline px-3 py-2 font-mono text-[10px] font-bold text-[var(--color-text-2)] uppercase tracking-widest">
                Saved hosts
              </div>
              <div class="max-h-64 overflow-y-auto">
                {#each app.hosts as h (h.id)}
                  {@const hasSavedPw = !!(app.hostPasswords[h.id])}
                  <button
                    class="flex w-full items-center gap-2.5 px-3 py-2 text-left text-xs hover:bg-[var(--color-surface-3)] transition-colors"
                    onclick={() => switchToRemote(h.id)}
                  >
                    <div class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-[var(--color-accent-soft)] text-[var(--color-accent)]">
                      <Server size="13" />
                    </div>
                    <div class="min-w-0 flex-1">
                      <div class="flex items-center gap-1.5 truncate">
                        <span class="truncate font-medium text-[var(--color-text-1)]">{h.name}</span>
                        {#if h.environment === 'production'}
                          <span class="shrink-0 rounded px-1 py-px text-[8px] font-semibold uppercase" style="color:#ef4444;background:rgba(239,68,68,0.12);border:1px solid rgba(239,68,68,0.25)">prod</span>
                        {:else if h.environment === 'staging'}
                          <span class="shrink-0 rounded px-1 py-px text-[8px] font-semibold uppercase" style="color:#f59e0b;background:rgba(245,158,11,0.12);border:1px solid rgba(245,158,11,0.25)">stage</span>
                        {/if}
                      </div>
                      <div class="flex items-center gap-1 truncate text-[10px] text-[var(--color-text-3)] font-mono">
                        {h.username}@{h.host}:{h.port}
                        {#if hasSavedPw}
                          <span class="text-[var(--color-accent)] opacity-70" title="Password saved">&#x2022;</span>
                        {/if}
                      </div>
                    </div>
                  </button>
                {/each}
                {#if app.hosts.length === 0}
                  <div class="px-3 py-5 text-center font-mono text-[10px] text-[var(--color-text-3)]">
                    No saved hosts yet.
                  </div>
                {/if}
              </div>
            </div>
          {/if}
        </div>
      {:else}
        <button
          class="ml-2 flex items-center gap-1.5 border border-[var(--color-danger)]/30 px-2 py-0.5 font-mono text-[9px] uppercase tracking-wider text-[var(--color-danger)] hover:bg-[var(--color-danger)]/10 transition-all"
          onclick={disconnectRemote}
        >
          <Unplug size="10" />
          <span>DISCONNECT</span>
        </button>
      {/if}
    </div>
  </div>

  <div bind:this={containerEl} class="relative flex-1 overflow-hidden p-1.5">
    <!-- Search Bar Overlay -->
    {#if showSearch}
      <div class="absolute right-4 top-2 z-30 flex items-center gap-2 border hairline-strong surface-2 p-1.5 shadow-xl fade-up" style="backdrop-filter: blur(8px);">
        <Search size="12" class="ml-1 text-[var(--color-text-4)]" />
        <input
          class="w-40 bg-transparent px-1 font-mono text-xs outline-none placeholder:text-[var(--color-text-4)]"
          placeholder="Find..."
          bind:value={searchQuery}
          use:focus
          oninput={() => searchAddon?.findNext(searchQuery)}
          onkeydown={(e) => {
            if (e.key === "Escape") {
              showSearch = false;
              term?.focus();
            } else if (e.key === "Enter") {
              if (e.shiftKey) searchAddon?.findPrevious(searchQuery);
              else searchAddon?.findNext(searchQuery);
            }
          }}
        />
        <span class="px-1 font-mono text-[10px] text-[var(--color-text-4)]">
          {searchResults.resultCount > 0 ? `${searchResults.resultIndex + 1}/${searchResults.resultCount}` : '0/0'}
        </span>
        <button class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]" onclick={() => searchAddon?.findPrevious(searchQuery)}>
          <ChevronUp size="12" />
        </button>
        <button class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]" onclick={() => searchAddon?.findNext(searchQuery)}>
          <ChevronDown size="12" />
        </button>
        <div class="h-4 w-px bg-[var(--color-line)]"></div>
        <button class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]" onclick={() => { showSearch = false; term?.focus(); }}>
          <X size="12" />
        </button>
      </div>
    {/if}

    <!-- Empty State Splash Screen -->
    {#if !hasTyped && (status === "running" || status === "connected")}
      <div class="pointer-events-none absolute inset-0 flex items-center justify-center fade-up z-10 bg-[var(--color-surface-0)]/40 backdrop-blur-[2px]">
        <div class="pointer-events-auto border hairline-strong surface-2 p-5 shadow-2xl min-w-[320px]">
          <div class="mb-4 flex items-center gap-2">
            <TerminalIcon size="14" class="text-[var(--color-accent)]" />
            <span class="font-mono text-[10px] font-bold uppercase tracking-widest text-[var(--color-text-1)]">
              {#if mode === "remote"}
                Connected to {connectedHost?.name ?? connectedHostID}
              {:else}
                Local Shell Ready
              {/if}
            </span>
          </div>
          
          <div class="space-y-3">
            <div class="font-mono text-[9px] uppercase tracking-widest text-[var(--color-text-4)]">Quick Commands</div>
            <div class="flex flex-wrap gap-2">
              {#each ['htop', 'df -h', 'docker ps -a', 'uptime'] as cmd}
                <button
                  class="rounded-sm border hairline bg-[var(--color-surface-3)] px-2.5 py-1.5 font-mono text-[10px] text-[var(--color-text-2)] hover:border-[var(--color-accent)]/50 hover:text-[var(--color-accent)] hover:bg-[var(--color-accent)]/5 transition-all"
                  onclick={() => { term?.paste(cmd); term?.focus(); }}
                  title="Paste '{cmd}'"
                >
                  {cmd}
                </button>
              {/each}
            </div>

            {#if snippets.length > 0}
              <div class="mt-4 border-t hairline pt-3">
                <div class="mb-2 font-mono text-[9px] uppercase tracking-widest text-[var(--color-text-4)]">Recent Snippets</div>
                <div class="space-y-1.5">
                  {#each snippets.slice(0, 2) as s}
                    <button
                      class="flex w-full items-center justify-between rounded-sm border hairline border-transparent bg-[var(--color-surface-3)]/50 px-2.5 py-1.5 hover:border-[var(--color-line-strong)] hover:bg-[var(--color-surface-3)] transition-all"
                      onclick={() => { applyingSnippet = s; }}
                    >
                      <span class="font-mono text-[10px] text-[var(--color-text-1)]">{s.name}</span>
                      <span class="font-mono text-[9px] text-[var(--color-text-4)] truncate max-w-[120px]">{s.body}</span>
                    </button>
                  {/each}
                </div>
              </div>
            {/if}
          </div>
          <div class="mt-5 text-center font-mono text-[9px] text-[var(--color-text-4)]">
            Start typing to dismiss
          </div>
        </div>
      </div>
    {/if}
  </div>

  {#if promptingPassword && app.selectedHostID}
    {@const host = app.hosts.find((h) => h.id === app.selectedHostID)}
    {#if host}
      <div class="absolute inset-0 z-20 flex items-center justify-center bg-black/70">
        <div class="w-80 overflow-hidden border hairline-strong surface-2 shadow-2xl shadow-black/80">
          <div class="flex items-center gap-2 border-b hairline px-4 py-2.5">
            <Lock size="11" class="text-[var(--color-accent)]" />
            <span class="font-mono text-[10px] font-bold uppercase tracking-widest text-[var(--color-text-1)]">AUTH REQUIRED</span>
          </div>
          <div class="p-4">
            <div class="mb-3 font-mono text-[10px] text-[var(--color-text-3)]">
              Password for <span class="text-[var(--color-accent)]">{host.username}@{host.host}</span>
            </div>
            <input
              type="password"
              class="w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-[11px] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50"
              bind:value={runtimePassword}
              placeholder="•••••••••"
              use:focus
              onkeydown={(e) => e.key === "Enter" && submitPassword()}
            />
            <p class="mt-2 font-mono text-[9px] text-[var(--color-text-4)] uppercase tracking-widest">
              TIP: Set permanently in Edit Host → Password
            </p>
          </div>
          <div class="flex items-center justify-end gap-2 border-t hairline px-4 py-2.5">
            <button
              class="border border-[var(--color-line)] px-3 py-1.5 font-mono text-[10px] uppercase tracking-widest text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
              onclick={() => { promptingPassword = false; void openLocal(); }}>CANCEL</button
            >
            <button
              class="border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 px-3 py-1.5 font-mono text-[10px] uppercase tracking-widest text-[var(--color-accent)] hover:bg-[var(--color-accent)]/15 disabled:opacity-30 disabled:cursor-not-allowed transition-all"
              disabled={!runtimePassword}
              onclick={submitPassword}>CONNECT</button
            >
          </div>
        </div>
      </div>
    {/if}
  {/if}

  {#if promptingTofu && tofuPayload}
    <div class="absolute inset-0 z-20 flex items-center justify-center bg-black/70">
      <div class="w-96 overflow-hidden border border-[var(--color-warn)] surface-2 shadow-2xl shadow-black/80">
        <div class="flex items-center gap-2 border-b hairline px-4 py-2.5 bg-[var(--color-warn)]/10">
          <AlertTriangle size="11" class="text-[var(--color-warn)]" />
          <span class="font-mono text-[10px] font-bold uppercase tracking-widest text-[var(--color-warn)]">UNKNOWN HOST KEY</span>
        </div>
        <div class="p-4 font-mono">
          <p class="mb-3 text-[11px] text-[var(--color-text-2)] leading-relaxed">
            The authenticity of host <span class="text-[var(--color-accent)]">{tofuPayload.host}</span> can't be established.
          </p>
          <div class="mb-4 bg-[var(--color-surface-3)] p-3 border hairline text-[10px] space-y-1.5">
            <div class="text-[var(--color-text-4)]">Key Type</div>
            <div class="text-[var(--color-text-1)]">{tofuPayload.keyType}</div>
            <div class="text-[var(--color-text-4)] mt-2">Fingerprint</div>
            <div class="text-[var(--color-text-1)] break-all">{tofuPayload.presentedFp}</div>
          </div>
          <p class="text-[10px] text-[var(--color-text-3)]">Are you sure you want to continue connecting?</p>
        </div>
        <div class="flex items-center justify-end gap-2 border-t hairline px-4 py-2.5">
          <button
            class="border border-[var(--color-line)] px-3 py-1.5 font-mono text-[10px] uppercase tracking-widest text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
            onclick={() => { promptingTofu = false; void openLocal(); }}>CANCEL</button
          >
          <button
            class="border border-[var(--color-warn)]/50 bg-[var(--color-warn)]/10 px-3 py-1.5 font-mono text-[10px] uppercase tracking-widest text-[var(--color-warn)] hover:bg-[var(--color-warn)]/15 transition-all"
            onclick={approveTofu}>TRUST & CONNECT</button
          >
        </div>
      </div>
    </div>
  {/if}

  <!-- ── SUDO PILL ── floating auto-fill prompt ─────────────────────── -->
  {#if showSudoPill && (status === "running" || status === "connected")}
    {@const hasPw = !!getSudoPassword()}
    <div class="absolute bottom-3 left-1/2 z-30 -translate-x-1/2 fade-up">
      <div
        class="flex items-center gap-2 border border-[var(--color-warn)]/50 bg-[var(--color-surface-2)]/95 px-3 py-2 shadow-2xl shadow-black/60"
        style="backdrop-filter: blur(12px); box-shadow: 0 0 20px rgba(255,170,0,0.08), 0 8px 32px rgba(0,0,0,0.5);"
      >
        <ShieldCheck size="13" class="shrink-0 text-[var(--color-warn)]" />
        {#if sudoInlineInput}
          <!-- Inline password entry when no stored password exists -->
          <input
            type="password"
            class="w-44 border hairline bg-[var(--color-surface-3)] px-2 py-1 font-mono text-[11px] text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-warn)]/50"
            bind:value={sudoInlinePassword}
            placeholder="sudo password"
            use:focus
            onkeydown={(e) => e.key === "Enter" && sendInlineSudoPassword()}
          />
          <button
            class="border border-[var(--color-warn)]/50 bg-[var(--color-warn)]/10 px-2 py-1 font-mono text-[9px] font-bold uppercase tracking-widest text-[var(--color-warn)] hover:bg-[var(--color-warn)]/20 disabled:opacity-30 transition-all"
            disabled={!sudoInlinePassword}
            onclick={sendInlineSudoPassword}
          >SEND</button>
        {:else}
          <span class="font-mono text-[10px] text-[var(--color-text-2)]">
            {#if hasPw}
              sudo password ready
            {:else}
              sudo prompt detected
            {/if}
          </span>
          <button
            class="flex items-center gap-1.5 border border-[var(--color-warn)]/50 bg-[var(--color-warn)]/10 px-2.5 py-1 font-mono text-[9px] font-bold uppercase tracking-widest text-[var(--color-warn)] hover:bg-[var(--color-warn)]/20 transition-all"
            onclick={sendSudoPassword}
          >
            {#if hasPw}
              <ShieldCheck size="9" />AUTO-FILL
            {:else}
              TYPE PASSWORD
            {/if}
          </button>
          {#if hasPw}
            <button
              class="flex items-center gap-1.5 border border-[var(--color-warn)]/50 bg-transparent px-2 py-1 font-mono text-[9px] font-bold uppercase tracking-widest text-[var(--color-warn)] hover:bg-[var(--color-warn)]/20 transition-all"
              onclick={() => sudoInlineInput = true}
              title="Edit saved sudo password"
            >
              EDIT
            </button>
          {/if}
          <span class="font-mono text-[8px] text-[var(--color-text-4)] tracking-wider">CTRL+SHIFT+S</span>
        {/if}
        <button
          class="ml-1 text-[var(--color-text-4)] hover:text-[var(--color-text-2)] transition-colors"
          onclick={dismissSudoPill}
          title="Dismiss"
        >&times;</button>
      </div>
    </div>
  {/if}
</div>
{#if applyingSnippet}
  <SnippetApplyDialog
    snippet={applyingSnippet}
    onCancel={() => (applyingSnippet = null)}
    onApply={(rendered) => {
      applyingSnippet = null;
      writeLocal(rendered);
      app.fanOutBroadcast(sessionID, rendered);
      term?.focus();
    }}
  />
{/if}
