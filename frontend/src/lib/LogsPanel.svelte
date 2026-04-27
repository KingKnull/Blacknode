<script lang="ts">
  import { onDestroy, onMount, tick } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { LogsService } from "../../bindings/github.com/blacknode/blacknode";
  import type { LogLine } from "../../bindings/github.com/blacknode/blacknode/models";
  import type { LogQuery } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import {
    ScrollText,
    Play,
    Square,
    Pause,
    Trash2,
    Server,
    Bookmark,
    BookmarkPlus,
  } from "@lucide/svelte";

  // Color buckets so each host gets a stable, distinct accent. We pick from
  // the design palette's secondary colors so they sit beside emerald without
  // fighting it.
  const HOST_COLORS = [
    "#10d9a0", // accent
    "#3b82f6", // info
    "#f59e0b", // warn
    "#a855f7",
    "#06b6d4",
    "#ec4899",
    "#84cc16",
    "#fb923c",
  ];

  type Line = LogLine & { id: number };

  let command = $state("tail -F /var/log/syslog");
  let selectedHosts = $state<Set<string>>(new Set());
  let streamID = $state("");
  let running = $state(false);
  let paused = $state(false);
  let filter = $state("");
  let useRegex = $state(false);
  let buffer: Line[] = $state([]);
  let counter = 0;
  let scrollEl: HTMLDivElement | undefined = $state();
  let stickToBottom = $state(true);
  let off: (() => void) | undefined;

  // Saved-query state. Loaded once on mount and refreshed after Save/Delete.
  let savedQueries = $state<LogQuery[]>([]);
  let saveDialogOpen = $state(false);
  let saveName = $state("");
  let saveBusy = $state(false);

  onMount(() => {
    off = Events.On("logs:line", (e: any) => {
      const p: LogLine = e?.data;
      if (!p || p.streamID !== streamID) return;
      if (paused) return;
      counter++;
      buffer.push({ ...p, id: counter });
      // Cap the buffer so very chatty logs don't OOM the renderer.
      if (buffer.length > 5000) buffer.splice(0, buffer.length - 5000);
      if (stickToBottom) void scrollToBottom();
    });
    void refreshQueries();
  });

  async function refreshQueries() {
    savedQueries = ((await LogsService.ListQueries()) ?? []) as LogQuery[];
  }

  async function saveCurrent() {
    if (!saveName.trim() || !command.trim()) return;
    saveBusy = true;
    try {
      await LogsService.SaveQuery({
        name: saveName.trim(),
        command,
        hostIDs: [...selectedHosts],
        filter,
        useRegex,
      } as unknown as LogQuery);
      saveName = "";
      saveDialogOpen = false;
      await refreshQueries();
    } finally {
      saveBusy = false;
    }
  }

  async function loadQuery(q: LogQuery) {
    if (running) return; // never clobber an active stream
    command = q.command;
    selectedHosts = new Set(q.hostIDs);
    filter = q.filter ?? "";
    useRegex = q.useRegex ?? false;
  }

  async function deleteQuery(q: LogQuery) {
    if (!confirm(`Delete saved query "${q.name}"?`)) return;
    await LogsService.DeleteQuery(q.id);
    await refreshQueries();
  }

  onDestroy(() => {
    off?.();
    if (running && streamID) void LogsService.Stop(streamID);
  });

  async function scrollToBottom() {
    await tick();
    if (scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
  }

  function onScroll() {
    if (!scrollEl) return;
    const dist =
      scrollEl.scrollHeight - scrollEl.scrollTop - scrollEl.clientHeight;
    stickToBottom = dist < 60;
  }

  function toggleHost(id: string) {
    const next = new Set(selectedHosts);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    selectedHosts = next;
  }

  async function start() {
    if (!command || selectedHosts.size === 0) return;
    streamID = crypto.randomUUID();
    buffer = [];
    paused = false;
    const passwords: Record<string, string> = {};
    for (const id of selectedHosts) {
      const p = app.hostPasswords[id];
      if (p) passwords[id] = p;
    }
    await LogsService.Start(streamID, [...selectedHosts], passwords, command);
    running = true;
  }

  async function stop() {
    if (!streamID) return;
    await LogsService.Stop(streamID);
    running = false;
  }

  function clear() {
    buffer = [];
  }

  let hostColor = $derived((id: string) => {
    const idx = [...selectedHosts].indexOf(id);
    if (idx < 0) return HOST_COLORS[0];
    return HOST_COLORS[idx % HOST_COLORS.length];
  });

  let filtered = $derived(() => {
    if (!filter) return buffer;
    if (useRegex) {
      try {
        const re = new RegExp(filter, "i");
        return buffer.filter((l) => re.test(l.line));
      } catch {
        return buffer;
      }
    }
    const f = filter.toLowerCase();
    return buffer.filter((l) => l.line.toLowerCase().includes(f));
  });
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={ScrollText}
    title="Logs"
    subtitle="Multi-host live tail with regex filtering"
  >
    {#snippet actions()}
      {#if running}
        <button
          class="flex items-center gap-1 rounded-md border hairline-strong px-2.5 py-1 text-[11px] text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)]"
          onclick={() => (paused = !paused)}
        >
          <Pause size="11" />
          {paused ? "resume" : "pause"}
        </button>
        <button
          class="flex items-center gap-1 rounded-md bg-[var(--color-danger)] px-2.5 py-1 text-[11px] font-medium text-white hover:opacity-90"
          onclick={stop}
        >
          <Square size="11" /> stop
        </button>
      {:else}
        <button
          class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-2.5 py-1 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
          disabled={!command || selectedHosts.size === 0}
          onclick={start}
        >
          <Play size="11" /> start
        </button>
      {/if}
      <button
        class="flex items-center gap-1 rounded-md border hairline-strong px-2 py-1 text-[11px] text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
        title="Save current command + host set + filter as a named query"
        disabled={!command || selectedHosts.size === 0}
        onclick={() => (saveDialogOpen = true)}
      >
        <BookmarkPlus size="11" />
        save
      </button>
      <button
        class="flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={clear}
        title="Clear buffer"
      >
        <Trash2 size="11" />
      </button>
    {/snippet}
  </PageHeader>

  {#if savedQueries.length > 0}
    <div
      class="flex flex-wrap items-center gap-1.5 border-b hairline surface-1 px-4 py-2"
    >
      <span
        class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
      >
        <Bookmark size="10" class="mr-1 inline" /> saved
      </span>
      {#each savedQueries as q (q.id)}
        <div
          class="group flex items-center gap-0.5 rounded-md border hairline bg-[var(--color-surface-3)] text-[11px]"
        >
          <button
            class="px-2 py-0.5 hover:text-[var(--color-accent)] disabled:opacity-50"
            disabled={running}
            onclick={() => loadQuery(q)}
            title={q.command}
          >
            {q.name}
          </button>
          <button
            class="px-1 py-0.5 text-[var(--color-text-4)] opacity-0 hover:text-[var(--color-danger)] group-hover:opacity-100"
            onclick={() => deleteQuery(q)}
            title="Delete"
          >
            ×
          </button>
        </div>
      {/each}
    </div>
  {/if}

  <div class="border-b hairline surface-1 px-4 py-3 space-y-2">
    <div class="flex items-stretch gap-2">
      <input
        class="flex-1 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-sm outline-none disabled:opacity-50"
        bind:value={command}
        placeholder="tail -F /var/log/syslog"
        disabled={running}
      />
    </div>
    <div class="flex items-center gap-2">
      <input
        class="flex-1 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-1.5 font-mono text-xs outline-none"
        bind:value={filter}
        placeholder={useRegex ? "regex filter (case-insensitive)" : "substring filter"}
      />
      <label
        class="flex items-center gap-1.5 rounded-md border hairline-strong px-2 py-1 text-[11px] text-[var(--color-text-2)]"
      >
        <input
          type="checkbox"
          class="accent-[var(--color-accent)]"
          bind:checked={useRegex}
        />
        regex
      </label>
    </div>
  </div>

  <div class="grid h-full grid-cols-[240px_1fr] overflow-hidden">
    <div class="overflow-y-auto border-r hairline">
      <div class="px-3 py-2 text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-4)]">
        Hosts
      </div>
      {#each app.hosts as h (h.id)}
        {@const isSel = selectedHosts.has(h.id)}
        <label
          class="flex cursor-pointer items-center gap-2 border-b hairline px-3 py-1.5 text-xs transition-colors hover:bg-[var(--color-surface-2)]"
        >
          <input
            type="checkbox"
            class="accent-[var(--color-accent)]"
            checked={isSel}
            disabled={running}
            onchange={() => toggleHost(h.id)}
          />
          {#if isSel}
            <span
              class="h-2 w-2 rounded-full"
              style="background:{hostColor(h.id)}"
            ></span>
          {:else}
            <Server size="11" class="text-[var(--color-text-3)]" />
          {/if}
          <div class="min-w-0 flex-1">
            <div class="truncate text-[var(--color-text-1)]">{h.name}</div>
            <div class="truncate text-[10px] text-[var(--color-text-3)]">
              {h.username}@{h.host}
            </div>
          </div>
        </label>
      {/each}
      {#if app.hosts.length === 0}
        <div class="p-4 text-center text-[11px] text-[var(--color-text-3)]">
          No hosts.
        </div>
      {/if}
    </div>

    <div
      bind:this={scrollEl}
      onscroll={onScroll}
      class="overflow-y-auto bg-[var(--color-surface-0)] font-mono text-[12px] leading-[1.45]"
    >
      {#each filtered() as l (l.id)}
        <div
          class="grid grid-cols-[80px_1fr] gap-2 px-3 py-0.5 hover:bg-[var(--color-surface-2)] {l.isStderr
            ? 'text-[var(--color-danger)]'
            : 'text-[var(--color-text-1)]'}"
        >
          <span
            class="truncate text-[10px]"
            style="color:{hostColor(l.hostID)}"
            title={l.hostName}>{l.hostName}</span
          >
          <span class="break-all whitespace-pre-wrap">{l.line}</span>
        </div>
      {/each}
      {#if filtered().length === 0}
        <div class="flex h-full items-center justify-center">
          <div class="text-center">
            <ScrollText size="22" class="mx-auto text-[var(--color-text-4)]" />
            <p class="mt-2 text-xs text-[var(--color-text-3)]">
              {#if running}
                Listening… nothing matches the current filter.
              {:else}
                Pick hosts on the left and hit start.
              {/if}
            </p>
          </div>
        </div>
      {/if}
    </div>
  </div>

  {#if running}
    <div
      class="border-t hairline surface-1 px-4 py-1 text-[10px] text-[var(--color-text-3)]"
    >
      <span
        class="inline-block h-1.5 w-1.5 rounded-full bg-[var(--color-accent)] pulse-soft"
      ></span>
      <span class="ml-2">streaming · {buffer.length} lines · {paused ? "paused" : "live"}</span>
    </div>
  {/if}
</div>

{#if saveDialogOpen}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) saveDialogOpen = false;
    }}
  >
    <div
      class="w-[440px] overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/50"
    >
      <div class="flex items-center gap-2 border-b hairline px-5 py-3">
        <BookmarkPlus size="14" class="text-[var(--color-accent)]" />
        <h3 class="text-sm font-semibold">Save log query</h3>
      </div>
      <div class="space-y-3 p-5 text-sm">
        <p class="text-xs text-[var(--color-text-3)]">
          Saves the command, the current host selection ({selectedHosts.size}{" "}
          host{selectedHosts.size === 1 ? "" : "s"}), and the active filter.
          Recall it later from the bar above.
        </p>
        <label class="block">
          <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >Name</span
          >
          <input
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            bind:value={saveName}
            placeholder="e.g. nginx-errors-prod"
            onkeydown={(e) => e.key === "Enter" && saveCurrent()}
          />
        </label>
      </div>
      <div class="flex items-center justify-end gap-2 border-t hairline px-5 py-3">
        <button
          class="rounded-md px-3 py-1.5 text-xs text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={() => (saveDialogOpen = false)}>Cancel</button
        >
        <button
          class="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
          disabled={!saveName.trim() || saveBusy}
          onclick={saveCurrent}>{saveBusy ? "saving…" : "Save"}</button
        >
      </div>
    </div>
  </div>
{/if}
