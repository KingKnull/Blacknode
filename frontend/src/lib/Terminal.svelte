<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { WebLinksAddon } from "@xterm/addon-web-links";
  import {
    LocalShellService,
    SSHService,
    HostService,
  } from "../../bindings/github.com/blacknode/blacknode/internal/service";
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
  
  let promptingTofu = $state(false);
  let tofuPayload = $state<{host: string, port: number, keyType: string, presentedFp: string, presentedKey: string} | null>(null);

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
        background: "#f5f2eb",
        foreground: "#1a1208",
        cursor: "#0a6640",
        cursorAccent: "#f5f2eb",
        selectionBackground: "rgba(10, 102, 64, 0.18)",
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
      const msg = String(e?.message ?? e);
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
            class="absolute right-0 top-full z-30 mt-1 w-72 overflow-hidden rounded-lg border hairline-strong surface-2 shadow-2xl shadow-black/50"
          >
            <div class="border-b hairline px-3 py-2">
              <p class="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-text-3)]">Saved hosts</p>
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
                <div class="px-3 py-5 text-center text-[11px] text-[var(--color-text-3)]">
                  No saved hosts yet.
                </div>
              {/if}
            </div>
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
        class="ml-auto flex items-center gap-1.5 border border-[var(--color-line)] px-2 py-0.5 text-[var(--color-text-3)] hover:border-[var(--color-danger)]/40 hover:text-[var(--color-danger)] transition-all"
        onclick={disconnectRemote}
      >
        <Unplug size="10" />
        <span>DISCONNECT</span>
      </button>
    {/if}

    {#if errorMsg}
      <span class="ml-2 truncate font-mono text-[9px] text-[var(--color-danger)]" title={errorMsg}>
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
  </div>

  <div bind:this={containerEl} class="flex-1 overflow-hidden p-1.5"></div>

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
              autofocus
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
</div>
