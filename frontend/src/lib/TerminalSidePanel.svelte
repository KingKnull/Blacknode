<script lang="ts">
  import { onMount } from "svelte";
  import { HistoryService, SnippetService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { HistoryEntry, Snippet } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { History, Bookmark, Palette as PaletteIcon, X, Search, ChevronRight } from "@lucide/svelte";

  type Props = {
    hostID: string | null;
    onInsert: (text: string) => void;
    onClose: () => void;
  };
  let { hostID, onInsert, onClose }: Props = $props();

  type Tab = "history" | "snippets" | "themes";
  let activeTab = $state<Tab>("history");

  // History
  let histEntries = $state<HistoryEntry[]>([]);
  let histFilter = $state("");
  let histLoading = $state(false);

  // Snippets
  let snippets = $state<Snippet[]>([]);
  let snippetFilter = $state("");
  let snippetLoading = $state(false);

  let visibleHistory = $derived(
    histFilter
      ? histEntries.filter((e) =>
          e.command.toLowerCase().includes(histFilter.toLowerCase())
        )
      : histEntries
  );

  let visibleSnippets = $derived(
    snippetFilter
      ? snippets.filter(
          (s) =>
            s.name.toLowerCase().includes(snippetFilter.toLowerCase()) ||
            s.body.toLowerCase().includes(snippetFilter.toLowerCase())
        )
      : snippets
  );

  async function loadHistory() {
    histLoading = true;
    try {
      histEntries = ((await HistoryService.List(hostID ?? "", "", 200)) ?? []) as HistoryEntry[];
    } finally {
      histLoading = false;
    }
  }

  async function loadSnippets() {
    snippetLoading = true;
    try {
      snippets = ((await SnippetService.List()) ?? []) as Snippet[];
    } finally {
      snippetLoading = false;
    }
  }

  function selectTab(t: Tab) {
    activeTab = t;
    if (t === "history" && histEntries.length === 0) loadHistory();
    if (t === "snippets" && snippets.length === 0) loadSnippets();
  }

  onMount(() => {
    loadHistory();
  });

  function fmtAge(ts: number): string {
    const secs = Math.floor((Date.now() - ts * 1000) / 1000);
    if (secs < 60) return "now";
    if (secs < 3600) return `${Math.floor(secs / 60)}m`;
    if (secs < 86400) return `${Math.floor(secs / 3600)}h`;
    return `${Math.floor(secs / 86400)}d`;
  }

  // Terminal themes: foreground/background/cursor combos
  const THEMES = [
    { id: "default-dark", label: "Blacknode Dark", bg: "#0b0e14", fg: "#e6e9ef", cur: "#3b82f6" },
    { id: "default-light", label: "Blacknode Light", bg: "#ffffff", fg: "#1f2733", cur: "#2563eb" },
    { id: "dracula", label: "Dracula", bg: "#282a36", fg: "#f8f8f2", cur: "#ff79c6" },
    { id: "nord", label: "Nord", bg: "#2e3440", fg: "#d8dee9", cur: "#88c0d0" },
    { id: "gruvbox", label: "Gruvbox Dark", bg: "#282828", fg: "#ebdbb2", cur: "#fabd2f" },
    { id: "solarized-dark", label: "Solarized Dark", bg: "#002b36", fg: "#839496", cur: "#268bd2" },
    { id: "monokai", label: "Monokai", bg: "#272822", fg: "#f8f8f2", cur: "#a6e22e" },
    { id: "tokyo-night", label: "Tokyo Night", bg: "#1a1b26", fg: "#c0caf5", cur: "#7aa2f7" },
    { id: "catppuccin", label: "Catppuccin Mocha", bg: "#1e1e2e", fg: "#cdd6f4", cur: "#89b4fa" },
    { id: "one-dark", label: "One Dark", bg: "#282c34", fg: "#abb2bf", cur: "#61afef" },
  ];

  let selectedTheme = $state("default-dark");

  function applyTheme(t: typeof THEMES[0]) {
    selectedTheme = t.id;
    // Emit a custom event the Terminal.svelte can listen to for theme swaps.
    window.dispatchEvent(new CustomEvent("terminal:theme-change", { detail: t }));
  }
</script>

<!-- Slide-in panel from the right edge of the terminal -->
<aside
  class="slide-in-right flex h-full w-[280px] shrink-0 flex-col border-l hairline surface-1 overflow-hidden"
>
  <!-- Header -->
  <div class="flex items-center gap-2 border-b hairline px-3 py-2">
    <span class="flex-1 type-eyebrow text-[var(--color-text-3)]">Terminal panel</span>
    <button
      class="flex h-6 w-6 items-center justify-center rounded text-[var(--color-text-4)] transition-colors hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-2)]"
      onclick={onClose}
      aria-label="Close panel"
    >
      <X size="13" />
    </button>
  </div>

  <!-- Tab strip -->
  <div class="flex border-b hairline">
    {#each [["history", History, "History"] as const, ["snippets", Bookmark, "Snippets"] as const, ["themes", PaletteIcon, "Themes"] as const] as [id, Icon, label]}
      <button
        class="flex flex-1 flex-col items-center gap-0.5 py-2 type-micro font-medium transition-colors {activeTab === id
          ? 'text-[var(--color-accent)] border-b-2 border-[var(--color-accent)] -mb-px'
          : 'text-[var(--color-text-4)] hover:text-[var(--color-text-2)]'}"
        onclick={() => selectTab(id)}
      >
        <Icon size="13" />
        {label}
      </button>
    {/each}
  </div>

  <!-- Tab content -->
  <div class="flex-1 overflow-hidden flex flex-col">

    {#if activeTab === "history"}
      <div class="px-2 py-2 border-b hairline">
        <div class="relative flex items-center rounded border hairline bg-[var(--color-surface-2)]">
          <Search size="11" class="absolute left-2 text-[var(--color-text-4)]" />
          <input
            class="w-full bg-transparent py-1.5 pl-7 pr-2 font-mono type-micro text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)]"
            placeholder="Filter history…"
            bind:value={histFilter}
          />
        </div>
      </div>
      <div class="flex-1 overflow-y-auto py-1">
        {#if histLoading}
          <div class="px-3 py-4 text-center type-micro text-[var(--color-text-4)]">Loading…</div>
        {:else if visibleHistory.length === 0}
          <div class="px-3 py-6 text-center">
            <History size="20" class="mx-auto mb-2 text-[var(--color-text-4)]" />
            <p class="type-micro text-[var(--color-text-4)]">{histFilter ? "No matches" : "No history yet"}</p>
          </div>
        {/if}
        {#each visibleHistory as e (e.id)}
          <button
            class="group flex w-full items-start gap-2 px-2 py-1.5 text-left transition-colors hover:bg-[var(--color-surface-2)]"
            onclick={() => onInsert(e.command)}
            title="Click to insert"
          >
            <div class="min-w-0 flex-1">
              <div class="truncate font-mono type-micro text-[var(--color-text-1)]">{e.command}</div>
              {#if e.hostName}
                <div class="truncate type-nano text-[var(--color-text-4)]">{e.hostName}</div>
              {/if}
            </div>
            <div class="flex shrink-0 flex-col items-end gap-1">
              <span class="type-nano text-[var(--color-text-4)]">{fmtAge(e.executedAt)}</span>
              <ChevronRight size="10" class="opacity-0 group-hover:opacity-100 text-[var(--color-accent)] transition-opacity" />
            </div>
          </button>
        {/each}
      </div>
    {/if}

    {#if activeTab === "snippets"}
      <div class="px-2 py-2 border-b hairline">
        <div class="relative flex items-center rounded border hairline bg-[var(--color-surface-2)]">
          <Search size="11" class="absolute left-2 text-[var(--color-text-4)]" />
          <input
            class="w-full bg-transparent py-1.5 pl-7 pr-2 font-mono type-micro text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)]"
            placeholder="Filter snippets…"
            bind:value={snippetFilter}
          />
        </div>
      </div>
      <div class="flex-1 overflow-y-auto py-1">
        {#if snippetLoading}
          <div class="px-3 py-4 text-center type-micro text-[var(--color-text-4)]">Loading…</div>
        {:else if visibleSnippets.length === 0}
          <div class="px-3 py-6 text-center">
            <Bookmark size="20" class="mx-auto mb-2 text-[var(--color-text-4)]" />
            <p class="type-micro text-[var(--color-text-4)]">{snippetFilter ? "No matches" : "No snippets yet"}</p>
          </div>
        {/if}
        {#each visibleSnippets as s (s.id)}
          <button
            class="group flex w-full flex-col gap-0.5 px-3 py-2 text-left transition-colors hover:bg-[var(--color-surface-2)]"
            onclick={() => onInsert(s.body)}
            title="Click to insert snippet body"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="truncate type-caption font-medium text-[var(--color-text-1)]">{s.name}</span>
              <ChevronRight size="10" class="shrink-0 opacity-0 group-hover:opacity-100 text-[var(--color-accent)] transition-opacity" />
            </div>
            <span class="truncate font-mono type-nano text-[var(--color-text-3)]">{s.body}</span>
          </button>
        {/each}
      </div>
    {/if}

    {#if activeTab === "themes"}
      <div class="flex-1 overflow-y-auto py-1">
        {#each THEMES as t (t.id)}
          <button
            class="flex w-full items-center gap-3 px-3 py-2 text-left transition-colors hover:bg-[var(--color-surface-2)] {selectedTheme === t.id ? 'bg-[var(--color-accent)]/8' : ''}"
            onclick={() => applyTheme(t)}
          >
            <!-- Color preview swatches -->
            <div class="flex shrink-0 gap-px rounded overflow-hidden border hairline">
              <div class="h-5 w-5 rounded-sm" style="background:{t.bg}"></div>
              <div class="h-5 w-3" style="background:{t.fg}; opacity:0.8"></div>
              <div class="h-5 w-2" style="background:{t.cur}"></div>
            </div>
            <span class="flex-1 truncate type-caption text-[var(--color-text-1)]">{t.label}</span>
            {#if selectedTheme === t.id}
              <span class="shrink-0 rounded px-1 py-0.5 type-nano font-semibold text-[var(--color-accent)] bg-[var(--color-accent-soft)]">ACTIVE</span>
            {/if}
          </button>
        {/each}
      </div>
    {/if}

  </div>
</aside>
