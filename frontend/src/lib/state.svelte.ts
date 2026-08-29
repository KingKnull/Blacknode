import {
  HostService,
  VaultService,
  KeyService,
  SettingsService,
  RecordingService,
  PluginService,
  AutoLockService,
} from "../../bindings/github.com/blacknode/blacknode/internal/service";
import type { Host } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
import { Events } from "@wailsio/runtime";
import type { PanelView } from "../../bindings/github.com/blacknode/blacknode/internal/plugin/models";
import type {
  PublicKeyView,
  VaultStatus,
  AppSettings,
} from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
import { NotifyKind } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
import { checkCommand, type Danger } from "./danger";
import { bus } from "./events";

type View =
  | "terminals"
  | "exec"
  | "files"
  | "metrics"
  | "logs"
  | "forwards"
  | "recordings"
  | "containers"
  | "network"
  | "processes"
  | "http"
  | "database"
  | "snippets"
  | "history"
  | "topology"
  | "plugins"
  | "activity"
  | "vault"
  | "keys"
  | "settings"
  // Plugin-contributed panel ids are namespaced as `plugin:<pluginId>:<panelId>`.
  | `plugin:${string}:${string}`;

class AppState {
  view = $state<View>("terminals");
  vault = $state<VaultStatus>({ initialized: false, unlocked: false });
  hosts = $state<Host[]>([]);
  keys = $state<PublicKeyView[]>([]);
  settings = $state<AppSettings>({
    theme: "dark",
    autoLockMinutes: 15,
    defaultShellPath: "",
    metricsIntervalSeconds: 5,
    hasAnthropicKey: false,
  });
  selectedHostID = $state<string | null>(null);
  hostDetailOpen = $state(false);
  // Which hosts have credentials saved — booleans only. The plaintext lives in
  // the vault and is resolved by the Go connect path; panels pass no password
  // at all. See internal/sshconn/dialer.go (ResolveSecret).
  secretStatus = $state<Record<string, { hasPassword: boolean; hasSudo: boolean }>>({});
  loading = $state(false);
  paletteOpen = $state(false);
  aiOpen = $state(false);
  recordingsEnabled = $state(false);

  // Live metrics per host — populated by metrics:update events.
  hostMetrics = $state<Record<string, { cpuPercent: number; memPercent: number; diskPercent: number }>>({});

  // Per-session activity status, surfaced as colored dots on terminal tabs.
  //   unread      → output arrived while the pane was unfocused (green)
  //   needs-input → a prompt (e.g. sudo password) is waiting (amber)
  //   error       → the session failed / errored (red)
  // Cleared back to idle when the pane regains focus.
  sessionStatus = $state<Record<string, "idle" | "unread" | "needs-input" | "error">>({});

  setSessionStatus(sessionID: string, s: "idle" | "unread" | "needs-input" | "error") {
    if (this.sessionStatus[sessionID] === s) return;
    this.sessionStatus = { ...this.sessionStatus, [sessionID]: s };
  }
  // Mark unread without clobbering a more urgent state.
  markSessionUnread(sessionID: string) {
    const cur = this.sessionStatus[sessionID];
    if (cur === "error" || cur === "needs-input") return;
    this.setSessionStatus(sessionID, "unread");
  }
  clearSessionStatus(sessionID: string) {
    if (this.sessionStatus[sessionID] && this.sessionStatus[sessionID] !== "idle") {
      this.setSessionStatus(sessionID, "idle");
    }
  }
  forgetSession(sessionID: string) {
    if (sessionID in this.sessionStatus) {
      const next = { ...this.sessionStatus };
      delete next[sessionID];
      this.sessionStatus = next;
    }
    if (sessionID in this.sessionHosts) {
      delete this.sessionHosts[sessionID];
    }
  }

  // Track which hosts have active terminal sessions connected
  connectedHosts = $state<Set<string>>(new Set());

  // What each live pane is attached to, and how. Panes report this as they
  // connect and disconnect. Two things need it: naming the blast radius of a
  // broadcast command, and persisting the workspace for session restore.
  // `null` means the pane is on a local shell.
  sessionHosts = $state<Record<string, { hostID: string; via: "ssh" | "mosh" } | null>>({});

  setSessionHost(sessionID: string, host: { hostID: string; via: "ssh" | "mosh" } | null) {
    // Guarded so panes can report unconditionally on every state change without
    // waking every consumer of this map each time nothing actually moved.
    const cur = this.sessionHosts[sessionID] ?? null;
    if (!cur && !host) return;
    if (cur && host && cur.hostID === host.hostID && cur.via === host.via) return;
    this.sessionHosts[sessionID] = host;
  }

  addConnectedHost(hostID: string) {
    if (!this.connectedHosts.has(hostID)) {
      const next = new Set(this.connectedHosts);
      next.add(hostID);
      this.connectedHosts = next;
    }
  }

  removeConnectedHost(hostID: string) {
    if (this.connectedHosts.has(hostID)) {
      const next = new Set(this.connectedHosts);
      next.delete(hostID);
      this.connectedHosts = next;
    }
  }

  // Plugin-contributed panels surfaced in the sidebar nav.
  pluginPanels = $state<PanelView[]>([]);
  async refreshPluginPanels() {
    try {
      this.pluginPanels = ((await PluginService.Panels()) ?? []) as PanelView[];
    } catch {
      this.pluginPanels = [];
    }
  }

  // Cross-component channel: any panel can prefill the AI drawer with a mode
  // and a body, then open it. AIDrawer watches this and applies it.
  aiPrefill = $state<
    { id: string; mode: "translate" | "explain"; prompt: string } | null
  >(null);
  prefillAI(mode: "translate" | "explain", prompt: string) {
    this.aiPrefill = { id: crypto.randomUUID(), mode, prompt };
    this.aiOpen = true;
  }

  // Multi-cursor broadcast: when `broadcastEnabled` is true, every keystroke
  // typed in any session that's a member of `broadcastSet` is also written
  // to every *other* session in the set. Each Terminal registers a sink
  // function so we can fan out without each pane needing to know the
  // underlying mode (local PTY vs SSH).
  broadcastEnabled = $state(false);
  broadcastSet = $state<Set<string>>(new Set());
  // Each pane registers a write function so we can fan out without knowing its
  // mode. Which host it's pointed at isn't stored here — a pane can disconnect
  // and reconnect elsewhere while staying a broadcast member, so the danger
  // confirmation reads `sessionHosts` at prompt time instead of a snapshot.
  broadcastSinks = $state<Record<string, { write: (data: string) => void }>>({});

  // Line being composed in the broadcast source pane, reconstructed from the
  // keystrokes flowing through fanOutBroadcast. We need it because the danger
  // check works on whole commands, and broadcast operates on raw keystrokes:
  // by the time Enter arrives the command exists only as the sum of what was
  // typed. Local echo already showed it in every pane; this is a parallel
  // record, not a second source of truth.
  #broadcastLine = "";

  // A dangerous command caught on its way out to the broadcast group. The
  // Workspace renders a confirmation for this; resolving it either fans the
  // command out to every member or drops it.
  pendingBroadcastDanger = $state<{
    id: string;
    sourceSessionID: string;
    command: string;
    danger: Danger;
    targets: number;
    productionHosts: string[];
  } | null>(null);

  registerBroadcastSink(sessionID: string, write: (data: string) => void) {
    this.broadcastSinks[sessionID] = { write };
  }
  unregisterBroadcastSink(sessionID: string) {
    delete this.broadcastSinks[sessionID];
    if (this.broadcastSet.has(sessionID)) {
      const next = new Set(this.broadcastSet);
      next.delete(sessionID);
      this.broadcastSet = next;
    }
  }
  toggleBroadcastMember(sessionID: string) {
    const next = new Set(this.broadcastSet);
    if (next.has(sessionID)) next.delete(sessionID);
    else next.add(sessionID);
    this.broadcastSet = next;
  }

  // Fan out from a source session to every OTHER session in the group.
  //
  // Broadcast is the one path where a single keystroke reaches N hosts at
  // once, so it gets the same dangerous-command check as multi-host exec —
  // running `mkfs` on twelve machines simultaneously is exactly the mistake
  // worth interrupting. The check fires on Enter, against the line assembled
  // so far; everything else fans out immediately.
  fanOutBroadcast(sourceSessionID: string, data: string) {
    if (!this.broadcastEnabled) return;
    if (!this.broadcastSet.has(sourceSessionID)) return;
    // Don't stack a second prompt on top of one already awaiting an answer.
    if (this.pendingBroadcastDanger) return;

    if (this.#isSubmit(data)) {
      const command = this.#broadcastLine;
      this.#broadcastLine = "";
      const danger = checkCommand(command);
      if (danger) {
        const targets = [...this.broadcastSet].filter((s) => s !== sourceSessionID);
        this.pendingBroadcastDanger = {
          id: crypto.randomUUID(),
          sourceSessionID,
          command,
          danger,
          targets: targets.length,
          productionHosts: this.#productionHostNames(targets),
        };
        return; // held until the user confirms
      }
    } else {
      this.#trackBroadcastLine(data);
    }

    this.#writeToBroadcastGroup(sourceSessionID, data);
  }

  // Release a held command to the rest of the group.
  confirmBroadcastDanger() {
    const pending = this.pendingBroadcastDanger;
    this.pendingBroadcastDanger = null;
    if (!pending) return;
    // Members never received the Enter, but they did receive the keystrokes
    // that preceded it, so the newline alone submits the line they're holding.
    this.#writeToBroadcastGroup(pending.sourceSessionID, "\r");
  }

  // Drop a held command, and clear the half-typed line from every member so
  // they aren't left holding a command the user just declined to run.
  cancelBroadcastDanger() {
    const pending = this.pendingBroadcastDanger;
    this.pendingBroadcastDanger = null;
    if (!pending) return;
    this.#writeToBroadcastGroup(pending.sourceSessionID, "\x15"); // Ctrl-U: kill line
  }

  #writeToBroadcastGroup(sourceSessionID: string, data: string) {
    for (const sid of this.broadcastSet) {
      if (sid === sourceSessionID) continue;
      this.broadcastSinks[sid]?.write(data);
    }
  }

  // Names of production-tagged hosts among the given sessions, deduped.
  #productionHostNames(sessionIDs: string[]): string[] {
    const names = new Set<string>();
    for (const sid of sessionIDs) {
      const attached = this.sessionHosts[sid];
      if (!attached) continue;
      const host = this.hosts.find((h) => h.id === attached.hostID);
      if (!host) continue;
      if ((host.environment ?? "").toLowerCase() === "production") names.add(host.name);
    }
    return [...names];
  }

  #isSubmit(data: string): boolean {
    return data === "\r" || data === "\n" || data === "\r\n";
  }

  // Maintain the in-flight line: printable input appends, backspace removes,
  // and the usual line-kill / interrupt controls reset it.
  #trackBroadcastLine(data: string) {
    if (data === "\x7f" || data === "\b") {
      this.#broadcastLine = this.#broadcastLine.slice(0, -1);
      return;
    }
    // Ctrl-C, Ctrl-U, Ctrl-D, Escape — the line is gone as far as the shell
    // is concerned, so stop tracking it.
    if (data === "\x03" || data === "\x15" || data === "\x04" || data === "\x1b") {
      this.#broadcastLine = "";
      return;
    }
    // Ignore other control sequences (arrows, function keys) rather than
    // letting escape codes pollute the reconstructed command.
    if (data.length === 1 && data.charCodeAt(0) < 0x20) return;
    if (data.startsWith("\x1b")) return;
    this.#broadcastLine += data;
    // Bound the buffer; a pathological paste shouldn't grow it without limit.
    if (this.#broadcastLine.length > 4096) {
      this.#broadcastLine = this.#broadcastLine.slice(-4096);
    }
  }

  // Cross-component channel: AIDrawer/palette set this; the matching Terminal
  // sees the change via $effect and writes the text to its xterm/PTY.
  pendingTerminalInsert = $state<
    { id: string; sessionID: string; text: string } | null
  >(null);
  insertIntoTerminal(sessionID: string, text: string) {
    this.pendingTerminalInsert = {
      id: crypto.randomUUID(),
      sessionID,
      text,
    };
  }

  // ── Connect intents ───────────────────────────────────────────────
  // "Session X should connect to host Y." Opening a tab and telling its pane
  // where to connect are two steps, and the pane doesn't exist yet during the
  // first one — the bus drops events with no listeners, so a naive emit is
  // lost. Parking the intent here instead of guessing a mount delay means the
  // pane picks it up whenever it actually mounts, however long that takes.
  #connectIntents = new Map<string, { hostID: string; via: "ssh" | "mosh" }>();

  requestConnect(sessionID: string, hostID: string, via: "ssh" | "mosh" = "ssh") {
    this.#connectIntents.set(sessionID, { hostID, via });
    // A pane that's already mounted handles this immediately and clears the
    // intent; one that isn't will find it in takeConnectIntent on mount.
    bus.emit("connect-intent", { sessionID });
  }

  takeConnectIntent(sessionID: string): { hostID: string; via: "ssh" | "mosh" } | null {
    const intent = this.#connectIntents.get(sessionID);
    if (!intent) return null;
    this.#connectIntents.delete(sessionID);
    return intent;
  }

  // A pane that closes before ever mounting shouldn't leave its intent behind.
  forgetConnectIntent(sessionID: string) {
    this.#connectIntents.delete(sessionID);
  }

  // Set when the vault locks mid-session (idle timeout or the Lock button).
  // While true, refreshVault will NOT use the remember-me token to
  // auto-unlock — otherwise locking and remember-me cancel each other out and
  // the vault never stays locked. Cleared when the user unlocks with their
  // passphrase (VaultGate). Remember-me still auto-unlocks on app launch.
  suppressAutoUnlock = $state(false);

  async refreshVault() {
    this.vault = (await VaultService.Status()) as VaultStatus;
    if (this.vault.initialized && !this.vault.unlocked && !this.suppressAutoUnlock) {
      try {
        const ok = await VaultService.TryAutoUnlock();
        if (ok) {
          this.vault = (await VaultService.Status()) as VaultStatus;
        }
      } catch {
        // ignore auto-unlock failures — user will just see the lock screen
      }
    }
  }

  async refreshHosts() {
    this.hosts = ((await HostService.List()) ?? []) as Host[];
  }

  // Which hosts have a saved SSH / sudo password. Booleans only — enough to
  // render a "saved" dot and to decide whether to prompt at connect.
  async refreshSecretStatus() {
    if (!this.vault.unlocked) {
      this.secretStatus = {};
      return;
    }
    try {
      const map = (await HostService.SecretStatus()) as Record<
        string,
        { hasPassword: boolean; hasSudo: boolean }
      > | null;
      this.secretStatus = map ?? {};
    } catch {
      this.secretStatus = {};
    }
  }

  hasSavedPassword(hostID: string): boolean {
    return this.secretStatus[hostID]?.hasPassword ?? false;
  }

  hasSavedSudoPassword(hostID: string): boolean {
    return this.secretStatus[hostID]?.hasSudo ?? false;
  }

  async refreshKeys() {
    if (!this.vault.unlocked) {
      this.keys = [];
      return;
    }
    this.keys = ((await KeyService.List()) ?? []) as PublicKeyView[];
  }

  async refreshSettings() {
    if (!this.vault.unlocked) return;
    this.settings = (await SettingsService.Get()) as AppSettings;
    this.recordingsEnabled = (await RecordingService.IsEnabled()) ?? false;
  }

  async refreshAll() {
    this.loading = true;
    try {
      await this.refreshVault();
      await this.refreshHosts();
      await this.refreshKeys();
      await this.refreshSettings();
      await this.refreshSecretStatus();
    } finally {
      this.loading = false;
    }
  }

  // Cheap debounce for auto-lock activity pings — many DOM events fire fast.
  #lastTouch = 0;
  #touchWarned = false;
  touchActivity() {
    const now = Date.now();
    if (now - this.#lastTouch < 5_000) return;
    this.#lastTouch = now;
    void this.#callTouch();
  }

  async #callTouch() {
    try {
      await AutoLockService.Touch();
      this.#touchWarned = false;
    } catch {
      // If activity pings can't reach the backend, auto-lock will fire while
      // the user is actively working. Warn once instead of failing silently.
      if (!this.#touchWarned && this.settings.autoLockMinutes > 0) {
        this.#touchWarned = true;
        this.toast(
          "warn",
          "Activity tracking unavailable",
          "The vault may auto-lock while you're still working.",
        );
      }
    }
  }

  // Frontend-only notification helper.
  toast(kind: "ok" | "warn" | "error" | "info", title: string, body: string = "") {
    const kindMap = {
      ok: NotifyKind.NotifyOK,
      warn: NotifyKind.NotifyWarn,
      error: NotifyKind.NotifyError,
      info: NotifyKind.NotifyInfo,
    };
    Events.Emit("notification:toast", {
      id: crypto.randomUUID(),
      kind: kindMap[kind],
      title,
      body,
      source: "APP",
      timestamp: Math.floor(Date.now() / 1000),
    });
  }
}

export const app = new AppState();
export type { View };
