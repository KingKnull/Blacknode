<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { AutocompleteService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Suggestion } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";

  type Props = {
    prefix: string;
    hostID: string;
    x: number;
    y: number;
    onAccept: (text: string) => void;
    onDismiss: () => void;
  };

  let { prefix, hostID, x, y, onAccept, onDismiss }: Props = $props();

  let suggestions = $state<Suggestion[]>([]);
  let activeIndex = $state(0);
  let loading = $state(false);
  let el: HTMLDivElement | undefined = $state();

  // Strip ANSI escape sequences before matching.
  function stripAnsi(s: string): string {
    return s.replace(/\x1b\[[0-9;]*[A-Za-z]/g, "").replace(/\x1b\][^\x07]*\x07/g, "");
  }

  const SOURCE_LABELS: Record<string, string> = {
    history: "HIST",
    snippet: "SNIP",
    builtin: "CMD",
    ai: "AI",
  };

  const SOURCE_COLORS: Record<string, string> = {
    history: "var(--color-accent)",
    snippet: "var(--color-accent-3)",
    builtin: "var(--color-text-4)",
    ai: "var(--color-warn)",
  };

  // Re-fetch whenever prefix changes.
  $effect(() => {
    const clean = stripAnsi(prefix).trim();
    if (clean.length < 2) {
      suggestions = [];
      return;
    }
    loading = true;
    AutocompleteService.Suggest(clean, hostID, 8)
      .then((res) => {
        suggestions = (res ?? []) as Suggestion[];
        activeIndex = 0;
      })
      .catch(() => {
        suggestions = [];
      })
      .finally(() => {
        loading = false;
      });
  });

  function accept(s: Suggestion) {
    onAccept(s.text);
  }

  function onKeyDown(e: KeyboardEvent) {
    if (!suggestions.length) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      e.stopPropagation();
      activeIndex = (activeIndex + 1) % suggestions.length;
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      e.stopPropagation();
      activeIndex = (activeIndex - 1 + suggestions.length) % suggestions.length;
    } else if (e.key === "Enter" || e.key === "Tab") {
      e.preventDefault();
      e.stopPropagation();
      if (suggestions[activeIndex]) accept(suggestions[activeIndex]);
    } else if (e.key === "Escape") {
      e.preventDefault();
      onDismiss();
    }
  }

  onMount(() => {
    window.addEventListener("keydown", onKeyDown, true);
  });
  onDestroy(() => {
    window.removeEventListener("keydown", onKeyDown, true);
  });
</script>

<!-- Popup floats above the terminal, positioned near the cursor -->
{#if suggestions.length > 0 || loading}
  <div
    bind:this={el}
    class="fade-up absolute z-50 min-w-[340px] max-w-[480px] overflow-hidden border hairline-strong surface-2 shadow-2xl"
    style="left: {x}px; bottom: {y}px; backdrop-filter: blur(12px) saturate(1.2); box-shadow: 0 4px 24px rgba(0,0,0,0.6);"
    role="listbox"
    aria-label="Autocomplete suggestions"
  >
    {#if loading && suggestions.length === 0}
      <div class="px-3 py-2 font-mono type-micro text-[var(--color-text-4)]">
        Searching…
      </div>
    {/if}
    {#each suggestions as s, i (s.text + s.source)}
      <button
        class="flex w-full items-center gap-2.5 px-3 py-2 text-left transition-colors {i === activeIndex ? 'bg-[var(--color-accent)]/10 border-l-2 border-[var(--color-accent)]' : 'border-l-2 border-transparent hover:bg-[var(--color-surface-3)]'}"
        role="option"
        aria-selected={i === activeIndex}
        onclick={() => accept(s)}
        onmouseenter={() => (activeIndex = i)}
      >
        <!-- Source badge -->
        <span
          class="shrink-0 rounded px-1 font-mono type-nano font-bold"
          style="color: {SOURCE_COLORS[s.source] ?? 'var(--color-text-4)'}; background: color-mix(in srgb, {SOURCE_COLORS[s.source] ?? 'var(--color-text-4)'} 12%, transparent);"
        >
          {SOURCE_LABELS[s.source] ?? s.source.toUpperCase()}
        </span>
        <!-- Command text -->
        <span class="flex-1 truncate font-mono type-caption text-[var(--color-text-1)]">
          {s.text}
        </span>
        <!-- Description (snippet name / host name) -->
        {#if s.description}
          <span class="shrink-0 truncate max-w-[120px] type-micro text-[var(--color-text-4)]">
            {s.description}
          </span>
        {/if}
      </button>
    {/each}

    <!-- Footer hint -->
    <div class="border-t hairline flex items-center gap-3 px-3 py-1.5 font-mono type-nano text-[var(--color-text-4)]">
      <span><kbd class="rounded border hairline px-1">↑↓</kbd> navigate</span>
      <span><kbd class="rounded border hairline px-1">↵</kbd> or <kbd class="rounded border hairline px-1">Tab</kbd> accept</span>
      <span><kbd class="rounded border hairline px-1">Esc</kbd> dismiss</span>
    </div>
  </div>
{/if}
