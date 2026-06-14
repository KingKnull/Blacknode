<script lang="ts">
  import { app, type View } from "./state.svelte";
  import type { PanelView } from "../../bindings/github.com/blacknode/blacknode/internal/plugin/models";
  import { Puzzle } from "@lucide/svelte";

  type ViewDef = { id: View; label: string; Icon: any };

  type Props = {
    views: ViewDef[];
    pluginPanels: PanelView[];
  };
  let { views, pluginPanels }: Props = $props();

  let hoveredNav = $state<string | null>(null);

  // Split views into main (top) and utility (bottom).
  const UTILITY_IDS = new Set(['vault', 'keys', 'plugins', 'activity', 'settings']);
  let mainViews = $derived(views.filter(v => !UTILITY_IDS.has(v.id)));
  let utilityViews = $derived(views.filter(v => UTILITY_IDS.has(v.id)));

  // Functional grouping for the main rail. A divider is drawn whenever the
  // group key changes between adjacent items, so related views cluster
  // visually without needing labels.
  const GROUP: Partial<Record<View, string>> = {
    terminals: 'session', exec: 'session',
    files: 'files', recordings: 'files',
    metrics: 'observe', logs: 'observe', history: 'observe',
    forwards: 'net', network: 'net', topology: 'net',
    containers: 'work', processes: 'work',
    http: 'data', database: 'data',
    snippets: 'lib',
  };
  function dividerBefore(list: ViewDef[], i: number): boolean {
    if (i === 0) return false;
    return GROUP[list[i].id] !== GROUP[list[i - 1].id];
  }
</script>

{#snippet navButton(id: View, label: string, Icon: any)}
  {@const active = app.view === id}
  <button
    class="group relative flex h-8 w-8 items-center justify-center transition-all {active
      ? 'text-[var(--color-accent)]'
      : 'text-[var(--color-text-4)] hover:text-[var(--color-text-2)]'}"
    onclick={() => (app.view = id)}
    onmouseenter={() => (hoveredNav = label)}
    onmouseleave={() => (hoveredNav = null)}
    onfocus={() => (hoveredNav = label)}
    onblur={() => (hoveredNav = null)}
    aria-label={label}
    aria-current={active ? 'page' : undefined}
  >
    {#if active}
      <span class="absolute inset-0 bg-[var(--color-accent)]/10"></span>
      <span class="absolute left-0 inset-y-0 w-1 bg-[var(--color-accent)] shadow-[0_0_12px_var(--color-accent)] rounded-r-sm"></span>
    {:else}
      <span class="absolute inset-0 bg-[var(--color-accent)]/0 group-hover:bg-[var(--color-accent)]/3 transition-colors duration-200"></span>
    {/if}
    <Icon size="14" strokeWidth={active ? 2 : 1.5} />

    {#if hoveredNav === label}
      <div class="absolute left-full z-50 ml-2 whitespace-nowrap border hairline-strong bg-[var(--color-surface-2)] px-2.5 py-1.5 type-caption text-[var(--color-text-1)] shadow-xl pointer-events-none fade-up"
        style="border-radius: var(--radius-sm); box-shadow: 0 0 12px rgba(0,255,136,0.06), 0 4px 16px rgba(0,0,0,0.4);">
        {label}
      </div>
    {/if}
  </button>
{/snippet}

<nav class="flex flex-col items-center gap-px border-r hairline surface-1 py-2" aria-label="Primary">
  {#each mainViews as v, i (v.id)}
    {#if dividerBefore(mainViews, i)}
      <div class="my-1 h-px w-5 bg-[var(--color-line)]"></div>
    {/if}
    {@render navButton(v.id, v.label, v.Icon)}
  {/each}

  {#if pluginPanels.length > 0}
    <div class="my-1.5 h-px w-5 bg-[var(--color-line)]"></div>
    {#each pluginPanels as panel (panel.pluginId + ':' + panel.id)}
      {@const viewID = `plugin:${panel.pluginId}:${panel.id}` as View}
      {@render navButton(viewID, panel.title, Puzzle)}
    {/each}
  {/if}

  <!-- Spacer pushes utility views to bottom -->
  <div class="flex-1"></div>
  <div class="my-1 h-px w-5 bg-[var(--color-line)]"></div>

  {#each utilityViews as v (v.id)}
    {@render navButton(v.id, v.label, v.Icon)}
  {/each}
</nav>
