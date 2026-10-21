<script lang="ts">
  import { onMount } from "svelte";
  import { HistoryService, SnippetService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { HistoryEntry, Snippet } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { Clipboard as WailsClipboard } from "@wailsio/runtime";
  import { commandDuration, type CommandBlock } from "./commandBlocks";
  import {
    commandExportFilename, formatCommandExport, isSafeCommandExportFilename, selectedCommandBlock,
    type CommandExportContent, type CommandExportSelection,
  } from "./commandBlockExport";
  import { downloadText } from "./operationRecords";
  import Dialog from "./Dialog.svelte";
  import { History, Bookmark, Palette as PaletteIcon, SquareTerminal, X, Search, ChevronRight, AlertTriangle } from "@lucide/svelte";

  type Props = {
    commandBlocks: CommandBlock[];
    bookmarks: CommandBlock[];
    onToggleBookmark: (block: CommandBlock) => void;
    hostID: string | null;
    onInsert: (text: string) => void;
    onClose: () => void;
  };
  let { commandBlocks, bookmarks, onToggleBookmark, hostID, onInsert, onClose }: Props = $props();

  type Tab = "blocks" | "bookmarks" | "history" | "snippets" | "themes";
  let activeTab = $state<Tab>("blocks");
  let exportSelection = $state<CommandExportSelection | null>(null);
  let exportContent = $state<CommandExportContent>("block");
  let exportFilename = $state("");
  let exportAcknowledged = $state(false);
  let exportError = $state("");
  const exportID = $props.id();
  let exportBlock = $derived(selectedCommandBlock(commandBlocks, bookmarks, exportSelection));
  let exportFilenameValid = $derived(isSafeCommandExportFilename(exportFilename));
  let bookmarkedIDs = $derived(new Set(bookmarks.map((block) => block.id)));

  function prepareExport(block: CommandBlock, source: CommandExportSelection["source"]) {
    if (block.running) return;
    exportSelection = { source, id: block.id };
    exportContent = source === "bookmarks" ? "output" : "block";
    exportFilename = commandExportFilename(block);
    exportAcknowledged = false;
    exportError = "";
  }

  function exportSelected() {
    if (!exportBlock || !exportAcknowledged || !exportFilenameValid) return;
    try {
      downloadText(exportFilename, formatCommandExport(exportBlock, exportContent), "text/plain;charset=utf-8");
      exportSelection = null;
    } catch {
      exportError = "Could not start the text download. Try again.";
    }
  }

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

  async function copyBlockOutput(block: CommandBlock) {
    try {
      await WailsClipboard.SetText(block.output);
    } catch {
      // The panel is still useful for rerun/history without copy.
    }
  }

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
    {#each [["blocks", SquareTerminal, "Blocks"] as const, ["bookmarks", Bookmark, "Bookmarks"] as const, ["history", History, "History"] as const, ["snippets", Bookmark, "Snippets"] as const, ["themes", PaletteIcon, "Themes"] as const] as [id, Icon, label]}
      <button
        class="flex flex-1 flex-col items-center gap-0.5 py-2 type-micro font-medium transition-colors {activeTab === id
          ? 'text-[var(--color-accent)] border-b-2 border-[var(--color-accent)] -mb-px'
          : 'text-[var(--color-text-4)] hover:text-[var(--color-text-2)]'}"
        onclick={() => selectTab(id)}
        aria-pressed={activeTab === id}
      >
        <Icon size="13" />
        {label}
      </button>
    {/each}
  </div>

  <!-- Tab content -->
  <div class="flex-1 overflow-hidden flex flex-col">

    {#if activeTab === "blocks" || activeTab === "bookmarks"}
      <div class="border-b hairline px-3 py-2 type-micro text-[var(--color-text-3)]">
        {#if activeTab === "bookmarks"}
          <p class="font-medium text-[var(--color-text-1)]">Temporary bookmarks · live pane only</p>
          <p class="mt-1">Kept in memory while this pane is open, including when this panel is closed. Lost when the pane closes or the app reloads. Never saved to localStorage.</p>
        {:else}
          <p>Bookmark completed blocks to keep their captured output in this live pane.</p>
        {/if}
        <p class="mt-1">Captured output may be truncated (last 64 KiB). Without shell integration, a block completes when the next command starts.</p>
      </div>
      <div class="flex-1 overflow-y-auto py-1">
        {#each [...(activeTab === "bookmarks" ? bookmarks : commandBlocks)].reverse() as block (block.id)}
          <details class="border-b hairline px-2 py-2">
            <summary class="cursor-pointer">
              <span class="block truncate font-mono type-micro text-[var(--color-text-1)]">{block.command || "(command not captured)"}</span>
              <span class="mt-0.5 flex items-center gap-2 type-nano text-[var(--color-text-4)]">
                {commandDuration(block)}
                {#if block.running}
                  <span class="text-[var(--color-accent)]">running</span>
                {:else if block.exitCode !== undefined}
                  <span class={block.exitCode === 0 ? "text-[var(--color-success)]" : "text-[var(--color-danger)]"}>exit {block.exitCode}</span>
                {/if}
                {#if bookmarkedIDs.has(block.id)}
                  <span class="text-[var(--color-accent)]">bookmarked</span>
                {/if}
              </span>
            </summary>
            <div class="mt-2 flex flex-wrap gap-1">
              <button class="rounded border hairline px-1.5 py-0.5 type-nano hover:text-[var(--color-accent)]" onclick={() => onInsert(block.command)}>Rerun</button>
              <button class="rounded border hairline px-1.5 py-0.5 type-nano hover:text-[var(--color-accent)]" onclick={() => copyBlockOutput(block)}>Copy output</button>
              <button
                class="rounded border hairline px-1.5 py-0.5 type-nano hover:text-[var(--color-accent)] disabled:opacity-40"
                disabled={block.running}
                aria-pressed={bookmarkedIDs.has(block.id)}
                title={block.running ? "Wait for this command block to complete" : "Keep output only in this live pane"}
                onclick={() => onToggleBookmark(block)}
              >{bookmarkedIDs.has(block.id) ? "Unbookmark" : "Bookmark"}</button>
              <button
                class="rounded border hairline px-1.5 py-0.5 type-nano hover:text-[var(--color-accent)] disabled:opacity-40"
                disabled={block.running}
                title={block.running ? "Wait for this command block to complete" : "Review sensitive output before saving a text file"}
                onclick={() => prepareExport(block, activeTab === "bookmarks" ? "bookmarks" : "blocks")}
              >{activeTab === "bookmarks" ? "Export output…" : "Export block…"}</button>
            </div>
            <pre class="mt-2 max-h-40 overflow-auto rounded surface-3 p-2 font-mono type-nano text-[var(--color-text-3)]">{block.output || "(no output captured)"}</pre>
          </details>
        {:else}
          <div class="px-3 py-6 text-center">
            <SquareTerminal size="20" class="mx-auto mb-2 text-[var(--color-text-4)]" />
            <p class="type-micro text-[var(--color-text-4)]">{activeTab === "bookmarks" ? "No bookmarks yet. Open a completed block in Blocks and choose Bookmark." : "Run a command to build a block."}</p>
          </div>
        {/each}
      </div>
    {/if}

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

{#if exportBlock}
  <Dialog
    portal
    label="Review terminal export"
    onclose={() => { exportSelection = null; }}
    panelClass="w-[520px] overflow-y-auto rounded border hairline surface-2 p-4 shadow-2xl"
  >
    {#snippet children()}
      <h2 class="type-title font-semibold text-[var(--color-text-1)]">Export terminal text</h2>
      <div class="mt-3 rounded border border-[var(--color-warn)] bg-[var(--color-warn)]/10 p-3 type-caption text-[var(--color-text-1)]" role="note">
        <div class="flex items-center gap-2 font-semibold text-[var(--color-warn)]"><AlertTriangle size="16" />Credentials may be included</div>
        <p class="mt-1">Commands and terminal output can contain passwords, tokens, private keys and other secrets. This text is not redacted. Review it before saving or sharing.</p>
        <p class="mt-1">This saves a local text file only. It does not send data over the network or execute commands.</p>
      </div>
      <div class="ops-form mt-3">
        <label for="{exportID}-content">Include
          <select id="{exportID}-content" bind:value={exportContent}>
            <option value="block">Command block (command, metadata and output)</option>
            <option value="output">Captured output only</option>
          </select>
        </label>
        <label for="{exportID}-filename">Text filename
          <input id="{exportID}-filename" bind:value={exportFilename} aria-invalid={!exportFilenameValid} aria-describedby="{exportID}-filename-help" autocomplete="off" spellcheck="false" />
        </label>
        <p id="{exportID}-filename-help" class={exportFilenameValid ? "ops-muted" : "ops-error"}>Use a simple .txt filename (letters, numbers, hyphens, underscores or dots; max 120 characters). No paths or reserved device names.</p>
        <div>
          <p class="ops-muted mb-1">Review the exact file contents · captured output may be truncated</p>
          <pre class="ops-code max-h-56 select-text">{formatCommandExport(exportBlock, exportContent)}</pre>
        </div>
        <label class="!flex-row items-start gap-2">
          <input type="checkbox" bind:checked={exportAcknowledged} />
          <span>I reviewed this text for credentials and want to save it.</span>
        </label>
        {#if exportError}<p class="ops-error" role="alert">{exportError}</p>{/if}
        <div class="ops-actions justify-end">
          <button data-autofocus onclick={() => { exportSelection = null; }}>Cancel</button>
          <button class="primary" disabled={!exportAcknowledged || !exportFilenameValid} onclick={exportSelected}>Save .txt</button>
        </div>
      </div>
    {/snippet}
  </Dialog>
{/if}
