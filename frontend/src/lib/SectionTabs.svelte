<script lang="ts">
  import type { View } from "./state.svelte";
  import { Plus, Server, TerminalSquare, SquareTerminal, Database, Globe2 } from "@lucide/svelte";

  type ViewDef = { id: View; label: string; Icon: any };

  type Props = {
    views: ViewDef[];
    activeView: View;
    onSelect: (id: View) => void;
    onNew: (what: "host" | "terminal" | "shell" | "database" | "http") => void;
  };
  let { views, activeView, onSelect, onNew }: Props = $props();

  let menuOpen = $state(false);

  const NEW_ITEMS: { id: "host" | "terminal" | "shell" | "database" | "http"; label: string; Icon: any }[] = [
    { id: "host", label: "New host", Icon: Server },
    { id: "terminal", label: "New terminal", Icon: TerminalSquare },
    { id: "shell", label: "New local shell", Icon: SquareTerminal },
    { id: "database", label: "New DB connection", Icon: Database },
    { id: "http", label: "New HTTP request", Icon: Globe2 },
  ];
</script>

<div class="flex h-9 shrink-0 items-center gap-1 border-b hairline surface-1 px-2">
  <div class="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto">
    {#each views as v (v.id)}
      {@const active = activeView === v.id}
      <button
        class="flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1 type-caption transition-colors {active
          ? 'bg-[var(--color-surface-3)] text-[var(--color-text-1)]'
          : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-1)]'}"
        onclick={() => onSelect(v.id)}
        aria-current={active ? "page" : undefined}
      >
        <v.Icon size="13" strokeWidth={active ? 2 : 1.6} />
        {v.label}
      </button>
    {/each}
  </div>

  <!-- + New -->
  <div class="relative shrink-0">
    <button
      class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-2.5 py-1 type-caption font-medium text-white transition-opacity hover:opacity-90"
      onclick={() => (menuOpen = !menuOpen)}
    >
      <Plus size="13" /> New
    </button>
    {#if menuOpen}
      <button class="fixed inset-0 z-40 cursor-default" aria-label="Close menu" onclick={() => (menuOpen = false)}></button>
      <div class="fade-up absolute right-0 top-full z-50 mt-1 min-w-[190px] overflow-hidden border hairline-strong surface-2 py-1 shadow-xl shadow-black/40" style="border-radius: var(--radius-md);">
        {#each NEW_ITEMS as item (item.id)}
          <button
            class="flex w-full items-center gap-2.5 px-3 py-1.5 text-left type-caption text-[var(--color-text-2)] transition-colors hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
            onclick={() => { menuOpen = false; onNew(item.id); }}
          >
            <item.Icon size="14" class="text-[var(--color-text-4)]" /> {item.label}
          </button>
        {/each}
      </div>
    {/if}
  </div>
</div>
