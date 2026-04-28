<script lang="ts">
  import { PluginService } from "../../bindings/github.com/blacknode/blacknode";
  import type {
    PanelView,
    PluginInfo,
  } from "../../bindings/github.com/blacknode/blacknode/internal/plugin/models";
  import PageHeader from "./PageHeader.svelte";
  import {
    Puzzle,
    Play,
    RefreshCw,
    AlertTriangle,
    CheckCircle2,
    Folder,
  } from "@lucide/svelte";

  let plugins = $state<PluginInfo[]>([]);
  let busy = $state(false);
  let root = $state("");

  async function refresh() {
    try {
      plugins = ((await PluginService.List()) ?? []) as PluginInfo[];
    } catch {
      plugins = [];
    }
  }
  async function loadAll() {
    busy = true;
    try {
      plugins = ((await PluginService.LoadAll()) ?? []) as PluginInfo[];
    } finally {
      busy = false;
    }
  }
  async function reload() {
    busy = true;
    try {
      plugins = ((await PluginService.Reload()) ?? []) as PluginInfo[];
    } finally {
      busy = false;
    }
  }

  $effect(() => {
    refresh();
    PluginService.Root().then((r) => (root = r));
  });
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Puzzle}
    title="Plugins"
    subtitle="Out-of-process plugins discovered from your data directory."
  />

  <div class="flex flex-1 flex-col overflow-y-auto">
    <div class="flex items-center gap-2 border-b hairline surface-1 px-4 py-2 text-[11px]">
      <Folder size="12" class="text-[var(--color-text-3)]" />
      <span class="font-mono text-[var(--color-text-3)]">{root || "—"}</span>
      <span class="text-[var(--color-text-4)]">·</span>
      <span class="text-[var(--color-text-4)]"
        >{plugins.length} discovered</span
      >
      <button
        class="ml-auto flex items-center gap-1.5 rounded-md border hairline-strong px-3 py-1.5 text-[11px] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
        disabled={busy}
        onclick={loadAll}
      >
        <Play size="11" />
        Load all
      </button>
      <button
        class="flex items-center gap-1.5 rounded-md border hairline-strong px-3 py-1.5 text-[11px] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
        disabled={busy}
        onclick={reload}
      >
        <RefreshCw size="11" />
        Reload
      </button>
    </div>

    {#if plugins.length === 0}
      <div class="flex flex-1 items-center justify-center">
        <div class="max-w-md text-center">
          <Puzzle size="22" class="mx-auto text-[var(--color-text-4)]" />
          <p class="mt-2 text-xs text-[var(--color-text-3)]">
            No plugins loaded. Drop a plugin directory containing
            <span class="font-mono">plugin.json</span> + an executable into
            the directory above, then click <em>Load all</em>.
          </p>
          <p class="mt-2 text-[10px] text-[var(--color-text-4)]">
            See <span class="font-mono">examples/plugin-hello/</span> in
            the repo for the manifest schema.
          </p>
        </div>
      </div>
    {:else}
      <ul class="grid grid-cols-1 gap-2 p-4 md:grid-cols-2">
        {#each plugins as p (p.id)}
          <li
            class="rounded-lg border hairline surface-2 p-4 transition-colors hover:border-[var(--color-accent)]/30"
          >
            <div class="flex items-center gap-2">
              <Puzzle size="13" class="text-[var(--color-accent)]" />
              <h4 class="text-[13px] font-semibold text-[var(--color-text-1)]">
                {p.name || p.id}
              </h4>
              <span class="font-mono text-[10px] text-[var(--color-text-4)]"
                >v{p.version}</span
              >
              <span class="ml-auto flex items-center gap-1 text-[10px]">
                {#if p.status === "loaded"}
                  <CheckCircle2 size="10" class="text-[var(--color-accent)]" />
                  <span class="text-[var(--color-accent)]">loaded</span>
                {:else if p.status === "failed"}
                  <AlertTriangle
                    size="10"
                    class="text-[var(--color-danger)]"
                  />
                  <span class="text-[var(--color-danger)]">failed</span>
                {:else}
                  <span class="text-[var(--color-text-4)]">{p.status}</span>
                {/if}
              </span>
            </div>
            {#if p.description}
              <p class="mt-1 text-[11px] text-[var(--color-text-3)]">
                {p.description}
              </p>
            {/if}
            {#if p.error}
              <p class="mt-2 rounded border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-1.5 font-mono text-[10px] text-[var(--color-danger)]">
                {p.error}
              </p>
            {/if}
            {#if p.panels && p.panels.length > 0}
              <ul class="mt-2 flex flex-wrap gap-1.5 text-[10px]">
                {#each p.panels as panel (panel.id)}
                  <li
                    class="rounded border hairline px-1.5 py-0.5 text-[var(--color-text-3)]"
                  >
                    panel: {panel.title}
                  </li>
                {/each}
              </ul>
            {/if}
            {#if p.permissions && p.permissions.length > 0}
              <ul class="mt-2 flex flex-wrap gap-1.5 text-[10px]">
                {#each p.permissions as perm (perm)}
                  <li
                    class="rounded border hairline px-1.5 py-0.5 text-[var(--color-text-4)]"
                  >
                    perm: {perm}
                  </li>
                {/each}
              </ul>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>
