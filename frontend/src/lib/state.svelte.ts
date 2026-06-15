import {
  HostService,
  VaultService,
  KeyService,
  SettingsService,
  AIService,
  RecordingService,
  PluginService,
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
  hostPasswords = $state<Record<string, string>>({});
  hostSudoPasswords = $state<Record<string, string>>({});
  loading = $state(false);
  paletteOpen = $state(false);
  aiOpen = $state(false);
  recordingsEnabled = $state(false);

  // Live metrics per host — populated by metrics:update events.
  hostMetrics = $state<Record<string, { cpuPercent: number; memPercent: number; diskPercent: number }>>({});

  // Track which hosts have active terminal sessions connected
  connectedHosts = $state<Set<string>>(new Set());

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
  // svelte-ignore state_referenced_locally
  broadcastSinks = $state<Record<string, (data: string) => void>>({});

  registerBroadcastSink(sessionID: string, write: (data: string) => void) {
    this.broadcastSinks[sessionID] = write;
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
  fanOutBroadcast(sourceSessionID: string, data: string) {
    if (!this.broadcastEnabled) return;
    if (!this.broadcastSet.has(sourceSessionID)) return;
    for (const sid of this.broadcastSet) {
      if (sid === sourceSessionID) continue;
      const sink = this.broadcastSinks[sid];
      if (sink) sink(data);
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

  async refreshVault() {
    this.vault = (await VaultService.Status()) as VaultStatus;
    if (this.vault.initialized && !this.vault.unlocked) {
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

  async refreshPasswords() {
    if (!this.vault.unlocked) {
      this.hostPasswords = {};
      return;
    }
    try {
      const map = (await HostService.GetAllPasswords()) as Record<string, string> | null;
      if (map) {
        // Merge: keep any in-session passwords already set, and fill in saved ones
        for (const [id, pw] of Object.entries(map)) {
          if (pw) this.hostPasswords[id] = pw;
        }
      }
    } catch {
      // vault may not support this yet — ignore
    }
  }

  async refreshSudoPasswords() {
    if (!this.vault.unlocked) {
      this.hostSudoPasswords = {};
      return;
    }
    try {
      const map = (await HostService.GetAllSudoPasswords()) as Record<string, string> | null;
      if (map) {
        for (const [id, pw] of Object.entries(map)) {
          if (pw) this.hostSudoPasswords[id] = pw;
        }
      }
    } catch {
      // ignore
    }
  }

  setSudoPassword(hostID: string, password: string) {
    this.hostSudoPasswords[hostID] = password;
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
      await this.refreshPasswords();
      await this.refreshSudoPasswords();
    } finally {
      this.loading = false;
    }
  }

  setPassword(hostID: string, password: string) {
    this.hostPasswords[hostID] = password;
  }

  // Cheap debounce for auto-lock activity pings — many DOM events fire fast.
  #lastTouch = 0;
  touchActivity() {
    const now = Date.now();
    if (now - this.#lastTouch < 5_000) return;
    this.#lastTouch = now;
    void AIService; // ensure tree-shaker keeps the import
    void this.#callTouch();
  }

  async #callTouch() {
    try {
      const { AutoLockService } = await import(
        "../../bindings/github.com/blacknode/blacknode/internal/service"
      );
      await AutoLockService.Touch();
    } catch {
      // service unavailable — ignore
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
