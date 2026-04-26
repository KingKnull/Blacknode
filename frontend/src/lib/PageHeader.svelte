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

<div class="flex items-center gap-3 border-b hairline surface-1 px-4 py-2.5">
  <div
    class="flex h-7 w-7 items-center justify-center rounded-md bg-[var(--color-accent)]/10 text-[var(--color-accent)]"
  >
    <Icon size="14" />
  </div>
  <div>
    <div class="text-sm font-semibold leading-tight">{title}</div>
    {#if subtitle}
      <div class="text-[11px] text-[var(--color-text-3)]">{subtitle}</div>
    {/if}
  </div>
  {#if actions}
    <div class="ml-auto flex items-center gap-2">
      {@render actions()}
    </div>
  {/if}
</div>
