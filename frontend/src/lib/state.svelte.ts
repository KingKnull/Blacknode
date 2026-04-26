import {
  HostService,
  VaultService,
  KeyService,
  SettingsService,
  AIService,
  RecordingService,
} from "../../bindings/github.com/blacknode/blacknode";
import type { Host } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
import type {
  PublicKeyView,
  VaultStatus,
  AppSettings,
} from "../../bindings/github.com/blacknode/blacknode/models";

type View =
  | "terminals"
  | "exec"
  | "files"
  | "metrics"
  | "logs"
  | "forwards"
  | "recordings"
  | "keys"
  | "settings";

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
  hostPasswords = $state<Record<string, string>>({});
  loading = $state(false);
  paletteOpen = $state(false);
  aiOpen = $state(false);
  recordingsEnabled = $state(false);

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
  }

  async refreshHosts() {
    this.hosts = ((await HostService.List()) ?? []) as Host[];
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
        "../../bindings/github.com/blacknode/blacknode"
      );
      await AutoLockService.Touch();
    } catch {
      // service unavailable — ignore
    }
  }
}

export const app = new AppState();
export type { View };
