<script lang="ts">
  // Full horizontal lockup: mark + wordmark. The wordmark uses Inter at a
  // slight letter-spacing — enough to read as "technical" without slipping
  // into wide-tracked monospace territory. Single weight for cleanliness.
  import LogoMark from "./LogoMark.svelte";

  type Props = {
    size?: number; // mark size in px; text scales relative to this
    showTagline?: boolean;
  };
  let { size = 26, showTagline = false }: Props = $props();
</script>

<div class="flex items-center gap-2.5">
  <LogoMark {size} glow={false} />
  <div class="flex flex-col leading-none">
    <span
      class="font-semibold tracking-[0.04em] text-[var(--color-text-1)]"
      style="font-size: {size * 0.62}px; font-feature-settings: 'cv11', 'ss01';"
      >blacknode</span
    >
    {#if showTagline}
      <span
        class="mt-1 uppercase tracking-[0.2em] text-[var(--color-text-4)]"
        style="font-size: {size * 0.32}px;"
        >remote ops platform</span
      >
    {/if}
  </div>
</div>
