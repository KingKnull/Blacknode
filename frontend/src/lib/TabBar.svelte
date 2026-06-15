<script lang="ts">
  import { Plus, X } from "@lucide/svelte";
  import { leaves, type PaneNode } from "./panes";

  type Tab = { id: string; root: PaneNode; activeLeafID: string };

  type Props = {
    tabs: Tab[];
    activeTabID: string;
    tabLabel: (t: Tab) => string;
    onNewTab: () => void;
    onCloseTab: (id: string) => void;
    onCloseOthers: (id: string) => void;
    onSelectTab: (id: string) => void;
  };
  let { tabs, activeTabID, tabLabel, onNewTab, onCloseTab, onCloseOthers, onSelectTab }: Props = $props();

  // Drag-reorder state.
  let dragSourceID = $state<string | null>(null);
  let dragOverID = $state<string | null>(null);

  function tabDragStart(e: DragEvent, id: string) {
    dragSourceID = id;
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = "move";
      e.dataTransfer.setData("text/plain", id);
    }
  }
  function tabDragOver(e: DragEvent, id: string) {
    if (!dragSourceID || dragSourceID === id) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
    dragOverID = id;
    const from = tabs.findIndex((t) => t.id === dragSourceID);
    const to = tabs.findIndex((t) => t.id === id);
    if (from === -1 || to === -1 || from === to) return;
    const [moved] = tabs.splice(from, 1);
    tabs.splice(to, 0, moved);
  }
  function tabDragEnd() {
    dragSourceID = null;
    dragOverID = null;
  }

  // Right-click context menu on tabs.
  let tabMenu = $state<{ x: number; y: number; tabID: string } | null>(null);
  function openTabMenu(e: MouseEvent, id: string) {
    e.preventDefault();
    tabMenu = { x: e.clientX, y: e.clientY, tabID: id };
  }
  function closeTabMenu() {
    tabMenu = null;
  }
</script>

<div class="flex h-8 shrink-0 items-center gap-px border-b hairline surface-1 px-2">
  {#each tabs as t (t.id)}
    {@const label = tabLabel(t)}
    {@const isActive = activeTabID === t.id}
    <div
      role="tab"
      tabindex="0"
      draggable="true"
      aria-selected={isActive}
      class="group flex max-w-[180px] cursor-pointer items-center gap-1.5 border-r border-[var(--color-line)] px-3 py-1 type-caption select-none transition-colors {isActive ? 'bg-[var(--color-surface-2)] text-[var(--color-text-1)] border-t border-t-[var(--color-accent)]/60' : 'text-[var(--color-text-4)] hover:bg-[var(--color-surface-2)]/50 hover:text-[var(--color-text-3)]'}"
      class:opacity-40={dragSourceID === t.id}
      class:outline={dragOverID === t.id && dragSourceID !== t.id}
      class:outline-[var(--color-accent)]={dragOverID === t.id && dragSourceID !== t.id}
      onclick={() => onSelectTab(t.id)}
      onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onSelectTab(t.id); } }}
      oncontextmenu={(e) => openTabMenu(e, t.id)}
      ondragstart={(e) => tabDragStart(e, t.id)}
      ondragover={(e) => tabDragOver(e, t.id)}
      ondragend={tabDragEnd}
      ondrop={(e) => e.preventDefault()}
    >
      {#if isActive}
        <span class="h-1.5 w-1.5 shrink-0 rounded-full bg-[var(--color-accent)] phosphor-flicker shadow-[0_0_4px_var(--color-accent)]"></span>
      {:else}
        <span class="h-1 w-1 shrink-0 rounded-full bg-[var(--color-text-4)]"></span>
      {/if}
      <span class="truncate font-mono type-caption">{label}</span>
      <span
        role="button"
        tabindex="0"
        class="ml-auto shrink-0 p-0.5 opacity-0 group-hover:opacity-40 hover:!opacity-100 hover:text-[var(--color-danger)]"
        onclick={(e) => { e.stopPropagation(); onCloseTab(t.id); }}
        onkeydown={(e) => { if (e.key === 'Enter') { e.stopPropagation(); onCloseTab(t.id); } }}
      ><X size="9" /></span>
    </div>
  {/each}
  <button
    class="ml-1 flex h-6 w-6 shrink-0 items-center justify-center border border-[var(--color-line)] text-[var(--color-text-4)] hover:border-[var(--color-accent)]/40 hover:text-[var(--color-accent)] transition-colors"
    style="border-radius: var(--radius-sm);"
    onclick={onNewTab}
    title="New terminal (⌘T)"
  >
    <Plus size="10" />
  </button>
</div>

<!-- ── TAB CONTEXT MENU ─────────────────────────────────── -->
{#if tabMenu}
  <div
    class="fixed inset-0 z-40"
    role="presentation"
    onclick={closeTabMenu}
    oncontextmenu={(e) => { e.preventDefault(); closeTabMenu(); }}
  ></div>
  <div
    class="fade-up fixed z-50 min-w-[160px] overflow-hidden border hairline-strong surface-2 py-0.5 font-mono type-micro shadow-xl shadow-black/60"
    style="left: {tabMenu.x}px; top: {tabMenu.y}px; border-radius: var(--radius-sm);"
  >
    <button
      class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-danger)] transition-colors"
      onclick={() => { if (tabMenu) onCloseTab(tabMenu.tabID); closeTabMenu(); }}
    ><X size="10" /> CLOSE TAB</button>
    <button
      class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-danger)] disabled:opacity-30 transition-colors"
      disabled={tabs.length <= 1}
      onclick={() => { if (tabMenu) onCloseOthers(tabMenu.tabID); closeTabMenu(); }}
    ><X size="10" /> CLOSE OTHERS</button>
  </div>
{/if}
