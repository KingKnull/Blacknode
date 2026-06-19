<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { ActivityService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Activity } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import PageHeader from "./PageHeader.svelte";
  import EmptyState from "./EmptyState.svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";
  import Skeleton from "./Skeleton.svelte";
  import {
    Activity as ActivityIcon,
    AlertTriangle,
    AlertCircle,
    Info,
    Filter,
    Trash2,
    Loader2,
  } from "@lucide/svelte";

  let entries = $state<Activity[]>([]);
  let sources = $state<string[]>([]);
  let selectedSources = $state<Set<string>>(new Set());
  let levelFilter = $state<"all" | "info" | "warn" | "error">("all");
  let busy = $state(false);
  let purging = $state(false);
  let off: (() => void) | null = null;

  async function refresh() {
    busy = true;
    try {
      const f: any = { limit: 200 };
      if (selectedSources.size > 0) f.sources = [...selectedSources];
      if (levelFilter !== "all") f.levels = [levelFilter];
      entries = ((await ActivityService.List(f)) ?? []) as Activity[];
      sources = ((await ActivityService.Sources()) ?? []) as string[];
    } finally {
      busy = false;
    }
  }

  // Re-fetch whenever the user changes filters. The realtime stream
  // (below) keeps the list current between fetches; combining the two
  // means the UI reacts instantly without dropping older history.
  $effect(() => {
    void selectedSources;
    void levelFilter;
    refresh();
  });

  onMount(() => {
    off = Events.On("activity:append", (e: any) => {
      // The Wails event payload is a single Activity; prepend if it
      // matches the current filters, otherwise drop it (it'll show up
      // when the filter is cleared).
      const a = e?.data as Activity | undefined;
      if (!a) return;
      if (selectedSources.size > 0 && !selectedSources.has(a.source)) return;
      if (levelFilter !== "all" && a.level !== levelFilter) return;
      entries = [a, ...entries].slice(0, 200);
      if (!sources.includes(a.source)) {
        sources = [...sources, a.source].sort();
      }
    });
  });

  onDestroy(() => {
    off?.();
  });

  function toggleSource(src: string) {
    const next = new Set(selectedSources);
    if (next.has(src)) next.delete(src);
    else next.add(src);
    selectedSources = next;
  }

  let confirmPurge = $state(false);

  async function purgeOld() {
    confirmPurge = false;
    purging = true;
    try {
      await ActivityService.PurgeOlderThanDays(30);
      await refresh();
    } finally {
      purging = false;
    }
  }

  function fmtTime(unix: number): string {
    const d = new Date(unix * 1000);
    const today = new Date();
    if (
      d.getFullYear() === today.getFullYear() &&
      d.getMonth() === today.getMonth() &&
      d.getDate() === today.getDate()
    ) {
      return d.toLocaleTimeString();
    }
    return d.toLocaleString();
  }

  function levelColor(l: string): string {
    if (l === "error") return "text-[var(--color-danger)]";
    if (l === "warn") return "text-[var(--color-warn)]";
    return "text-[var(--color-text-3)]";
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={ActivityIcon}
    title="Activity"
    subtitle="Unified feed across vault, exec, sync, plugins, and more."
  />

  <div class="flex items-center gap-2 border-b hairline surface-1 px-4 py-2 type-caption">
    <Filter size="11" class="text-[var(--color-text-3)]" />
    <select
      class="rounded-md border hairline bg-[var(--color-surface-3)] px-2 py-1 type-caption outline-none"
      bind:value={levelFilter}
    >
      <option value="all">all levels</option>
      <option value="info">info</option>
      <option value="warn">warn</option>
      <option value="error">error</option>
    </select>

    {#if sources.length > 0}
      <span class="ml-2 type-eyebrow text-[var(--color-text-3)]"
        >sources</span
      >
      {#each sources as src (src)}
        <button
          class="rounded border hairline px-1.5 py-0.5 type-micro transition-colors {selectedSources.has( src, ) ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent-soft)] text-[var(--color-text-1)]' : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)]'}"
          onclick={() => toggleSource(src)}
        >
          {src}
        </button>
      {/each}
    {/if}

    <span class="ml-auto type-micro text-[var(--color-text-4)]"
      >{entries.length} entries</span
    >
    <button
      class="flex items-center gap-1 rounded-md border hairline-strong px-2 py-1 type-micro text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)] disabled:opacity-50"
      disabled={purging}
      onclick={() => (confirmPurge = true)}
      title="Delete entries older than 30 days"
    >
      {#if purging}
        <Loader2 size="10" class="animate-spin" />
      {:else}
        <Trash2 size="10" />
      {/if}
      Purge old
    </button>
  </div>

  <div class="flex-1 overflow-y-auto">
    {#if busy && entries.length === 0}
      <Skeleton rows={10} rowClass="h-8" />
    {:else if entries.length === 0}
      <EmptyState
        icon={ActivityIcon}
        title="No activity yet"
        description="Unlock the vault, run a command, or push a sync — entries appear here as they happen."
      />
    {:else}
      <ul class="divide-y hairline">
        {#each entries as a (a.id)}
          <li class="flex items-start gap-3 px-4 py-2.5">
            <span class="mt-0.5 shrink-0 {levelColor(a.level)}">
              {#if a.level === "error"}
                <AlertCircle size="12" />
              {:else if a.level === "warn"}
                <AlertTriangle size="12" />
              {:else}
                <Info size="12" />
              {/if}
            </span>
            <div class="flex-1 min-w-0">
              <div class="flex items-baseline gap-2">
                <span class="font-mono type-eyebrow text-[var(--color-text-3)]"
                  >{a.source}</span
                >
                <span class="type-body text-[var(--color-text-1)]"
                  >{a.title}</span
                >
                {#if a.hostName}
                  <span class="font-mono type-micro text-[var(--color-text-4)]"
                    >· {a.hostName}</span
                  >
                {/if}
                <span
                  class="ml-auto shrink-0 font-mono type-micro text-[var(--color-text-4)]"
                  >{fmtTime(a.at)}</span
                >
              </div>
              {#if a.body}
                <p class="mt-0.5 truncate type-caption text-[var(--color-text-3)]">
                  {a.body}
                </p>
              {/if}
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>

{#if confirmPurge}
  <ConfirmDanger
    title="PURGE ACTIVITY"
    body="Delete all activity entries older than 30 days? This cannot be undone."
    severity="warn"
    productionHosts={[]}
    onCancel={() => (confirmPurge = false)}
    onConfirm={purgeOld}
  />
{/if}
