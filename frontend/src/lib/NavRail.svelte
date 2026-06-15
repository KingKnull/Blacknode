<script lang="ts">
  type Section = { id: string; label: string; Icon: any };

  type Props = {
    sections: Section[];
    activeSectionId: string;
    onSelect: (id: string) => void;
  };
  let { sections, activeSectionId, onSelect }: Props = $props();

  // Utility sections live at the bottom of the rail.
  const UTILITY = new Set(["vault", "plugins", "settings"]);
  let mainSections = $derived(sections.filter((s) => !UTILITY.has(s.id)));
  let utilSections = $derived(sections.filter((s) => UTILITY.has(s.id)));
</script>

{#snippet railButton(s: Section)}
  {@const active = activeSectionId === s.id}
  <button
    class="group relative flex w-full flex-col items-center justify-center gap-1 py-2 transition-colors {active
      ? 'text-[var(--color-accent)]'
      : 'text-[var(--color-text-4)] hover:text-[var(--color-text-2)]'}"
    onclick={() => onSelect(s.id)}
    aria-current={active ? "page" : undefined}
    title={s.label}
  >
    {#if active}
      <span class="absolute left-0 inset-y-1 w-0.5 rounded-r bg-[var(--color-accent)]"></span>
    {/if}
    <s.Icon size="18" strokeWidth={active ? 2 : 1.6} />
    <span class="type-micro leading-none">{s.label}</span>
  </button>
{/snippet}

<nav class="flex flex-col items-stretch border-r hairline surface-1 py-1.5" aria-label="Sections">
  {#each mainSections as s (s.id)}
    {@render railButton(s)}
  {/each}

  <div class="flex-1"></div>

  {#each utilSections as s (s.id)}
    {@render railButton(s)}
  {/each}
</nav>
