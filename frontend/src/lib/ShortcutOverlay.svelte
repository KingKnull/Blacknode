<script lang="ts">
  import { X } from "@lucide/svelte";

  type Props = { onclose: () => void };
  let { onclose }: Props = $props();

  // Single source of truth for all keyboard shortcuts.
  // Add new shortcuts here — the overlay renders from this list automatically.
  const GROUPS: { label: string; shortcuts: { keys: string[]; desc: string }[] }[] = [
    {
      label: "Navigation",
      shortcuts: [
        { keys: ["Ctrl", "1–9"],  desc: "Switch to tab N" },
        { keys: ["Ctrl", "Tab"],  desc: "Next tab" },
        { keys: ["Ctrl", "W"],    desc: "Close tab" },
        { keys: ["Ctrl", "T"],    desc: "New tab" },
        { keys: ["?"],            desc: "Open this overlay" },
      ],
    },
    {
      label: "Terminal",
      shortcuts: [
        { keys: ["Ctrl", "Shift", "F"], desc: "Search terminal output" },
        { keys: ["Ctrl", "Shift", "S"], desc: "Auto-fill sudo password" },
        { keys: ["Ctrl", "Shift", "I"], desc: "Toggle AI assistant" },
      ],
    },
    {
      label: "Hosts",
      shortcuts: [
        { keys: ["Ctrl", "Shift", "H"], desc: "Toggle host sidebar" },
      ],
    },
  ];

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === "Escape" || e.key === "?") {
      e.preventDefault();
      onclose();
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<!-- Backdrop -->
<div
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-[3px]"
  role="dialog"
  aria-modal="true"
  aria-label="Keyboard shortcuts"
  onclick={(e) => { if (e.target === e.currentTarget) onclose(); }}
>
  <div class="fade-up w-[480px] max-h-[80vh] overflow-y-auto border hairline-strong surface-2 shadow-2xl shadow-black/60"
    style="box-shadow: 0 0 40px rgba(0,255,136,0.04), 0 20px 60px rgba(0,0,0,0.6);">

    <!-- Header -->
    <div class="flex items-center justify-between border-b hairline px-5 py-3">
      <div class="flex items-center gap-2">
        <span class="font-mono text-[10px] font-bold uppercase tracking-[0.18em] text-[var(--color-text-3)]">Keyboard Shortcuts</span>
        <span class="border hairline px-1.5 py-px font-mono text-[9px] text-[var(--color-text-4)]">?</span>
      </div>
      <button
        class="flex h-6 w-6 items-center justify-center text-[var(--color-text-4)] hover:text-[var(--color-text-1)] transition-colors"
        onclick={onclose}
        title="Close"
      >
        <X size="13" />
      </button>
    </div>

    <!-- Shortcut groups -->
    <div class="divide-y divide-[var(--color-line)]">
      {#each GROUPS as group}
        <div class="px-5 py-4">
          <div class="mb-3 font-mono text-[9px] font-bold uppercase tracking-[0.18em] text-[var(--color-text-4)]">
            {group.label}
          </div>
          <div class="space-y-2">
            {#each group.shortcuts as s}
              <div class="flex items-center justify-between gap-4">
                <span class="text-[11px] text-[var(--color-text-2)]">{s.desc}</span>
                <div class="flex shrink-0 items-center gap-1">
                  {#each s.keys as key, i}
                    {#if i > 0}
                      <span class="text-[9px] text-[var(--color-text-4)]">+</span>
                    {/if}
                    <kbd class="border hairline bg-[var(--color-surface-3)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--color-text-2)]">{key}</kbd>
                  {/each}
                </div>
              </div>
            {/each}
          </div>
        </div>
      {/each}
    </div>

    <!-- Footer -->
    <div class="border-t hairline px-5 py-2.5">
      <p class="font-mono text-[9px] text-[var(--color-text-4)]">Press <kbd class="border hairline bg-[var(--color-surface-3)] px-1 py-px font-mono text-[9px]">Esc</kbd> or <kbd class="border hairline bg-[var(--color-surface-3)] px-1 py-px font-mono text-[9px]">?</kbd> to close</p>
    </div>
  </div>
</div>
