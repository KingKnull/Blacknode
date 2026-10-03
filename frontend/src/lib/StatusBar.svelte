<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { app } from "./state.svelte";
  import { SyncService, UpdateService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { SyncStatus } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import {
    TerminalSquare,
    Server,
    Sparkles,
    Radio,
    Cloud,
    CloudOff,
    Loader2,
  } from "@lucide/svelte";

  type Props = {
    tabCount: number;
    activeLeafCount: number;
  };
  let { tabCount, activeLeafCount }: Props = $props();

  // ── Cloud sync indicator ────────────────────────────────────────────
  let syncStatus = $state<SyncStatus | null>(null);
  let syncBusy = $state(false);
  let pollTimer: ReturnType<typeof setInterval> | undefined;

  async function refreshSyncStatus() {
    try {
      syncStatus = (await SyncService.Status()) as SyncStatus;
    } catch {
      // ignore — sync may not be configured
    }
  }

  // dot color: green < 5min, yellow < 30min, red on error, grey if never synced
  type DotTone = "green" | "yellow" | "red" | "grey";
  let dotTone = $derived.by<DotTone>(() => {
    if (!syncStatus || !syncStatus.configured) return "grey";
    if (syncStatus.lastError) return "red";
    const lastSync = Math.max(syncStatus.lastPushAt, syncStatus.lastPullAt);
    if (!lastSync) return "grey";
    const ageMin = (Date.now() / 1000 - lastSync) / 60;
    if (ageMin < 5) return "green";
    if (ageMin < 30) return "yellow";
    return "red";
  });

  const TONE_COLOR: Record<DotTone, string> = {
    green: "var(--color-success)",
    yellow: "var(--color-warn)",
    red: "var(--color-danger)",
    grey: "var(--color-text-4)",
  };

  async function quickSync() {
    if (syncBusy || !syncStatus?.configured) return;
    syncBusy = true;
    try {
      syncStatus = (await SyncService.AutoSync()) as SyncStatus;
    } catch (e: any) {
      syncStatus = { ...(syncStatus ?? ({} as SyncStatus)), lastError: String(e?.message ?? e) };
    } finally {
      syncBusy = false;
    }
  }

  function openSyncSettings() {
    app.view = "settings";
    // Settings panel scrolls to #section-sync on its own section nav;
    // a small delay lets the panel mount before we try to scroll.
    setTimeout(() => {
      document.getElementById("section-sync")?.scrollIntoView({ behavior: "smooth" });
    }, 100);
  }

  function fmtAge(unix: number): string {
    if (!unix) return "never";
    const secs = Math.floor(Date.now() / 1000 - unix);
    if (secs < 60) return "just now";
    if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
    if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
    return `${Math.floor(secs / 86400)}d ago`;
  }

  // Single source of truth for the version — the hardcoded fallback only
  // shows if the backend call fails.
  let version = $state("v0.1");
  onMount(() => {
    refreshSyncStatus();
    pollTimer = setInterval(refreshSyncStatus, 60_000);
    UpdateService.CurrentVersion()
      .then((v) => { if (v) version = `v${v}`; })
      .catch(() => {});
  });
  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer);
  });
</script>

<footer class="flex h-6 shrink-0 items-center gap-4 border-t hairline surface-1 px-3 type-micro text-[var(--color-text-4)] select-none">
  <span class="flex items-center gap-1.5">
    <Server size="11" /> <span class="tabular">{app.hosts.length}</span> {app.hosts.length === 1 ? 'host' : 'hosts'}
  </span>
  <span class="flex items-center gap-1.5">
    <TerminalSquare size="11" /> <span class="tabular">{tabCount}</span> {tabCount === 1 ? 'tab' : 'tabs'}{activeLeafCount > 1 ? ` · ${activeLeafCount} panes` : ''}
  </span>
  {#if app.settings.hasAnthropicKey}
    <span class="flex items-center gap-1.5 text-[var(--color-accent)]">
      <Sparkles size="11" /> AI ready
    </span>
  {/if}
  {#if app.broadcastEnabled}
    <span class="flex items-center gap-1.5 text-[var(--color-warn)]">
      <Radio size="11" class="pulse-soft" /> Broadcasting
    </span>
  {/if}

  <!-- Cloud sync indicator -->
  {#if syncStatus?.configured}
    <button
      class="flex items-center gap-1.5 transition-colors hover:text-[var(--color-text-2)]"
      onclick={quickSync}
      ondblclick={openSyncSettings}
      title={syncStatus.lastError
        ? `Sync error: ${syncStatus.lastError} — click to retry, double-click for settings`
        : `Last push ${fmtAge(syncStatus.lastPushAt)} · Last pull ${fmtAge(syncStatus.lastPullAt)} — click to sync now`}
    >
      {#if syncBusy}
        <Loader2 size="11" class="animate-spin" style="color: var(--color-accent)" />
      {:else}
        <span class="relative flex h-2.5 w-2.5 items-center justify-center">
          <Cloud size="11" style="color: {TONE_COLOR[dotTone]}" />
          <span
            class="absolute -right-0.5 -top-0.5 h-1.5 w-1.5 rounded-full {dotTone === 'green' ? 'pulse-soft' : ''}"
            style="background: {TONE_COLOR[dotTone]}"
          ></span>
        </span>
      {/if}
      <span>Sync</span>
    </button>
  {:else}
    <button
      class="flex items-center gap-1.5 opacity-60 transition-opacity hover:opacity-100"
      onclick={openSyncSettings}
      title="Cloud sync not configured — click to set up"
    >
      <CloudOff size="11" /> Sync off
    </button>
  {/if}

  <span class="ml-auto text-[var(--color-text-4)]">{version}</span>
</footer>
