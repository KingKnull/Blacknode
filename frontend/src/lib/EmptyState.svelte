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

<div class="fade-up flex {compact ? 'py-10' : 'h-full'} w-full items-center justify-center px-4">
  <div class="max-w-xs text-center">
    {#if Icon}
      <!-- Layered icon treatment: soft glow ring behind a bordered tile,
           rather than a flat box — gives empty states a touch more presence
           without becoming illustration-heavy. -->
      <div class="relative mx-auto mb-4 flex h-14 w-14 items-center justify-center">
        <span
          class="absolute inset-0 rounded-full opacity-60 blur-md"
          style="background: radial-gradient(circle, color-mix(in srgb, var(--color-accent) 18%, transparent) 0%, transparent 70%);"
        ></span>
        <div
          class="relative flex h-12 w-12 items-center justify-center border hairline-strong surface-2"
          style="border-radius: var(--radius-md); box-shadow: 0 0 24px rgba(59, 130, 246, 0.06), inset 0 1px 0 rgba(255,255,255,0.03);"
        >
          <Icon size="20" class="text-[var(--color-text-3)] glow-pulse" />
        </div>
      </div>
    {/if}
    <p class="type-body font-medium text-[var(--color-text-2)]">{title}</p>
    {#if description}
      <p class="mt-1.5 type-caption leading-relaxed text-[var(--color-text-4)]">{description}</p>
    {/if}
    {#if action}
      <div class="mt-4 flex items-center justify-center gap-2">
        {@render action()}
      </div>
    {/if}
  </div>
</div>
