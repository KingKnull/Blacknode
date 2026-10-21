<script lang="ts">
  import { onMount } from "svelte";
  import { HistoryService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { HistoryEntry } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import { bus } from "./events";
  import PageHeader from "./PageHeader.svelte";
  import EmptyState from "./EmptyState.svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";
  import Dialog from "./Dialog.svelte";
  import Skeleton from "./Skeleton.svelte";
  import {
    History as HistoryIcon,
    Search,
    Trash2,
    Server,
    Zap,
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
  let sourceFilter = $state(""); // exec | snippet | ""
  let loading = $state(false);
  let searching = $state(false);
  let reviewEntry = $state<HistoryEntry | null>(null);
  let reviewCommand = $state("");

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

  function reviewForInsert(entry: HistoryEntry) {
    reviewEntry = entry;
    reviewCommand = entry.command;
  }

  function closeReview() {
    reviewEntry = null;
    reviewCommand = "";
  }

  function insertReviewedCommand() {
    const command = reviewCommand.trim();
    if (!command) return;
    bus.emit('insert-into-active-terminal', command);
    app.view = "terminals";
    closeReview();
  }

  function fmtTime(unix: number) {
    if (!unix) return "";
    const d = new Date(unix * 1000);
    return d.toLocaleString();
  }

  function sourceIcon(source: string) {
    if (source === "snippet") return Bookmark;
    return Zap; // exec
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={HistoryIcon}
    title="Command history"
    subtitle="Auto-captured from Multi-host runs and snippet applies"
  >
    {#snippet actions()}
      {#if entries.length > 0}
        <button
          class="flex items-center gap-1 rounded-md border hairline-strong px-2.5 py-1 type-caption text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
          onclick={() => (confirmClear = true)}
        >
          <Trash2 size="11" /> clear all
        </button>
      {/if}
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
        aria-label="Search command history"
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
        aria-label="Filter history by host"
      >
        <option value="">any host</option>
        {#each app.hosts as h (h.id)}
          <option value={h.id}>{h.name}</option>
        {/each}
      </select>
      <select
        class="rounded-md border hairline bg-[var(--color-surface-3)] px-2 py-1 outline-none"
        bind:value={sourceFilter}
        aria-label="Filter history by source"
      >
        <option value="">any source</option>
        <option value="exec">multi-host exec</option>
        <option value="snippet">snippet apply</option>
      </select>
    </div>
  </div>

  <div class="flex-1 overflow-y-auto">
    {#if loading && entries.length === 0}
      <Skeleton rows={10} rowClass="h-10" />
    {:else if entries.length === 0}
      <EmptyState
        icon={HistoryIcon}
        title="No history yet"
        description="Commands appear here when you run via Multi-host, insert from the AI drawer, or apply a snippet."
      />
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
                  onclick={() => reviewForInsert(e)}
                  title="Review before inserting into active terminal"
                  aria-label="Review command before inserting into active terminal"
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

{#if reviewEntry}
  <Dialog
    label="Review command before insert"
    onclose={closeReview}
    portal
    panelClass="w-[min(42rem,calc(100vw-2rem))] rounded-lg border hairline-strong surface-1 shadow-2xl"
  >
    <div class="border-b hairline px-4 py-3">
      <div class="type-title text-[var(--color-text-1)]">REVIEW COMMAND</div>
      <div class="mt-1 type-caption text-[var(--color-text-3)]">
        Inspect or edit this command before it is inserted into the active terminal. It will not run automatically.
      </div>
    </div>
    <div class="space-y-3 p-4">
      <div class="flex flex-wrap items-center gap-2 type-micro text-[var(--color-text-3)]">
        <span>{reviewEntry.source}</span>
        {#if reviewEntry.hostName}<span>· target: {reviewEntry.hostName}</span>{/if}
        <span>· {fmtTime(reviewEntry.executedAt)}</span>
      </div>
      <textarea
        bind:value={reviewCommand}
        rows="8"
        aria-label="Command to insert"
        data-autofocus
        class="w-full resize-y rounded-md border hairline-strong bg-[var(--color-surface-3)] p-3 font-mono type-caption text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/60"
      ></textarea>
      <div class="flex justify-end gap-2">
        <button
          class="rounded-md border hairline-strong px-3 py-1.5 type-caption text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={closeReview}
        >
          cancel
        </button>
        <button
          class="rounded-md border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 px-3 py-1.5 type-caption text-[var(--color-accent)] hover:bg-[var(--color-accent)]/20 disabled:cursor-not-allowed disabled:opacity-50"
          onclick={insertReviewedCommand}
          disabled={!reviewCommand.trim()}
        >
          insert into terminal
        </button>
      </div>
    </div>
  </Dialog>
{/if}

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
