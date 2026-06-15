<script lang="ts">
  import { onMount } from "svelte";
  import { HistoryService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { HistoryEntry } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import { bus } from "./events";
  import PageHeader from "./PageHeader.svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";
  import Skeleton from "./Skeleton.svelte";
  import {
    History as HistoryIcon,
    Search,
    Trash2,
    Server,
    Zap,
    Sparkles,
    Bookmark,
    Loader2,
    Copy,
    ArrowRight,
    Check,
    AlertTriangle,
  } from "@lucide/svelte";

  let entries = $state<HistoryEntry[]>([]);
  let query = $state("");
  let hostFilter = $state(""); // hostID or ""
  let sourceFilter = $state(""); // exec | ai-translate | snippet | ""
  let loading = $state(false);
  let searching = $state(false);

  onMount(refresh);

  async function refresh() {
    loading = true;
    try {
      entries = ((await HistoryService.List(hostFilter, sourceFilter, 500)) ??
        []) as HistoryEntry[];
    } finally {
      loading = false;
    }
  }

  async function runSearch() {
    if (!query.trim()) {
      await refresh();
      return;
    }
    searching = true;
    try {
      entries = ((await HistoryService.Search(query)) ?? []) as HistoryEntry[];
    } finally {
      searching = false;
    }
  }

  $effect(() => {
    void hostFilter;
    void sourceFilter;
    if (!query) refresh();
  });

  async function del(e: HistoryEntry) {
    await HistoryService.Delete(e.id);
    entries = entries.filter((x) => x.id !== e.id);
  }

  let confirmClear = $state(false);

  async function clearAll() {
    confirmClear = false;
    await HistoryService.Clear();
    entries = [];
  }

  function copy(text: string) {
    void navigator.clipboard.writeText(text);
    app.toast('ok', 'COPIED', 'Command copied to clipboard.');
  }

  function insertIntoActiveTerminal(rendered: string) {
    bus.emit('insert-into-active-terminal', rendered);
    app.view = "terminals";
  }

  function fmtTime(unix: number) {
    if (!unix) return "";
    const d = new Date(unix * 1000);
    return d.toLocaleString();
  }

  function sourceIcon(source: string) {
    if (source === "ai-translate") return Sparkles;
    if (source === "snippet") return Bookmark;
    return Zap; // exec
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={HistoryIcon}
    title="Command history"
    subtitle="Auto-captured from Multi-host runs, AI translations, and snippet applies"
  >
    {#snippet actions()}
      <button
        class="flex items-center gap-1 rounded-md border hairline-strong px-2.5 py-1 type-caption text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
        onclick={() => (confirmClear = true)}
      >
        <Trash2 size="11" /> clear all
      </button>
    {/snippet}
  </PageHeader>

  <div class="space-y-2 border-b hairline surface-1 px-4 py-3">
    <div
      class="relative flex items-center rounded-md border hairline bg-[var(--color-surface-3)] focus-within:border-[var(--color-accent)]/40"
    >
      <Search size="13" class="absolute left-3 text-[var(--color-text-4)]" />
      <input
        bind:value={query}
        onkeydown={(e) => e.key === "Enter" && runSearch()}
        placeholder="search command bodies (case-insensitive)…"
        class="w-full bg-transparent py-2 pl-9 pr-3 type-body outline-none placeholder:text-[var(--color-text-4)]"
      />
      {#if searching || loading}
        <Loader2 size="12" class="mr-3 animate-spin text-[var(--color-text-3)]" />
      {/if}
      {#if query}
        <button
          class="mr-2 rounded px-2 py-0.5 type-micro text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={() => {
            query = "";
            void refresh();
          }}
        >
          clear
        </button>
      {/if}
    </div>
    <div class="flex flex-wrap items-center gap-2 type-caption">
      <span class="text-[var(--color-text-3)]">filters:</span>
      <select
        class="rounded-md border hairline bg-[var(--color-surface-3)] px-2 py-1 outline-none"
        bind:value={hostFilter}
      >
        <option value="">any host</option>
        {#each app.hosts as h (h.id)}
          <option value={h.id}>{h.name}</option>
        {/each}
      </select>
      <select
        class="rounded-md border hairline bg-[var(--color-surface-3)] px-2 py-1 outline-none"
        bind:value={sourceFilter}
      >
        <option value="">any source</option>
        <option value="exec">multi-host exec</option>
        <option value="ai-translate">AI translate</option>
        <option value="snippet">snippet apply</option>
      </select>
    </div>
  </div>

  <div class="flex-1 overflow-y-auto">
    {#if loading && entries.length === 0}
      <Skeleton rows={10} rowClass="h-10" />
    {:else if entries.length === 0}
      <div class="flex h-full items-center justify-center">
        <div class="max-w-md text-center">
          <HistoryIcon size="22" class="mx-auto text-[var(--color-text-4)]" />
          <p class="mt-2 type-caption text-[var(--color-text-3)]">
            No history yet. Commands appear here when you run via Multi-host,
            insert from the AI drawer, or apply a snippet.
          </p>
        </div>
      </div>
    {:else}
      <div class="divide-y divide-[var(--color-line)]">
        {#each entries as e (e.id)}
          {@const Icon = sourceIcon(e.source)}
          <div class="px-4 py-2.5 transition-colors hover:bg-[var(--color-surface-2)]">
            <div class="flex items-center gap-2 type-micro text-[var(--color-text-3)]">
              <Icon size="11" />
              <span>{e.source}</span>
              {#if e.hostName}
                <span class="flex items-center gap-1">
                  <Server size="10" />
                  {e.hostName}
                </span>
              {/if}
              {#if e.status === "ok"}
                <span class="flex items-center gap-1 text-[var(--color-accent)]">
                  <Check size="10" /> exit 0
                </span>
              {:else if e.status === "fail"}
                <span class="flex items-center gap-1 text-[var(--color-danger)]">
                  <AlertTriangle size="10" /> exit {e.exitCode}
                </span>
              {/if}
              <span class="ml-auto font-mono text-[var(--color-text-4)]"
                >{fmtTime(e.executedAt)}</span
              >
            </div>
            <div class="mt-1 flex items-start gap-2">
              <pre
                class="flex-1 overflow-x-auto whitespace-pre-wrap break-all font-mono type-caption text-[var(--color-text-1)]">{e.command}</pre>
              <div class="shrink-0 opacity-60 hover:opacity-100">
                <button
                  class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
                  onclick={() => copy(e.command)}
                  title="Copy"
                >
                  <Copy size="11" />
                </button>
                <button
                  class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
                  onclick={() => insertIntoActiveTerminal(e.command)}
                  title="Insert into active terminal"
                >
                  <ArrowRight size="11" />
                </button>
                <button
                  class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
                  onclick={() => del(e)}
                  title="Delete"
                >
                  <Trash2 size="11" />
                </button>
              </div>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

{#if confirmClear}
  <ConfirmDanger
    title="CLEAR HISTORY"
    body="Clear ALL command history? This cannot be undone."
    severity="warn"
    productionHosts={[]}
    onCancel={() => (confirmClear = false)}
    onConfirm={clearAll}
  />
{/if}
