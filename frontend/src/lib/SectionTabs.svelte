<script lang="ts">
  import { tick } from "svelte";
  import { DropdownMenu } from "bits-ui";
  import { app, type View } from "./state.svelte";
  import { Plus, Server, TerminalSquare, SquareTerminal, Database, Globe2 } from "@lucide/svelte";

  type ViewDef = { id: View; label: string; Icon: any };

  type Props = {
    views: ViewDef[];
    activeView: View;
    onSelect: (id: View) => void;
    onNew: (what: "host" | "terminal" | "shell" | "database" | "http") => void;
  };
  let { views, activeView, onSelect, onNew }: Props = $props();

  let viewList: HTMLDivElement;

  // Keep the selected tool visible when navigation changes through the rail
  // or command palette, including in narrow windows.
  $effect(() => {
    activeView; views;
    void tick().then(() => {
      const selected = viewList?.querySelector<HTMLElement>('[aria-current="page"]');
      if (!selected) return;
      const listRect = viewList.getBoundingClientRect();
      const rect = selected.getBoundingClientRect();
      if (rect.left < listRect.left) viewList.scrollLeft -= listRect.left - rect.left;
      else if (rect.right > listRect.right) viewList.scrollLeft += rect.right - listRect.right;
    });
  });

  const NEW_ITEMS: { id: "host" | "terminal" | "shell" | "database" | "http"; label: string; Icon: any }[] = [
    { id: "host", label: "New host", Icon: Server },
    { id: "terminal", label: "New terminal", Icon: TerminalSquare },
    { id: "shell", label: "New local shell", Icon: SquareTerminal },
    { id: "database", label: "New DB connection", Icon: Database },
    { id: "http", label: "New HTTP request", Icon: Globe2 },
  ];
</script>

<div class="flex h-10 shrink-0 items-center gap-1 border-b hairline surface-1 px-3">
  <div bind:this={viewList} class="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto">
    {#each views as v (v.id)}
      {@const active = activeView === v.id}
      <button
        class="relative flex shrink-0 items-center gap-1.5 rounded-md px-2.5 py-1.5 type-caption transition-colors {active
          ? 'bg-[var(--color-surface-3)] text-[var(--color-text-1)]'
          : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-1)]'}"
        onclick={() => onSelect(v.id)}
        aria-current={active ? "page" : undefined}
      >
        <v.Icon size="13" strokeWidth={active ? 2 : 1.6} />
        {v.label}
        {#if active}<span class="absolute inset-x-2 bottom-0 h-0.5 rounded-full bg-[var(--color-accent)]"></span>{/if}
      </button>
    {/each}
  </div>

  <!-- + New -->
  <div class="relative shrink-0">
    <DropdownMenu.Root>
      <DropdownMenu.Trigger
        class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-2.5 py-1.5 type-caption font-semibold text-[var(--color-surface-0)] transition-opacity hover:opacity-90"
      >
        <Plus size="13" /> New
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content align="end" sideOffset={4} aria-label="Create new" class="fade-up z-50 min-w-[190px] overflow-hidden border hairline-strong surface-2 py-1 shadow-xl shadow-black/40" style="border-radius: var(--radius-md);">
          {#each NEW_ITEMS.filter((item) => app.isViewVisible(item.id)) as item (item.id)}
            <DropdownMenu.Item
              textValue={item.label}
              class="flex min-h-8 w-full cursor-pointer items-center gap-2.5 px-3 py-2 text-left type-caption text-[var(--color-text-2)] transition-colors hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)] focus-visible:bg-[var(--color-surface-3)] data-highlighted:bg-[var(--color-surface-3)] data-highlighted:text-[var(--color-text-1)]"
              onSelect={async () => {
                // Finish menu focus restoration before an action opens a dialog.
                await tick();
                onNew(item.id);
              }}
            >
              <item.Icon size="14" class="text-[var(--color-text-4)]" /> {item.label}
            </DropdownMenu.Item>
          {/each}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  </div>
</div>
