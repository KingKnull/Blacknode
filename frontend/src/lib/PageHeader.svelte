<script lang="ts">
  import type { Snippet } from "svelte";

  type Props = {
    icon: any;
    title: string;
    subtitle?: string;
    actions?: Snippet;
  };
  let { icon: Icon, title, subtitle, actions }: Props = $props();
</script>

<div class="flex shrink-0 flex-wrap items-center gap-3 border-b hairline surface-1 px-4 py-3">
  <div
    class="flex h-8 w-8 shrink-0 items-center justify-center border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 text-[var(--color-accent)]"
    style="border-radius: var(--radius-sm);"
  >
    <Icon size="16" />
  </div>
  <div class="min-w-0 flex-1">
    <h2 class="type-title text-[var(--color-text-1)]">{title}</h2>
    {#if subtitle}
      <div class="type-caption text-[var(--color-text-4)] mt-0.5">{subtitle}</div>
    {/if}
  </div>
  {#if actions}
    <div class="ml-auto flex max-w-full flex-wrap items-center gap-2">
      {@render actions()}
    </div>
  {/if}
</div>
