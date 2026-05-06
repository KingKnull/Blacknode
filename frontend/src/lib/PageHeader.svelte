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

<div class="flex items-center gap-3 border-b hairline surface-1 px-4 py-2">
  <div
    class="flex h-6 w-6 items-center justify-center border border-[var(--color-accent)]/30 bg-[var(--color-accent)]/6 text-[var(--color-accent)]"
  >
    <Icon size="12" />
  </div>
  <div>
    <div class="font-mono text-[11px] font-bold uppercase tracking-[0.12em] text-[var(--color-text-1)]">{title}</div>
    {#if subtitle}
      <div class="font-mono text-[9px] uppercase tracking-widest text-[var(--color-text-4)]">{subtitle}</div>
    {/if}
  </div>
  {#if actions}
    <div class="ml-auto flex items-center gap-2">
      {@render actions()}
    </div>
  {/if}
</div>
