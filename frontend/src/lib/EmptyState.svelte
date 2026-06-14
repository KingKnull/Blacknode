<script lang="ts">
  type Props = {
    icon?: any;
    title: string;
    description?: string;
    /** Optional call-to-action button(s). */
    action?: any;
    /** Compact variant for inline / list-embedded empties. */
    compact?: boolean;
  };
  let { icon: Icon, title, description, action, compact = false }: Props = $props();
</script>

<div class="flex {compact ? 'py-10' : 'h-full'} w-full items-center justify-center px-4">
  <div class="max-w-xs text-center">
    {#if Icon}
      <div class="mx-auto mb-3 flex h-12 w-12 items-center justify-center border hairline text-[var(--color-text-4)]"
        style="border-radius: var(--radius-sm); box-shadow: 0 0 20px rgba(0,255,136,0.04);">
        <Icon size="20" class="glow-pulse" />
      </div>
    {/if}
    <p class="type-caption font-medium text-[var(--color-text-3)]">{title}</p>
    {#if description}
      <p class="mt-1 type-caption text-[var(--color-text-4)]">{description}</p>
    {/if}
    {#if action}
      <div class="mt-3 flex items-center justify-center gap-2">
        {@render action()}
      </div>
    {/if}
  </div>
</div>
