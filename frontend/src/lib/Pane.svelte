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
    onresize?: (splitID: string, ratio: number) => void;
  };
  let { node, activeLeafID, onactivate, onsplit, onclose, onresize }: Props = $props();

  // Drag state lives in the split branch only — leaf branches don't render a
  // divider. Tracked here so the pointermove handler can resolve the new
  // ratio against the parent's bounding rect.
  let containerEl: HTMLDivElement | undefined = $state();
  let dragging = $state(false);

  function startDrag(e: PointerEvent) {
    if (node.kind !== "split" || !containerEl) return;
    e.preventDefault();
    dragging = true;
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
  }

  function onMove(e: PointerEvent) {
    if (!dragging || node.kind !== "split" || !containerEl) return;
    const rect = containerEl.getBoundingClientRect();
    const ratio =
      node.direction === "horizontal"
        ? (e.clientX - rect.left) / rect.width
        : (e.clientY - rect.top) / rect.height;
    onresize?.(node.id, ratio);
  }

  function endDrag(e: PointerEvent) {
    if (!dragging) return;
    dragging = false;
    try {
      (e.target as HTMLElement).releasePointerCapture(e.pointerId);
    } catch {
      // capture may already be released
    }
  }

  // Reset to even split on double-click — common splitter convention.
  function resetRatio() {
    if (node.kind !== "split") return;
    onresize?.(node.id, 0.5);
  }
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
        ? 'border-[var(--color-accent)]/40 shadow-[inset_0_0_0_1px_rgba(34,211,238,0.15)]'
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
    bind:this={containerEl}
    class="flex h-full w-full {node.direction === 'horizontal'
      ? 'flex-row'
      : 'flex-col'}"
  >
    <div
      class="min-h-0 min-w-0 overflow-hidden"
      style:flex-basis="{(node.ratio ?? 0.5) * 100}%"
      style:flex-grow="0"
      style:flex-shrink="0"
    >
      <Self
        node={node.a}
        {activeLeafID}
        {onactivate}
        {onsplit}
        {onclose}
        {onresize}
      />
    </div>

    <!-- Draggable splitter. Visible 1px line + 6px hit area. -->
    <div
      class="group relative flex shrink-0 items-center justify-center {node.direction ===
      'horizontal'
        ? 'w-1.5 cursor-col-resize'
        : 'h-1.5 cursor-row-resize'} {dragging ? 'bg-[var(--color-accent)]/30' : ''}"
      role="separator"
      onpointerdown={startDrag}
      onpointermove={onMove}
      onpointerup={endDrag}
      onpointercancel={endDrag}
      ondblclick={resetRatio}
      aria-orientation={node.direction === "horizontal" ? "vertical" : "horizontal"}
    >
      <div
        class="absolute bg-[var(--color-line)] transition-colors group-hover:bg-[var(--color-accent)]/40 {node.direction ===
        'horizontal'
          ? 'h-full w-px'
          : 'h-px w-full'} {dragging ? '!bg-[var(--color-accent)]' : ''}"
      ></div>
    </div>

    <div
      class="min-h-0 min-w-0 overflow-hidden"
      style:flex-basis="{(1 - (node.ratio ?? 0.5)) * 100}%"
      style:flex-grow="0"
      style:flex-shrink="0"
    >
      <Self
        node={node.b}
        {activeLeafID}
        {onactivate}
        {onsplit}
        {onclose}
        {onresize}
      />
    </div>
  </div>
{/if}
