<script lang="ts">
  import { onMount } from "svelte";
  import { RecordingService } from "../../bindings/github.com/blacknode/blacknode";
  import type { Recording } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import type { SearchHit } from "../../bindings/github.com/blacknode/blacknode/models";
  import PageHeader from "./PageHeader.svelte";
  import RecordingPlayer from "./RecordingPlayer.svelte";
  import {
    Film,
    Play,
    Trash2,
    Search,
    Server,
    TerminalSquare,
    Loader2,
    Circle,
  } from "@lucide/svelte";

  let list = $state<Recording[]>([]);
  let hits = $state<SearchHit[]>([]);
  let query = $state("");
  let searching = $state(false);
  let loading = $state(true);
  let recordingEnabled = $state(false);
  let toggleBusy = $state(false);

  let playing = $state<{ id: string; offset: number } | null>(null);

  onMount(async () => {
    loading = true;
    try {
      list = ((await RecordingService.List()) ?? []) as Recording[];
      recordingEnabled = (await RecordingService.IsEnabled()) ?? false;
    } finally {
      loading = false;
    }
  });

  async function refresh() {
    list = ((await RecordingService.List()) ?? []) as Recording[];
  }

  async function setEnabled(on: boolean) {
    toggleBusy = true;
    try {
      await RecordingService.SetEnabled(on);
      recordingEnabled = on;
    } finally {
      toggleBusy = false;
    }
  }

  async function del(r: Recording) {
    if (!confirm(`Delete recording "${r.title || r.id}"?`)) return;
    await RecordingService.Delete(r.id);
    await refresh();
    hits = hits.filter((h) => h.recording.id !== r.id);
  }

  async function runSearch() {
    if (!query.trim()) {
      hits = [];
      return;
    }
    searching = true;
    try {
      hits = ((await RecordingService.Search(query)) ?? []) as SearchHit[];
    } finally {
      searching = false;
    }
  }

  function fmtDuration(seconds: number) {
    const m = Math.floor(seconds / 60);
    const s = seconds % 60;
    return `${m}:${s.toString().padStart(2, "0")}`;
  }

  function fmtSize(n: number) {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    return `${(n / 1024 / 1024).toFixed(2)} MB`;
  }

  function fmtTime(unix: number) {
    return new Date(unix * 1000).toLocaleString();
  }

  function snippet(s: string) {
    // Strip ANSI for cleaner search-result preview
    return s
      .replace(/\x1b\[[0-9;]*[a-zA-Z]/g, "")
      .replace(/\r/g, "")
      .trim()
      .slice(0, 200);
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Film}
    title="Recordings"
    subtitle="Asciinema-format session captures — for audit, replay, and search"
  >
    {#snippet actions()}
      <button
        class="flex items-center gap-1.5 rounded-md border hairline-strong px-2.5 py-1 text-[11px] {recordingEnabled
          ? 'border-[var(--color-accent)]/40 bg-[var(--color-accent)]/10 text-[var(--color-accent)]'
          : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]'} disabled:opacity-50"
        disabled={toggleBusy}
        onclick={() => setEnabled(!recordingEnabled)}
        title="Toggle automatic recording for new sessions"
      >
        {#if toggleBusy}
          <Loader2 size="11" class="animate-spin" />
        {:else}
          <Circle
            size="9"
            class={recordingEnabled
              ? "fill-[var(--color-accent)] text-[var(--color-accent)]"
              : "text-[var(--color-text-4)]"}
          />
        {/if}
        recording {recordingEnabled ? "on" : "off"}
      </button>
    {/snippet}
  </PageHeader>

  <div class="border-b hairline surface-1 px-4 py-3">
    <div
      class="relative flex items-center rounded-md border hairline bg-[var(--color-surface-3)] focus-within:border-[var(--color-accent)]/40"
    >
      <Search size="13" class="absolute left-3 text-[var(--color-text-4)]" />
      <input
        bind:value={query}
        onkeydown={(e) => e.key === "Enter" && runSearch()}
        placeholder="Search recorded output across every session…"
        class="w-full bg-transparent py-2 pl-9 pr-3 text-sm outline-none placeholder:text-[var(--color-text-4)]"
      />
      {#if searching}
        <Loader2 size="12" class="mr-3 animate-spin text-[var(--color-text-3)]" />
      {/if}
      {#if query}
        <button
          class="mr-2 rounded px-2 py-0.5 text-[10px] text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={() => {
            query = "";
            hits = [];
          }}>clear</button
        >
      {/if}
    </div>
  </div>

  <div class="flex-1 overflow-y-auto">
    {#if loading}
      <div
        class="flex h-32 items-center justify-center text-xs text-[var(--color-text-3)]"
      >
        <Loader2 size="14" class="animate-spin" /> &nbsp;loading…
      </div>
    {:else if hits.length > 0}
      {#each hits as hit (hit.recording.id)}
        <div class="border-b hairline">
          <div class="flex items-center gap-2 surface-1 px-4 py-2 text-xs">
            <Film size="12" class="text-[var(--color-accent)]" />
            <span class="font-medium">{hit.recording.title || hit.recording.id}</span>
            <span class="font-mono text-[10px] text-[var(--color-text-3)]"
              >{fmtTime(hit.recording.startedAt)}</span
            >
            <span
              class="ml-auto rounded-sm border hairline px-1.5 py-0.5 text-[10px] text-[var(--color-text-3)]"
              >{hit.matches.length} match{hit.matches.length === 1 ? "" : "es"}</span
            >
          </div>
          {#each hit.matches as m, i (i)}
            <button
              class="grid w-full grid-cols-[60px_1fr] items-start gap-2 border-b border-[var(--color-line)] px-4 py-1.5 text-left hover:bg-[var(--color-surface-2)]"
              onclick={() =>
                (playing = { id: hit.recording.id, offset: m.offset })}
            >
              <span
                class="font-mono text-[10px] text-[var(--color-accent)]"
                >+{m.offset.toFixed(1)}s</span
              >
              <pre
                class="overflow-hidden whitespace-pre-wrap break-all font-mono text-[11px] text-[var(--color-text-2)]">{snippet(m.snippet)}</pre>
            </button>
          {/each}
        </div>
      {/each}
    {:else if list.length > 0}
      <div class="divide-y divide-[var(--color-line)]">
        {#each list as r (r.id)}
          <div
            class="grid grid-cols-[1fr_140px_120px_100px_80px] items-center gap-3 px-4 py-2.5 text-xs transition-colors hover:bg-[var(--color-surface-2)]"
          >
            <div class="flex min-w-0 items-center gap-2">
              {#if r.isLocal}
                <TerminalSquare size="12" class="text-[var(--color-text-3)]" />
              {:else}
                <Server size="12" class="text-[var(--color-text-3)]" />
              {/if}
              <span class="truncate text-[var(--color-text-1)]"
                >{r.title || r.id}</span
              >
            </div>
            <span class="font-mono text-[10px] text-[var(--color-text-3)]"
              >{fmtTime(r.startedAt)}</span
            >
            <span class="font-mono text-[10px] text-[var(--color-text-3)]"
              >{fmtDuration(r.durationSeconds)}</span
            >
            <span class="font-mono text-[10px] text-[var(--color-text-3)]"
              >{fmtSize(r.sizeBytes)}</span
            >
            <div class="flex justify-end gap-0.5">
              <button
                class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-2 py-1 text-[10px] font-medium text-[var(--color-surface-0)] hover:opacity-90"
                onclick={() => (playing = { id: r.id, offset: 0 })}
              >
                <Play size="10" /> play
              </button>
              <button
                class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
                onclick={() => del(r)}
              >
                <Trash2 size="11" />
              </button>
            </div>
          </div>
        {/each}
      </div>
    {:else}
      <div class="flex h-full items-center justify-center">
        <div class="max-w-md text-center">
          <Film size="22" class="mx-auto text-[var(--color-text-4)]" />
          <p class="mt-2 text-xs text-[var(--color-text-3)]">
            {#if recordingEnabled}
              No recordings yet. Open a terminal and they'll appear here when
              you close it.
            {:else}
              Recording is off. Toggle it on to capture every new terminal
              session in asciinema cast format. Existing sessions aren't
              affected.
            {/if}
          </p>
          <p class="mt-2 text-[10px] text-[var(--color-text-4)]">
            Output is captured; keystrokes are not — passwords typed at sudo
            prompts never hit disk.
          </p>
        </div>
      </div>
    {/if}
  </div>

  {#if playing}
    <RecordingPlayer
      recordingID={playing.id}
      seekToOffset={playing.offset}
      onClose={() => (playing = null)}
    />
  {/if}
</div>

