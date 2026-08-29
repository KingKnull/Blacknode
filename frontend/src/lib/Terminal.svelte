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
    MoshService,
    TelnetService,
    SerialService,
    HostService,
    SnippetService,
  } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Snippet } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { focus } from "./actions";
  import { app } from "./state.svelte";
  import { bus } from "./events";
  import SnippetApplyDialog from "./SnippetApplyDialog.svelte";
  import AutocompletePopup from "./AutocompletePopup.svelte";
  import TerminalSidePanel from "./TerminalSidePanel.svelte";
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
    Wifi,
    Cable,
    PanelRight,
  } from "@lucide/svelte";

  type Props = { sessionID: string };
  let { sessionID }: Props = $props();

  type Mode = "local" | "remote" | "mosh" | "telnet" | "serial";
  type Status = "starting" | "running" | "connecting" | "connected" | "idle" | "error";

  let mode: Mode = $state("local");
  let status: Status = $state("starting");
  let errorMsg = $state("");
  let connectedHostID = $state<string | null>(null);
  let promptingPassword = $state(false);
  // One-shot password typed at the connect prompt. Held only for the duration
  // of this pane's connect attempts (TOFU approval and auto-reconnect re-use
  // it), then dropped on disconnect. Saved passwords are never loaded here —
  // the Go dialer resolves those from the vault itself.
  let runtimePassword = $state("");
  let rememberPassword = $state(false);
  let showHostPicker = $state(false);

  let promptingTofu = $state(false);
  let tofuPayload = $state<{host: string, port: number, keyType: string, presentedFp: string, presentedKey: string} | null>(null);

  // Splash screen tracking
  let hasTyped = $state(false);

  // Whether this pane currently holds keyboard focus — drives the tab
  // "unread output" indicator (output that arrives while unfocused is unread).
  let isFocused = false;

  // Auto-reconnect state (SSH only — Mosh handles reconnects itself)
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

  let latencyMs = $state<number | null>(null);
  let latencyTimer: ReturnType<typeof setInterval> | undefined;

  // ── Side panel ────────────────────────────────────────────────────
  let showSidePanel = $state(false);

  // ── Autocomplete ──────────────────────────────────────────────────
  // Track the current input buffer for autocomplete suggestions.
  // We reset on newline and strip ANSI sequences before querying.
  let acBuffer = $state("");
  let showAutocomplete = $state(false);

  function stripAnsi(s: string): string {
    return s.replace(/\x1b\[[0-9;]*[A-Za-z]/g, "").replace(/\x1b\][^\x07]*\x07/g, "");
  }

  function updateAcBuffer(d: string) {
    // Newline / carriage return → reset buffer and dismiss.
    if (d === "\r" || d === "\n" || d.includes("\r") || d.includes("\n")) {
      acBuffer = "";
      showAutocomplete = false;
      return;
    }
    // Backspace
    if (d === "\x7f" || d === "\b") {
      acBuffer = acBuffer.slice(0, -1);
      showAutocomplete = acBuffer.trim().length >= 2;
      return;
    }
    // Escape / control sequences → dismiss
    if (d.startsWith("\x1b")) {
      if (d === "\x1b[B") {
        // ↓ arrow at end of line → open autocomplete if we have a buffer
        if (acBuffer.trim().length >= 2) {
          showAutocomplete = true;
        }
      } else {
        showAutocomplete = false;
      }
      return;
    }
    acBuffer += d;
    const clean = stripAnsi(acBuffer).trim();
    // Auto-show after 2 chars following a space (sub-command matching)
    const afterSpace = clean.includes(" ") && clean.split(" ").pop()!.length >= 2;
    showAutocomplete = clean.length >= 2 && (clean.length <= 60) && (!clean.includes(" ") || afterSpace);
  }

  function acceptAutocomplete(text: string) {
    showAutocomplete = false;
    // Replace the current buffer with the accepted text
    const spaces = acBuffer.length;
    const backspaces = "\x7f".repeat(spaces);
    writeLocal(backspaces + text);
    acBuffer = text;
  }

  // ── Sudo prompt detection ──────────────────────────────────────────
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
      if (re.test(data)) { triggerSudoPill(); return; }
    }
  }

  function triggerSudoPill() {
    if (sudoPillTimer) clearTimeout(sudoPillTimer);
    showSudoPill = true;
    if (!isFocused) app.setSessionStatus(sessionID, "needs-input");
    sudoPillTimer = setTimeout(() => {
      showSudoPill = false;
      sudoInlineInput = false;
    }, 20_000);
  }

  function dismissSudoPill() {
    if (sudoPillTimer) clearTimeout(sudoPillTimer);
    showSudoPill = false;
    sudoInlineInput = false;
    if (app.sessionStatus[sessionID] === "needs-input") app.clearSessionStatus(sessionID);
  }

  // Whether a sudo password is on file for whatever this pane is attached to.
  // A boolean is all the UI needs; the plaintext never comes over the bridge.
  function hasSudoPassword(): boolean {
    if (mode === "remote" && connectedHostID) return app.hasSavedSudoPassword(connectedHostID);
    if (mode === "local") return app.hasSavedSudoPassword("local");
    return false;
  }

  // Ask the backend to unseal the sudo password and write it into the session
  // itself. Returns false when nothing is stored, in which case we fall back
  // to the inline prompt. The renderer never sees the password either way.
  async function sendSudoPassword() {
    let sent = false;
    try {
      if (mode === "remote" && connectedHostID) {
        sent = (await SSHService.SendSudoPassword(sessionID, connectedHostID)) ?? false;
      } else if (mode === "local") {
        sent = (await LocalShellService.SendSudoPassword(sessionID)) ?? false;
      }
    } catch (e: any) {
      app.toast("error", "SUDO FILL FAILED", String(e?.message ?? e));
      return;
    }
    if (!sent) { sudoInlineInput = true; return; }
    dismissSudoPill();
  }

  // The inline prompt is the one place a password legitimately passes through
  // the renderer — the user just typed it here. We write it straight to the
  // session and hand it to the vault; it isn't retained in app state.
  async function sendInlineSudoPassword() {
    if (!sudoInlinePassword) return;
    const pw = sudoInlinePassword;
    sudoInlinePassword = "";
    writeLocal(pw + "\n");
    const target = mode === "remote" && connectedHostID ? connectedHostID : mode === "local" ? "local" : null;
    if (target) {
      try {
        await HostService.SetSudoPassword(target, pw);
        await app.refreshSecretStatus();
      } catch (e: any) {
        app.toast("warn", "SUDO PASSWORD NOT SAVED", String(e?.message ?? e));
      }
    }
    dismissSudoPill();
  }

  // AI insert
  $effect(() => {
    const p = app.pendingTerminalInsert;
    if (!p || p.sessionID !== sessionID) return;
    if (mode === "local" && status === "running") void LocalShellService.Write(sessionID, p.text);
    else if (mode === "remote" && status === "connected") void SSHService.Write(sessionID, p.text);
    else if (mode === "mosh" && status === "connected") void MoshService.Write(sessionID, p.text);
    else if (mode === "telnet" && status === "connected") void TelnetService.Write(sessionID, p.text);
    else if (mode === "serial" && status === "connected") void SerialService.Write(sessionID, p.text);
    app.pendingTerminalInsert = null;
  });

  // Reflect an errored session onto its tab dot (and clear it once recovered).
  $effect(() => {
    if (status === "error") app.setSessionStatus(sessionID, "error");
    else if (app.sessionStatus[sessionID] === "error") app.clearSessionStatus(sessionID);
  });

  // Keep the connected-hosts set and this pane's host attachment in sync. The
  // attachment is what names a broadcast's blast radius and what the workspace
  // persists for session restore, so it has to follow disconnects as closely as
  // connects — a stale entry would restore a pane to a host it had left.
  $effect(() => {
    if (mode === "local" || !connectedHostID) {
      app.setSessionHost(sessionID, null);
      return;
    }
    if (status === "connected") {
      app.addConnectedHost(connectedHostID);
      app.setSessionHost(sessionID, {
        hostID: connectedHostID,
        // Telnet and serial hosts restore through the same SSH entry point,
        // which re-reads host.protocol and routes them — only Mosh is a
        // genuinely separate choice the user made for this pane.
        via: mode === "mosh" ? "mosh" : "ssh",
      });
    } else {
      app.removeConnectedHost(connectedHostID);
      app.setSessionHost(sessionID, null);
    }
  });

  // Terminal theme themes — updated via TerminalSidePanel theme picker
  function applyTermTheme(themeObj: { bg: string; fg: string; cur: string }) {
    term?.options.theme && (term.options.theme = {
      ...term.options.theme,
      background: themeObj.bg,
      foreground: themeObj.fg,
      cursor: themeObj.cur,
    });
  }

  function termTheme() {
    if (app.settings.theme === "light") {
      return {
        background: "#ffffff", foreground: "#1f2733", cursor: "#2563eb", cursorAccent: "#ffffff",
        selectionBackground: "rgba(37, 99, 235, 0.18)",
        black: "#1f2733", brightBlack: "#6b7383", red: "#dc2626", brightRed: "#ef4444",
        green: "#16a34a", brightGreen: "#22c55e", yellow: "#d97706", brightYellow: "#f59e0b",
        blue: "#2563eb", brightBlue: "#3b82f6", magenta: "#7c3aed", brightMagenta: "#a78bfa",
        cyan: "#0891b2", brightCyan: "#38bdf8", white: "#6b7383", brightWhite: "#1f2733",
      };
    }
    return {
      background: "#0b0e14", foreground: "#e6e9ef", cursor: "#3b82f6", cursorAccent: "#0b0e14",
      selectionBackground: "rgba(59,130,246, 0.22)",
      black: "#0b0e14", brightBlack: "#545d6b", red: "#ef4444", brightRed: "#f87171",
      green: "#22c55e", brightGreen: "#4ade80", yellow: "#f59e0b", brightYellow: "#fbbf24",
      blue: "#3b82f6", brightBlue: "#60a5fa", magenta: "#a78bfa", brightMagenta: "#c4b5fd",
      cyan: "#38bdf8", brightCyan: "#7dd3fc", white: "#9aa4b2", brightWhite: "#e6e9ef",
    };
  }

  onMount(() => {
    term = new Terminal({
      fontFamily: '"JetBrains Mono Variable", "Cascadia Mono", Menlo, Consolas, monospace',
      fontSize: 13, lineHeight: 1.25, letterSpacing: 0,
      cursorBlink: true, cursorStyle: "bar",
      allowProposedApi: true, scrollback: 5000,
      theme: termTheme(),
    });
    fit = new FitAddon();
    searchAddon = new SearchAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    term.loadAddon(searchAddon);

    searchAddon.onDidChangeResults((e) => { searchResults = e; });

    term.open(containerEl!);
    fit.fit();

    try {
      const webgl = new WebglAddon();
      webgl.onContextLoss(() => { webgl.dispose(); });
      term.loadAddon(webgl);
    } catch { /* canvas fallback */ }

    term.attachCustomKeyEventHandler((e) => {
      if (e.type === "keydown" && e.ctrlKey && e.shiftKey && e.key.toLowerCase() === "s") {
        e.preventDefault(); sendSudoPassword(); return false;
      }
      if (e.type === "keydown" && e.ctrlKey && e.shiftKey && e.key.toLowerCase() === "f") {
        e.preventDefault(); showSearch = true; return false;
      }
      // Ctrl+. → toggle side panel
      if (e.type === "keydown" && e.ctrlKey && e.key === ".") {
        e.preventDefault(); showSidePanel = !showSidePanel; return false;
      }
      return true;
    });

    term.onData((d) => {
      hasTyped = true;
      updateAcBuffer(d);
      writeLocal(d);
      app.fanOutBroadcast(sessionID, d);
      if (showSudoPill && !sudoInlineInput) dismissSudoPill();
    });

    app.registerBroadcastSink(sessionID, writeLocal);

    term.onResize(({ cols, rows }) => {
      if (mode === "local" && status === "running") void LocalShellService.Resize(sessionID, cols, rows);
      if (mode === "remote" && status === "connected") void SSHService.Resize(sessionID, cols, rows);
      if (mode === "mosh" && status === "connected") void MoshService.Resize(sessionID, cols, rows);
      if (mode === "telnet" && status === "connected") void TelnetService.Resize(sessionID, cols, rows);
      if (mode === "serial" && status === "connected") void SerialService.Resize(sessionID, cols, rows);
    });

    resizeObs = new ResizeObserver(() => {
      clearTimeout(resizeDebounce);
      resizeDebounce = setTimeout(() => fit?.fit(), 80);
    });
    resizeObs.observe(containerEl!);

    // Focus tracking — focusin/focusout bubble up from xterm's helper textarea.
    containerEl?.addEventListener("focusin", () => {
      isFocused = true;
      app.clearSessionStatus(sessionID);
    });
    containerEl?.addEventListener("focusout", () => { isFocused = false; });

    dataOff = Events.On("terminal:data", (e: any) => {
      const p = e?.data;
      if (!p || p.sessionID !== sessionID) return;
      term?.write(p.data);
      checkForSudoPrompt(p.data);
      // Output landing on an unfocused pane → flag the tab as having unread output.
      if (!isFocused) app.markSessionUnread(sessionID);
    });

    exitOff = Events.On("terminal:exit", (e: any) => {
      const p = e?.data;
      if (!p || p.sessionID !== sessionID) return;
      const reason = p.reason ?? "";
      term?.writeln(`\r\n\x1b[90m[session closed: ${reason}]\x1b[0m`);
      if (mode === "remote" && connectedHostID) {
        const hostID = connectedHostID;
        connectedHostID = null;
        stopLatencyPolling();
        if (reconnectAttempt < MAX_RECONNECT_ATTEMPTS) {
          reconnecting = true;
          const delay = Math.pow(2, reconnectAttempt + 1) * 1000;
          reconnectAttempt++;
          term?.writeln(`\x1b[33m[reconnecting in ${delay / 1000}s... attempt ${reconnectAttempt}/${MAX_RECONNECT_ATTEMPTS}]\x1b[0m`);
          reconnectTimer = setTimeout(async () => {
            try {
              await actuallyConnect(hostID);
              reconnecting = false;
              reconnectAttempt = 0;
              term?.writeln(`\x1b[32m[reconnected]\x1b[0m`);
            } catch {
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
      } else if (mode === "mosh" || mode === "telnet" || mode === "serial") {
        // Mosh handles its own reconnects; telnet/serial have no auto-reconnect.
        connectedHostID = null;
        status = "idle";
      } else {
        status = "idle";
      }
    });

    void openLocal();

    // Drain any connect intent parked for this session before the pane
    // existed, then stay subscribed for intents that arrive while it's live.
    // Both paths go through the same function, so there's no ordering to get
    // wrong and nothing to wait for.
    function drainConnectIntent() {
      const intent = app.takeConnectIntent(sessionID);
      if (!intent) return;
      if (intent.via === "mosh") void switchToMosh(intent.hostID);
      else void switchToRemote(intent.hostID);
    }
    drainConnectIntent();
    const offConnectIntent = bus.on('connect-intent', (detail) => {
      if (detail.sessionID === sessionID) drainConnectIntent();
    });

    // Listen for theme changes from TerminalSidePanel
    const onThemeChange = (e: Event) => {
      const t = (e as CustomEvent).detail;
      if (t) applyTermTheme(t);
    };
    window.addEventListener("terminal:theme-change", onThemeChange);

    return () => { offConnectIntent(); window.removeEventListener("terminal:theme-change", onThemeChange); };
  });

  onDestroy(() => {
    dataOff?.(); exitOff?.();
    resizeObs?.disconnect();
    stopLatencyPolling();
    if (sudoPillTimer) clearTimeout(sudoPillTimer);
    if (reconnectTimer) clearTimeout(reconnectTimer);
    clearTimeout(resizeDebounce);
    app.unregisterBroadcastSink(sessionID);
    app.forgetSession(sessionID);
    app.forgetConnectIntent(sessionID);
    // xterm can throw here if the WebGL addon already disposed itself after a
    // context loss (hidden/minimized webview) — a throw during onDestroy would
    // abort Svelte's whole unmount and blank the app, so never let it escape.
    try { term?.dispose(); } catch { /* already disposed */ }
    if (mode === "local" && status === "running") void LocalShellService.Close(sessionID);
    if (mode === "remote" && status === "connected") void SSHService.Disconnect(sessionID);
    if (mode === "mosh" && status === "connected") void MoshService.Disconnect(sessionID);
    if (mode === "telnet" && status === "connected") void TelnetService.Disconnect(sessionID);
    if (mode === "serial" && status === "connected") void SerialService.Disconnect(sessionID);
  });

  function writeLocal(d: string) {
    if (mode === "local" && status === "running") void LocalShellService.Write(sessionID, d);
    else if (mode === "remote" && status === "connected") void SSHService.Write(sessionID, d);
    else if (mode === "mosh" && status === "connected") void MoshService.Write(sessionID, d);
    else if (mode === "telnet" && status === "connected") void TelnetService.Write(sessionID, d);
    else if (mode === "serial" && status === "connected") void SerialService.Write(sessionID, d);
  }

  // If the host defines a startup snippet, render it (using variable defaults)
  // and send it once the shell is up. A short delay lets the remote prompt
  // settle before we type into it.
  function runStartupSnippet(hostID: string) {
    const host = app.hosts.find((h) => h.id === hostID);
    if (!host?.startupSnippetID) return;
    setTimeout(async () => {
      try {
        const rendered = await SnippetService.Apply(host.startupSnippetID!, {}, hostID, host.name, false);
        if (rendered) writeLocal(rendered.endsWith("\n") ? rendered : rendered + "\n");
      } catch { /* snippet may have been deleted — ignore */ }
    }, 400);
  }

  function toggleBroadcastMember() { app.toggleBroadcastMember(sessionID); }

  let inBroadcast = $derived(app.broadcastSet.has(sessionID));
  let broadcastActive = $derived(app.broadcastEnabled && inBroadcast);

  async function openLocal() {
    status = "starting"; errorMsg = "";
    try {
      await LocalShellService.Open(sessionID, term?.cols ?? 80, term?.rows ?? 24);
      mode = "local"; status = "running"; term?.focus();
      bus.emit('session-label', { sessionID, label: "" });
    } catch (e: any) { status = "error"; errorMsg = String(e?.message ?? e); }
  }

  async function switchToRemote(hostID: string) {
    showHostPicker = false;
    const host = app.hosts.find((h) => h.id === hostID);
    if (!host) return;
    if ((host.environment ?? "").toLowerCase() === "production") {
      const ok = confirm(`⚠️ ${host.name} is tagged PRODUCTION.\n\nConnect anyway?`);
      if (!ok) return;
    }
    // Telnet and serial are plaintext transports with no SSH auth — route them
    // straight to their own services instead of the SSH connect path.
    const proto = host.protocol || "ssh";
    if (proto === "telnet" || proto === "serial") {
      await connectAltProtocol(host.id, proto);
      return;
    }
    if (mode === "local" && status === "running") await LocalShellService.Close(sessionID);
    app.selectedHostID = hostID;
    mode = "remote";
    if (host.authMethod === "password") {
      // A saved password is resolved backend-side during the dial, so we only
      // need to know whether one exists. If not, prompt for a one-shot.
      if (!app.hasSavedPassword(host.id)) { promptingPassword = true; return; }
      runtimePassword = "";
    } else {
      runtimePassword = "";
    }
    await actuallyConnect(host.id);
  }

  async function switchToMosh(hostID: string) {
    showHostPicker = false;
    const host = app.hosts.find((h) => h.id === hostID);
    if (!host) return;
    if (mode === "local" && status === "running") await LocalShellService.Close(sessionID);
    if (mode === "remote" && status === "connected") await SSHService.Disconnect(sessionID);
    if (mode === "telnet" && status === "connected") await TelnetService.Disconnect(sessionID);
    if (mode === "serial" && status === "connected") await SerialService.Disconnect(sessionID);
    app.selectedHostID = hostID;
    mode = "mosh";
    status = "connecting";
    errorMsg = "";
    try {
      await MoshService.Connect(sessionID, hostID, term?.cols ?? 80, term?.rows ?? 24);
      status = "connected";
      connectedHostID = hostID;
      term?.focus();
      bus.emit('session-label', { sessionID, label: app.hosts.find((h) => h.id === hostID)?.name ?? "" });
      runStartupSnippet(hostID);
    } catch (e: any) {
      status = "error";
      errorMsg = String(e?.message ?? e);
      // Fall back to local shell on Mosh failure
      await openLocal();
    }
  }

  // Connect a telnet or serial host. These transports have no SSH credentials,
  // TOFU, latency probe, or auto-reconnect — they pipe bytes through the same
  // terminal:data / terminal:exit events as every other session type.
  async function connectAltProtocol(hostID: string, proto: "telnet" | "serial") {
    showHostPicker = false;
    if (mode === "local" && status === "running") await LocalShellService.Close(sessionID);
    if (mode === "remote" && status === "connected") await SSHService.Disconnect(sessionID);
    if (mode === "telnet" && status === "connected") await TelnetService.Disconnect(sessionID);
    if (mode === "serial" && status === "connected") await SerialService.Disconnect(sessionID);
    app.selectedHostID = hostID;
    mode = proto;
    status = "connecting";
    errorMsg = "";
    try {
      if (proto === "telnet") {
        await TelnetService.Connect(sessionID, hostID, term?.cols ?? 80, term?.rows ?? 24);
      } else {
        await SerialService.Connect(sessionID, hostID);
      }
      status = "connected";
      connectedHostID = hostID;
      term?.focus();
      bus.emit('session-label', { sessionID, label: app.hosts.find((h) => h.id === hostID)?.name ?? "" });
      runStartupSnippet(hostID);
    } catch (e: any) {
      status = "error";
      errorMsg = String(e?.message ?? e);
      // Fall back to local shell on connect failure, like Mosh does.
      await openLocal();
    }
  }

  // A password typed here is a one-shot: it goes to this pane's connect call
  // and nowhere else. It is only persisted if the user asks for it, in which
  // case it goes straight into the vault rather than into app state.
  async function submitPassword() {
    if (!runtimePassword || !app.selectedHostID) return;
    const hostID = app.selectedHostID;
    if (rememberPassword) {
      try {
        await HostService.SetPassword(hostID, runtimePassword);
        await app.refreshSecretStatus();
      } catch (e: any) {
        app.toast("warn", "PASSWORD NOT SAVED", String(e?.message ?? e));
      }
    }
    promptingPassword = false;
    await actuallyConnect(hostID);
  }

  async function actuallyConnect(hostID: string) {
    status = "connecting"; errorMsg = "";
    try {
      await SSHService.ConnectByHost(sessionID, hostID, runtimePassword, term?.cols ?? 80, term?.rows ?? 24);
      status = "connected"; connectedHostID = hostID; term?.focus();
      bus.emit('session-label', { sessionID, label: app.hosts.find((h) => h.id === hostID)?.name ?? "" });
      startLatencyPolling();
      runStartupSnippet(hostID);
    } catch (e: any) {
      let msg = String(e?.message ?? e);
      if (msg.trimStart().startsWith("{")) {
        try { const p = JSON.parse(msg); if (p.message) msg = p.message; } catch {}
      }
      if (msg.includes("UNKNOWN_HOST_KEY:")) {
        const jsonStr = msg.split("UNKNOWN_HOST_KEY:")[1];
        try { tofuPayload = JSON.parse(jsonStr); promptingTofu = true; status = "idle"; return; } catch {}
      }
      status = "error"; errorMsg = msg;
    }
  }

  async function approveTofu() {
    if (!tofuPayload || !app.selectedHostID) return;
    try {
      await HostService.ApproveHostKey(tofuPayload.host, tofuPayload.port, tofuPayload.keyType, tofuPayload.presentedKey, tofuPayload.presentedFp);
      promptingTofu = false;
      await actuallyConnect(app.selectedHostID);
    } catch (e: any) { status = "error"; errorMsg = "TOFU approval failed: " + String(e?.message ?? e); }
  }

  function startLatencyPolling() {
    stopLatencyPolling();
    void measureLatency();
    latencyTimer = setInterval(() => {
      if (status !== "connected") { stopLatencyPolling(); return; }
      void measureLatency();
    }, 5_000);
  }

  function stopLatencyPolling() {
    if (latencyTimer) { clearInterval(latencyTimer); latencyTimer = undefined; }
    latencyMs = null;
  }

  async function measureLatency() {
    try { latencyMs = (await SSHService.Latency(sessionID)) as number; }
    catch { latencyMs = null; }
  }

  async function disconnectRemote() {
    // Cancel any pending auto-reconnect before tearing down.
    if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = undefined; }
    reconnecting = false;
    reconnectAttempt = 0;
    try {
      if (mode === "mosh") await MoshService.Disconnect(sessionID);
      else if (mode === "telnet") await TelnetService.Disconnect(sessionID);
      else if (mode === "serial") await SerialService.Disconnect(sessionID);
      else await SSHService.Disconnect(sessionID);
    } finally {
      connectedHostID = null; status = "idle";
      runtimePassword = "";
      stopLatencyPolling();
      await openLocal();
    }
  }

  let connectedHost = $derived(connectedHostID ? app.hosts.find((h) => h.id === connectedHostID) : null);
  let connectedEnv = $derived(envBadge(connectedHost?.environment));
</script>

<div class="relative flex h-full w-full flex-col bg-[var(--color-surface-0)]">
  <!-- Production warning banner -->
  {#if connectedEnv.isProd && status === "connected"}
    <div class="flex items-center justify-center gap-1.5 border-b py-0.5 type-eyebrow"
      style:background={connectedEnv.bg} style:color={connectedEnv.color} style:border-color={connectedEnv.border}>
      <AlertTriangle size="10" /> production session <AlertTriangle size="10" />
    </div>
  {/if}

  <!-- Terminal toolbar -->
  <div class="flex items-center gap-2 border-b hairline px-3 py-1.5 type-caption surface-1">
    {#if mode === "local"}
      <TerminalIcon size="14" class="text-[var(--color-text-3)]" />
      <span class="font-mono type-body font-bold tracking-wide text-[var(--color-text-1)]">local</span>
      <span class="font-mono type-micro text-[var(--color-text-4)]">· {sessionID.slice(0, 6)}</span>
      {#if status === "starting"}
        <Loader2 size="12" class="animate-spin text-[var(--color-text-3)]" />
        <span class="text-[var(--color-text-3)]">starting…</span>
      {:else if status === "running"}
        <span class="ml-1 h-1.5 w-1.5 rounded-full bg-[var(--color-accent)] pulse-soft"></span>
      {/if}
    {:else}
      {#if mode === "mosh"}
        <Wifi size="14" class="text-[var(--color-success)]" />
        <span class="rounded border border-[var(--color-success)]/40 bg-[var(--color-success)]/10 px-1.5 py-px font-mono type-micro font-bold text-[var(--color-success)]">MOSH</span>
      {:else if mode === "telnet"}
        <Plug size="14" class="text-[var(--color-warn)]" />
        <span class="rounded border border-[var(--color-warn)]/40 bg-[var(--color-warn)]/10 px-1.5 py-px font-mono type-micro font-bold text-[var(--color-warn)]" title="Telnet is unencrypted">TELNET</span>
      {:else if mode === "serial"}
        <Cable size="14" class="text-[var(--color-accent)]" />
        <span class="rounded border border-[var(--color-accent)]/40 bg-[var(--color-accent)]/10 px-1.5 py-px font-mono type-micro font-bold text-[var(--color-accent)]">SERIAL</span>
      {:else}
        <Plug size="14" class="text-[var(--color-accent)]" />
      {/if}
      {#if connectedHost && mode === "serial"}
        <span class="font-mono type-body font-bold tracking-wide text-[var(--color-text-1)]">{connectedHost.serialDevice || "serial"}</span>
        {#if connectedHost.serialBaud}<span class="font-mono type-micro text-[var(--color-text-4)]">@{connectedHost.serialBaud}</span>{/if}
        <span class="ml-1 h-1.5 w-1.5 rounded-full bg-[var(--color-accent)] pulse-soft"></span>
      {:else if connectedHost && mode === "telnet"}
        <span class="font-mono type-body font-bold tracking-wide text-[var(--color-text-1)]">{connectedHost.host}</span>
        <span class="font-mono type-micro text-[var(--color-text-4)]">:{connectedHost.port || 23}</span>
        <span class="ml-1 h-1.5 w-1.5 rounded-full bg-[var(--color-accent)] pulse-soft"></span>
      {:else if connectedHost}
        <span class="font-mono type-body font-bold tracking-wide text-[var(--color-text-1)]">{connectedHost.username}@{connectedHost.host}</span>
        <span class="font-mono type-micro text-[var(--color-text-4)]">:{connectedHost.port}</span>
        <span class="ml-1 h-1.5 w-1.5 rounded-full bg-[var(--color-accent)] pulse-soft"></span>
        {#if latencyMs !== null && mode === "remote"}
          {@const tone = latencyMs < 50 ? "text-[var(--color-accent)]" : latencyMs < 200 ? "text-[var(--color-warn)]" : "text-[var(--color-danger)]"}
          <span class="ml-1 inline-flex items-center gap-0.5 rounded border hairline px-1.5 py-0.5 font-mono type-micro {tone}" title="Round-trip time">
            <Activity size="9" />{latencyMs}ms
          </span>
        {/if}
      {:else if status === "connecting"}
        <Loader2 size="12" class="animate-spin text-[var(--color-text-3)]" />
        <span class="text-[var(--color-text-3)]">{mode === "mosh" ? "launching mosh…" : "connecting…"}</span>
      {/if}
      {#if reconnecting}
        <span class="ml-1 flex items-center gap-1 font-mono type-micro text-[var(--color-warn)]">
          <Loader2 size="10" class="animate-spin" />
          reconnecting ({reconnectAttempt}/{MAX_RECONNECT_ATTEMPTS})
        </span>
      {/if}
    {/if}

    <div class="ml-auto flex items-center gap-2 opacity-50 hover:opacity-100 transition-opacity">
      {#if errorMsg}
        <span class="truncate font-mono type-nano text-[var(--color-danger)]" title={errorMsg}>ERR: {errorMsg}</span>
      {/if}

      {#if app.recordingsEnabled && (status === "running" || status === "connected")}
        <span class="flex items-center gap-1 border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/8 px-1.5 py-px font-mono type-eyebrow text-[var(--color-danger)]">
          <Circle size="6" class="fill-[var(--color-danger)] text-[var(--color-danger)] pulse-soft" />REC
        </span>
      {/if}

      <button
        class="flex items-center gap-1 border px-1.5 py-px font-mono type-eyebrow transition-all {inBroadcast ? broadcastActive ? 'border-[var(--color-warn)]/40 bg-[var(--color-warn)]/8 text-[var(--color-warn)]' : 'border-[var(--color-line-strong)] text-[var(--color-text-2)]' : 'border-[var(--color-line)] text-[var(--color-text-4)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)]'}"
        onclick={toggleBroadcastMember}
        title={inBroadcast ? "Remove from broadcast" : "Add to broadcast"}
      >
        <Radio size="9" class={broadcastActive ? 'pulse-soft' : ''} />CAST
      </button>

      <!-- Snippet quick-insert -->
      <div class="relative">
        <button
          class="flex items-center gap-1 border border-[var(--color-line)] px-2 py-0.5 font-mono type-eyebrow text-[var(--color-text-3)] transition-all hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)]"
          onclick={() => { showSnippets = !showSnippets; if (showSnippets) loadSnippets(); }}
          title="Insert Snippet"
        >
          <BookmarkIcon size="10" />SNIPPET
        </button>
        {#if showSnippets}
          <div class="absolute right-0 top-full z-50 mt-1 w-64 border hairline-strong surface-2 shadow-2xl fade-up" style="backdrop-filter: blur(12px) saturate(1.2); box-shadow: 0 4px 20px rgba(0,0,0,0.5);">
            <div class="border-b hairline px-3 py-2 font-mono type-eyebrow text-[var(--color-text-2)]">Saved Snippets</div>
            <div class="max-h-60 overflow-y-auto p-1">
              {#if snippets.length === 0}
                <div class="px-3 py-4 text-center font-mono type-micro text-[var(--color-text-4)]">No snippets found.</div>
              {/if}
              {#each snippets as s (s.id)}
                <button
                  class="flex w-full flex-col gap-1 rounded-sm px-2 py-1.5 text-left transition-colors hover:bg-[var(--color-surface-3)]"
                  onclick={() => { applyingSnippet = s; showSnippets = false; }}
                >
                  <span class="font-mono type-caption font-bold text-[var(--color-text-1)] truncate">{s.name}</span>
                  <span class="font-mono type-nano text-[var(--color-text-3)] truncate">{s.body}</span>
                </button>
              {/each}
            </div>
          </div>
        {/if}
      </div>

      <!-- Side panel toggle (Ctrl+.) -->
      <button
        class="flex items-center gap-1 border px-1.5 py-px font-mono type-eyebrow transition-all {showSidePanel ? 'border-[var(--color-accent)]/40 bg-[var(--color-accent)]/8 text-[var(--color-accent)]' : 'border-[var(--color-line)] text-[var(--color-text-4)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)]'}"
        onclick={() => (showSidePanel = !showSidePanel)}
        title="Toggle side panel (Ctrl+.)"
      >
        <PanelRight size="10" />PANEL
      </button>

      {#if mode === "local"}
        <div class="relative ml-2">
          <button
            class="flex items-center gap-1.5 border border-[var(--color-line)] px-2 py-0.5 font-mono type-eyebrow text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
            onclick={() => (showHostPicker = !showHostPicker)}
          >
            <Server size="10" /><span>CONNECT</span>
          </button>
          {#if showHostPicker}
            <div class="absolute right-0 top-full z-30 mt-1 w-72 overflow-hidden border hairline-strong surface-2 shadow-2xl">
              <div class="border-b hairline px-3 py-2 font-mono type-eyebrow text-[var(--color-text-2)]">Saved hosts</div>
              <div class="max-h-64 overflow-y-auto">
                {#each app.hosts as h (h.id)}
                  {@const hasSavedPw = app.hasSavedPassword(h.id)}
                  <button
                    class="flex w-full items-center gap-2.5 px-3 py-2 text-left type-caption hover:bg-[var(--color-surface-3)] transition-colors"
                    onclick={() => switchToRemote(h.id)}
                  >
                    <div class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-[var(--color-accent-soft)] text-[var(--color-accent)]">
                      <Server size="13" />
                    </div>
                    <div class="min-w-0 flex-1">
                      <div class="flex items-center gap-1.5 truncate">
                        <span class="truncate font-medium text-[var(--color-text-1)]">{h.name}</span>
                        {#if h.environment === 'production'}
                          <span class="shrink-0 rounded px-1 py-px type-eyebrow" style="color:#ef4444;background:rgba(239,68,68,0.12);border:1px solid rgba(239,68,68,0.25)">prod</span>
                        {:else if h.environment === 'staging'}
                          <span class="shrink-0 rounded px-1 py-px type-eyebrow" style="color:#f59e0b;background:rgba(245,158,11,0.12);border:1px solid rgba(245,158,11,0.25)">stage</span>
                        {/if}
                      </div>
                      <div class="flex items-center gap-1 truncate type-micro text-[var(--color-text-3)] font-mono">
                        {h.username}@{h.host}:{h.port}
                        {#if hasSavedPw}<span class="text-[var(--color-accent)] opacity-70" title="Password saved">•</span>{/if}
                      </div>
                    </div>
                  </button>
                {/each}
                {#if app.hosts.length === 0}
                  <div class="px-3 py-5 text-center font-mono type-micro text-[var(--color-text-3)]">No saved hosts yet.</div>
                {/if}
              </div>
            </div>
          {/if}
        </div>
      {:else}
        <button
          class="ml-2 flex items-center gap-1.5 border border-[var(--color-danger)]/30 px-2 py-0.5 font-mono type-eyebrow text-[var(--color-danger)] hover:bg-[var(--color-danger)]/10 transition-all"
          onclick={disconnectRemote}
        >
          <Unplug size="10" /><span>DISCONNECT</span>
        </button>
      {/if}
    </div>
  </div>

  <!-- Main area: terminal + optional side panel -->
  <div class="flex flex-1 overflow-hidden">
    <div bind:this={containerEl} class="relative flex-1 overflow-hidden p-1.5">

      <!-- Autocomplete popup -->
      {#if showAutocomplete && (status === "running" || status === "connected")}
        <AutocompletePopup
          prefix={acBuffer}
          hostID={connectedHostID ?? ""}
          x={8}
          y={36}
          onAccept={acceptAutocomplete}
          onDismiss={() => (showAutocomplete = false)}
        />
      {/if}

      <!-- Search bar overlay -->
      {#if showSearch}
        <div class="absolute right-4 top-2 z-30 flex items-center gap-2 border hairline-strong surface-2 p-1.5 shadow-xl fade-up" style="backdrop-filter: blur(8px);">
          <Search size="12" class="ml-1 text-[var(--color-text-4)]" />
          <input
            class="w-40 bg-transparent px-1 font-mono type-caption outline-none placeholder:text-[var(--color-text-4)]"
            placeholder="Find..."
            bind:value={searchQuery}
            use:focus
            oninput={() => searchAddon?.findNext(searchQuery)}
            onkeydown={(e) => {
              if (e.key === "Escape") { showSearch = false; term?.focus(); }
              else if (e.key === "Enter") { if (e.shiftKey) searchAddon?.findPrevious(searchQuery); else searchAddon?.findNext(searchQuery); }
            }}
          />
          <span class="px-1 font-mono type-micro text-[var(--color-text-4)]">
            {searchResults.resultCount > 0 ? `${searchResults.resultIndex + 1}/${searchResults.resultCount}` : '0/0'}
          </span>
          <button class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]" onclick={() => searchAddon?.findPrevious(searchQuery)}><ChevronUp size="12" /></button>
          <button class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]" onclick={() => searchAddon?.findNext(searchQuery)}><ChevronDown size="12" /></button>
          <div class="h-4 w-px bg-[var(--color-line)]"></div>
          <button class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]" onclick={() => { showSearch = false; term?.focus(); }}><X size="12" /></button>
        </div>
      {/if}

      <!-- Splash screen -->
      {#if !hasTyped && (status === "running" || status === "connected")}
        <div class="pointer-events-none absolute inset-0 flex items-center justify-center fade-up z-10 bg-[var(--color-surface-0)]/40 backdrop-blur-[2px]">
          <div class="pointer-events-auto border hairline-strong surface-2 p-5 shadow-2xl min-w-[320px]">
            <div class="mb-4 flex items-center gap-2">
              {#if mode === "mosh"}
                <Wifi size="14" class="text-[var(--color-success)]" />
                <span class="font-mono type-eyebrow text-[var(--color-success)]">MOSH · {connectedHost?.name}</span>
              {:else}
                <TerminalIcon size="14" class="text-[var(--color-accent)]" />
                <span class="font-mono type-eyebrow text-[var(--color-text-1)]">
                  {mode === "local" ? "Local Shell Ready" : `Connected to ${connectedHost?.name ?? connectedHostID}`}
                </span>
              {/if}
            </div>
            <div class="space-y-3">
              <div class="font-mono type-eyebrow text-[var(--color-text-4)]">Quick Commands</div>
              <div class="flex flex-wrap gap-2">
                {#each ['htop', 'df -h', 'docker ps -a', 'uptime'] as cmd}
                  <button
                    class="rounded-sm border hairline bg-[var(--color-surface-3)] px-2.5 py-1.5 font-mono type-micro text-[var(--color-text-2)] hover:border-[var(--color-accent)]/50 hover:text-[var(--color-accent)] hover:bg-[var(--color-accent)]/5 transition-all"
                    onclick={() => { term?.paste(cmd); term?.focus(); }}
                  >{cmd}</button>
                {/each}
              </div>
              <div class="mt-3 font-mono type-nano text-[var(--color-text-4)]">
                <kbd class="rounded border hairline px-1">↓</kbd> for autocomplete · <kbd class="rounded border hairline px-1">Ctrl+.</kbd> for panel
              </div>
            </div>
            <div class="mt-5 text-center font-mono type-nano text-[var(--color-text-4)]">Start typing to dismiss</div>
          </div>
        </div>
      {/if}
    </div>

    <!-- Side panel -->
    {#if showSidePanel}
      <TerminalSidePanel
        hostID={connectedHostID}
        onInsert={(text) => { writeLocal(text); term?.focus(); }}
        onClose={() => (showSidePanel = false)}
      />
    {/if}
  </div>

  <!-- Password prompt -->
  {#if promptingPassword && app.selectedHostID}
    {@const host = app.hosts.find((h) => h.id === app.selectedHostID)}
    {#if host}
      <div class="absolute inset-0 z-20 flex items-center justify-center bg-black/70">
        <div class="w-80 overflow-hidden border hairline-strong surface-2 shadow-2xl shadow-black/80">
          <div class="flex items-center gap-2 border-b hairline px-4 py-2.5">
            <Lock size="11" class="text-[var(--color-accent)]" />
            <span class="font-mono type-eyebrow text-[var(--color-text-1)]">AUTH REQUIRED</span>
          </div>
          <div class="p-4">
            <div class="mb-3 font-mono type-micro text-[var(--color-text-3)]">
              Password for <span class="text-[var(--color-accent)]">{host.username}@{host.host}</span>
            </div>
            <input
              type="password"
              class="w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-caption outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50"
              bind:value={runtimePassword} placeholder="•••••••••" use:focus
              onkeydown={(e) => e.key === "Enter" && submitPassword()}
            />
            <label class="mt-2.5 flex items-center gap-2">
              <input type="checkbox" class="accent-[var(--color-accent)]" bind:checked={rememberPassword} />
              <span class="font-mono type-eyebrow text-[var(--color-text-3)]">Save to vault for this host</span>
            </label>
          </div>
          <div class="flex items-center justify-end gap-2 border-t hairline px-4 py-2.5">
            <button class="border border-[var(--color-line)] px-3 py-1.5 font-mono type-eyebrow text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all" onclick={() => { promptingPassword = false; void openLocal(); }}>CANCEL</button>
            <button class="border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 px-3 py-1.5 font-mono type-eyebrow text-[var(--color-accent)] hover:bg-[var(--color-accent)]/15 disabled:opacity-30 disabled:cursor-not-allowed transition-all" disabled={!runtimePassword} onclick={submitPassword}>CONNECT</button>
          </div>
        </div>
      </div>
    {/if}
  {/if}

  <!-- TOFU prompt -->
  {#if promptingTofu && tofuPayload}
    <div class="absolute inset-0 z-20 flex items-center justify-center bg-black/70">
      <div class="w-96 overflow-hidden border border-[var(--color-warn)] surface-2 shadow-2xl shadow-black/80">
        <div class="flex items-center gap-2 border-b hairline px-4 py-2.5 bg-[var(--color-warn)]/10">
          <AlertTriangle size="11" class="text-[var(--color-warn)]" />
          <span class="font-mono type-eyebrow text-[var(--color-warn)]">UNKNOWN HOST KEY</span>
        </div>
        <div class="p-4 font-mono">
          <p class="mb-3 type-caption text-[var(--color-text-2)] leading-relaxed">
            The authenticity of host <span class="text-[var(--color-accent)]">{tofuPayload.host}</span> can't be established.
          </p>
          <div class="mb-4 bg-[var(--color-surface-3)] p-3 border hairline type-micro space-y-1.5">
            <div class="text-[var(--color-text-4)]">Key Type</div>
            <div class="text-[var(--color-text-1)]">{tofuPayload.keyType}</div>
            <div class="text-[var(--color-text-4)] mt-2">Fingerprint</div>
            <div class="text-[var(--color-text-1)] break-all">{tofuPayload.presentedFp}</div>
          </div>
          <p class="type-micro text-[var(--color-text-3)]">Are you sure you want to continue connecting?</p>
        </div>
        <div class="flex items-center justify-end gap-2 border-t hairline px-4 py-2.5">
          <button class="border border-[var(--color-line)] px-3 py-1.5 font-mono type-eyebrow text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all" onclick={() => { promptingTofu = false; void openLocal(); }}>CANCEL</button>
          <button class="border border-[var(--color-warn)]/50 bg-[var(--color-warn)]/10 px-3 py-1.5 font-mono type-eyebrow text-[var(--color-warn)] hover:bg-[var(--color-warn)]/15 transition-all" onclick={approveTofu}>TRUST & CONNECT</button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Sudo pill -->
  {#if showSudoPill && (status === "running" || status === "connected")}
    {@const hasPw = hasSudoPassword()}
    <div class="absolute bottom-3 left-1/2 z-30 -translate-x-1/2 fade-up">
      <div class="flex items-center gap-2 border border-[var(--color-warn)]/50 bg-[var(--color-surface-2)]/95 px-3 py-2 shadow-2xl shadow-black/60" style="backdrop-filter: blur(12px); box-shadow: 0 0 20px rgba(255,170,0,0.08), 0 8px 32px rgba(0,0,0,0.5);">
        <ShieldCheck size="13" class="shrink-0 text-[var(--color-warn)]" />
        {#if sudoInlineInput}
          <input type="password" class="w-44 border hairline bg-[var(--color-surface-3)] px-2 py-1 font-mono type-caption text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-warn)]/50" bind:value={sudoInlinePassword} placeholder="sudo password" use:focus onkeydown={(e) => e.key === "Enter" && sendInlineSudoPassword()} />
          <button class="border border-[var(--color-warn)]/50 bg-[var(--color-warn)]/10 px-2 py-1 font-mono type-eyebrow text-[var(--color-warn)] hover:bg-[var(--color-warn)]/20 disabled:opacity-30 transition-all" disabled={!sudoInlinePassword} onclick={sendInlineSudoPassword}>SEND</button>
        {:else}
          <span class="font-mono type-micro text-[var(--color-text-2)]">{hasPw ? "sudo password ready" : "sudo prompt detected"}</span>
          <button class="flex items-center gap-1.5 border border-[var(--color-warn)]/50 bg-[var(--color-warn)]/10 px-2.5 py-1 font-mono type-eyebrow text-[var(--color-warn)] hover:bg-[var(--color-warn)]/20 transition-all" onclick={sendSudoPassword}>
            {#if hasPw}<ShieldCheck size="9" />AUTO-FILL{:else}TYPE PASSWORD{/if}
          </button>
          {#if hasPw}
            <button class="flex items-center gap-1.5 border border-[var(--color-warn)]/50 bg-transparent px-2 py-1 font-mono type-eyebrow text-[var(--color-warn)] hover:bg-[var(--color-warn)]/20 transition-all" onclick={() => (sudoInlineInput = true)}>EDIT</button>
          {/if}
          <span class="font-mono type-nano text-[var(--color-text-4)] tracking-wider">CTRL+SHIFT+S</span>
        {/if}
        <button class="ml-1 text-[var(--color-text-4)] hover:text-[var(--color-text-2)] transition-colors" onclick={dismissSudoPill}>&times;</button>
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
