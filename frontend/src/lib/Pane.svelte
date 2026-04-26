<script lang="ts">
  import type { PaneNode } from "./panes";
  import Terminal from "./Terminal.svelte";
  import Self from "./Pane.svelte";
  import { SplitSquareHorizontal, SplitSquareVertical, X } from "@lucide/svelte";

  type Props = {
    node: PaneNode;
    activeLeafID: string | null;
    onactivate: (leafID: string) => void;
    onsplit: (leafID: string, direction: "horizontal" | "vertical") => void;
    onclose: (leafID: string) => void;
  };
  let { node, activeLeafID, onactivate, onsplit, onclose }: Props = $props();
</script>

{#if node.kind === "leaf"}
  <div
    class="relative h-full w-full"
    role="presentation"
    onmousedown={() => onactivate(node.id)}
  >
    <div
      class="pointer-events-none absolute inset-0 z-10 rounded-sm border transition-colors {activeLeafID ===
      node.id
        ? 'border-[var(--color-accent)]/40 shadow-[inset_0_0_0_1px_rgba(16,217,160,0.15)]'
        : 'border-transparent'}"
    ></div>
    <div
      class="absolute right-2 top-9 z-20 flex gap-1 opacity-0 transition-opacity hover:opacity-100"
      class:opacity-60={activeLeafID === node.id}
    >
      <button
        title="Split right"
        class="rounded border hairline-strong surface-2 p-1 text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={(e) => {
          e.stopPropagation();
          onsplit(node.id, "horizontal");
        }}
      >
        <SplitSquareHorizontal size="12" />
      </button>
      <button
        title="Split down"
        class="rounded border hairline-strong surface-2 p-1 text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={(e) => {
          e.stopPropagation();
          onsplit(node.id, "vertical");
        }}
      >
        <SplitSquareVertical size="12" />
      </button>
      <button
        title="Close pane"
        class="rounded border hairline-strong surface-2 p-1 text-[var(--color-text-2)] hover:bg-[var(--color-danger)]/20 hover:text-[var(--color-danger)]"
        onclick={(e) => {
          e.stopPropagation();
          onclose(node.id);
        }}
      >
        <X size="12" />
      </button>
    </div>
    <Terminal sessionID={node.sessionID} />
  </div>
{:else}
  <div
    class="flex h-full w-full {node.direction === 'horizontal'
      ? 'flex-row'
      : 'flex-col'}"
  >
    <div class="min-h-0 min-w-0 flex-1">
      <Self
        node={node.a}
        {activeLeafID}
        {onactivate}
        {onsplit}
        {onclose}
      />
    </div>
    <div
      class="bg-[var(--color-line)] {node.direction === 'horizontal'
        ? 'w-px'
        : 'h-px'}"
    ></div>
    <div class="min-h-0 min-w-0 flex-1">
      <Self
        node={node.b}
        {activeLeafID}
        {onactivate}
        {onsplit}
        {onclose}
      />
    </div>
  </div>
{/if}
