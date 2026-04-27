<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { WebLinksAddon } from "@xterm/addon-web-links";
  import {
    LocalShellService,
    SSHService,
  } from "../../bindings/github.com/blacknode/blacknode";
  import { app } from "./state.svelte";
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

  let containerEl: HTMLDivElement | undefined = $state();
  let term: Terminal | undefined;
  let fit: FitAddon | undefined;
  let dataOff: (() => void) | undefined;
  let exitOff: (() => void) | undefined;
  let resizeObs: ResizeObserver | undefined;
  // Latency state — only populated for connected SSH sessions, polled every
  // 5s. Null means "not measured yet" or "ping failed".
  let latencyMs = $state<number | null>(null);
  let latencyTimer: ReturnType<typeof setInterval> | undefined;

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

  // Pick the xterm palette to match the app theme. We don't hot-swap — if
  // the user toggles theme, existing sessions keep the theme they spawned
  // with; new sessions pick up the new theme.
  function termTheme() {
    if (app.settings.theme === "light") {
      return {
        background: "#ffffff",
        foreground: "#0a0e18",
        cursor: "#0891b2",
        cursorAccent: "#ffffff",
        selectionBackground: "rgba(8, 145, 178, 0.20)",
        black: "#1f2533",
        brightBlack: "#525866",
        red: "#c53030",
        brightRed: "#9b1c1c",
        green: "#16a34a",
        brightGreen: "#15803d",
        yellow: "#b25800",
        brightYellow: "#92400e",
        blue: "#1d4ed8",
        brightBlue: "#1e3a8a",
        magenta: "#7e22ce",
        brightMagenta: "#581c87",
        cyan: "#0891b2",
        brightCyan: "#0e7490",
        white: "#7a8092",
        brightWhite: "#0a0e18",
      };
    }
    return {
      background: "#08080b",
      foreground: "#ededf3",
      cursor: "#22d3ee",
      cursorAccent: "#08080b",
      selectionBackground: "rgba(34, 211, 238, 0.25)",
      black: "#08080b",
      brightBlack: "#4a4a58",
      red: "#ef4444",
      brightRed: "#fca5a5",
      green: "#10b981",
      brightGreen: "#6ee7b7",
      yellow: "#f59e0b",
      brightYellow: "#fcd34d",
      blue: "#3b82f6",
      brightBlue: "#93c5fd",
      magenta: "#a855f7",
      brightMagenta: "#d8b4fe",
      cyan: "#22d3ee",
      brightCyan: "#67e8f9",
      white: "#a4a4b3",
      brightWhite: "#ededf3",
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
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    term.open(containerEl!);
    fit.fit();

    term.onData((d) => {
      writeLocal(d);
      // If broadcast is on and we're in the group, fan out to siblings.
      app.fanOutBroadcast(sessionID, d);
    });

    // Register a sink so other terminals can broadcast keystrokes into us
    // without knowing whether we're a local PTY or an SSH session.
    app.registerBroadcastSink(sessionID, writeLocal);
    term.onResize(({ cols, rows }) => {
      if (mode === "local" && status === "running") void LocalShellService.Resize(sessionID, cols, rows);
      if (mode === "remote" && status === "connected") void SSHService.Resize(sessionID, cols, rows);
    });

    resizeObs = new ResizeObserver(() => fit?.fit());
    resizeObs.observe(containerEl!);

    dataOff = Events.On("terminal:data", (e: any) => {
      const p = e?.data;
      if (!p || p.sessionID !== sessionID) return;
      term?.write(p.data);
    });
    exitOff = Events.On("terminal:exit", (e: any) => {
      const p = e?.data;
      if (!p || p.sessionID !== sessionID) return;
      term?.writeln(`\r\n\x1b[90m[session closed: ${p.reason ?? ""}]\x1b[0m`);
      if (mode === "remote") {
        connectedHostID = null;
        status = "idle";
      } else {
        status = "idle";
      }
    });

    void openLocal();
  });

  onDestroy(() => {
    dataOff?.();
    exitOff?.();
    resizeObs?.disconnect();
    stopLatencyPolling();
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
      status = "error";
      errorMsg = String(e?.message ?? e);
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
      <span class="text-[var(--color-text-2)]">local</span>
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

      <div class="relative ml-auto">
        <button
          class="flex items-center gap-1.5 rounded px-2 py-1 text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={() => (showHostPicker = !showHostPicker)}
        >
          <Server size="12" />
          <span>connect to host</span>
        </button>
        {#if showHostPicker}
          <div
            class="absolute right-0 top-full z-30 mt-1 w-64 overflow-hidden rounded-md border hairline-strong surface-2 shadow-2xl shadow-black/40"
          >
            <div class="px-3 py-2 text-[10px] uppercase tracking-wider text-[var(--color-text-4)]">
              Saved hosts
            </div>
            {#each app.hosts as h (h.id)}
              <button
                class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-[var(--color-surface-3)]"
                onclick={() => switchToRemote(h.id)}
              >
                <Server size="12" class="text-[var(--color-text-3)]" />
                <div class="min-w-0 flex-1">
                  <div class="truncate text-[var(--color-text-1)]">{h.name}</div>
                  <div class="truncate text-[10px] text-[var(--color-text-3)]">
                    {h.username}@{h.host}
                  </div>
                </div>
              </button>
            {/each}
            {#if app.hosts.length === 0}
              <div class="px-3 py-3 text-center text-[11px] text-[var(--color-text-3)]">
                No saved hosts yet.
              </div>
            {/if}
          </div>
        {/if}
      </div>
    {:else}
      <Plug size="14" class="text-[var(--color-accent)]" />
      {#if connectedHost}
        <span class="font-mono text-[var(--color-text-1)]"
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
      <button
        class="ml-auto flex items-center gap-1.5 rounded px-2 py-1 text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-danger)]"
        onclick={disconnectRemote}
      >
        <Unplug size="12" />
        <span>disconnect</span>
      </button>
    {/if}

    {#if errorMsg}
      <span class="ml-2 truncate font-mono text-[10px] text-[var(--color-danger)]"
        title={errorMsg}>{errorMsg}</span
      >
    {/if}

    {#if app.recordingsEnabled && (status === "running" || status === "connected")}
      <span
        class="ml-1 flex items-center gap-1 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wider text-[var(--color-danger)]"
        title="This session is being recorded"
      >
        <Circle
          size="6"
          class="fill-[var(--color-danger)] text-[var(--color-danger)] pulse-soft"
        />
        REC
      </span>
    {/if}

    <button
      class="flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] {inBroadcast
        ? broadcastActive
          ? 'bg-[var(--color-warn)]/15 text-[var(--color-warn)] border border-[var(--color-warn)]/40'
          : 'bg-[var(--color-surface-3)] text-[var(--color-text-1)] border hairline-strong'
        : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)] border border-transparent'}"
      onclick={toggleBroadcastMember}
      title={inBroadcast
        ? "Remove this pane from the broadcast group"
        : "Add this pane to the broadcast group"}
    >
      <Radio size="10" class={broadcastActive ? "pulse-soft" : ""} />
      <span>cast</span>
    </button>
  </div>

  <div bind:this={containerEl} class="flex-1 overflow-hidden p-2"></div>

  {#if promptingPassword && app.selectedHostID}
    {@const host = app.hosts.find((h) => h.id === app.selectedHostID)}
    {#if host}
      <div class="border-t hairline surface-1 px-3 py-2">
        <div class="flex items-center gap-2 text-xs">
          <Lock size="12" class="text-[var(--color-text-3)]" />
          <span class="text-[var(--color-text-3)]"
            >Password for {host.username}@{host.host}</span
          >
          <input
            type="password"
            class="flex-1 rounded bg-[var(--color-surface-3)] px-2 py-1 outline-none"
            bind:value={runtimePassword}
            onkeydown={(e) => e.key === "Enter" && submitPassword()}
          />
          <button
            class="rounded bg-[var(--color-accent)] px-2 py-1 text-[var(--color-surface-0)] font-medium"
            onclick={submitPassword}>OK</button
          >
          <button
            class="rounded px-2 py-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)]"
            onclick={() => {
              promptingPassword = false;
              void openLocal();
            }}>cancel</button
          >
        </div>
      </div>
    {/if}
  {/if}
</div>
