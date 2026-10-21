<script lang="ts">
  import { onMount, tick } from "svelte";
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

  let menuOpen = $state(false);
  let newButton: HTMLButtonElement;
  let menu = $state<HTMLDivElement>();
  let viewList: HTMLDivElement;

  function closeMenu(restoreFocus = true) {
    menuOpen = false;
    if (restoreFocus) newButton?.focus();
  }

  async function openMenu(last = false) {
    menuOpen = true;
    await tick();
    const items = menu?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]');
    if (items?.length) items[last ? items.length - 1 : 0].focus();
  }

  function menuKeydown(e: KeyboardEvent) {
    const items = [...(menu?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? [])];
    if (!items.length) return;
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    let next: number;
    if (e.key === "ArrowDown") next = (index + 1) % items.length;
    else if (e.key === "ArrowUp") next = (index - 1 + items.length) % items.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = items.length - 1;
    else if (e.key === "Tab") { closeMenu(false); return; }
    else return;
    e.preventDefault();
    items[next].focus();
  }

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

  onMount(() => {
    const onKeydown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && menuOpen) { e.preventDefault(); closeMenu(); }
    };
    window.addEventListener("keydown", onKeydown);
    return () => window.removeEventListener("keydown", onKeydown);
  });
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
  <div class="relative shrink-0" onfocusout={(e) => { if (menuOpen && e.relatedTarget instanceof Node && !e.currentTarget.contains(e.relatedTarget)) closeMenu(false); }}>
    <button
      bind:this={newButton}
      class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-2.5 py-1.5 type-caption font-semibold text-[var(--color-surface-0)] transition-opacity hover:opacity-90"
      aria-haspopup="menu"
      aria-expanded={menuOpen}
      aria-controls={menuOpen ? "new-item-menu" : undefined}
      onclick={() => { if (menuOpen) closeMenu(); else void openMenu(); }}
      onkeydown={(e) => { if (e.key === "ArrowDown" || e.key === "ArrowUp") { e.preventDefault(); void openMenu(e.key === "ArrowUp"); } }}
    >
      <Plus size="13" /> New
    </button>
    {#if menuOpen}
      <button class="fixed inset-0 z-40 cursor-default" tabindex="-1" aria-label="Close menu" onclick={() => closeMenu()}></button>
      <div bind:this={menu} id="new-item-menu" role="menu" aria-label="Create new" tabindex="-1" onkeydown={menuKeydown} class="fade-up absolute right-0 top-full z-50 mt-1 min-w-[190px] overflow-hidden border hairline-strong surface-2 py-1 shadow-xl shadow-black/40" style="border-radius: var(--radius-md);">
        {#each NEW_ITEMS.filter((item) => app.isViewVisible(item.id)) as item (item.id)}
          <button
            role="menuitem"
            tabindex="-1"
            class="flex min-h-8 w-full items-center gap-2.5 px-3 py-2 text-left type-caption text-[var(--color-text-2)] transition-colors hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)] focus-visible:bg-[var(--color-surface-3)]"
            onclick={() => { closeMenu(); onNew(item.id); }}
          >
            <item.Icon size="14" class="text-[var(--color-text-4)]" /> {item.label}
          </button>
        {/each}
      </div>
    {/if}
  </div>
</div>
