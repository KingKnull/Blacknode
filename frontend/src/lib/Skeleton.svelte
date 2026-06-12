<script lang="ts">
  type Props = {
    /** Number of placeholder rows. */
    rows?: number;
    /** Tailwind height utility for each row. */
    rowClass?: string;
    /** Padding around the stack. */
    class?: string;
  };
  let { rows = 6, rowClass = "h-8", class: klass = "p-4" }: Props = $props();
</script>

<div class="{klass} space-y-2" aria-hidden="true" data-loading>
  {#each Array(rows) as _, i (i)}
    <div class="shimmer {rowClass} w-full rounded-sm" style="opacity: {1 - i * 0.1}"></div>
  {/each}
</div>
