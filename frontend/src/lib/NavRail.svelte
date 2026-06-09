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
  // Utility views are always pushed to the bottom of the rail.
  const UTILITY_IDS = new Set(['vault', 'keys', 'plugins', 'activity', 'settings']);
  let mainViews = $derived(views.filter(v => !UTILITY_IDS.has(v.id)));
  let utilityViews = $derived(views.filter(v => UTILITY_IDS.has(v.id)));
</script>

<nav class="flex flex-col items-center gap-px border-r hairline surface-1 py-2">
  {#each views as v (v.id)}
    <button
      class="group relative flex h-8 w-8 items-center justify-center transition-all {app.view === v.id
        ? 'text-[var(--color-accent)]'
        : 'text-[var(--color-text-4)] hover:text-[var(--color-text-2)]'}"
      onclick={() => (app.view = v.id)}
      onmouseenter={() => (hoveredNav = v.label)}
      onmouseleave={() => (hoveredNav = null)}
      onfocus={() => (hoveredNav = v.label)}
      onblur={() => (hoveredNav = null)}
    >
      {#if app.view === v.id}
        <!-- Active: left accent bar + phosphor glow bg -->
        <span class="absolute inset-0 bg-[var(--color-accent)]/10"></span>
        <span class="absolute left-0 inset-y-0 w-1 bg-[var(--color-accent)] shadow-[0_0_12px_var(--color-accent)] rounded-r-sm"></span>
      {:else}
        <!-- Hover glow -->
        <span class="absolute inset-0 bg-[var(--color-accent)]/0 group-hover:bg-[var(--color-accent)]/3 transition-colors duration-200"></span>
      {/if}
      <v.Icon size="14" strokeWidth={app.view === v.id ? 1.5 : 1.5} />

      {#if hoveredNav === v.label}
        <div class="absolute left-full z-50 ml-2 whitespace-nowrap border hairline-strong bg-[var(--color-surface-2)] px-2.5 py-1.5 text-xs text-[var(--color-text-1)] shadow-xl pointer-events-none fade-up"
          style="box-shadow: 0 0 12px rgba(0,255,136,0.06), 0 4px 16px rgba(0,0,0,0.4);">
          {v.label}
        </div>
      {/if}
    </button>
  {/each}
  {#if pluginPanels.length > 0}
    <div class="my-1.5 h-px w-5 bg-[var(--color-line)]"></div>
    {#each pluginPanels as panel (panel.pluginId + ':' + panel.id)}
      {@const viewID = `plugin:${panel.pluginId}:${panel.id}` as View}
      <button
        class="group relative flex h-8 w-8 items-center justify-center transition-all {app.view === viewID
          ? 'text-[var(--color-accent)]'
          : 'text-[var(--color-text-4)] hover:text-[var(--color-text-2)]'}"
        onclick={() => (app.view = viewID)}
        onmouseenter={() => (hoveredNav = panel.title)}
        onmouseleave={() => (hoveredNav = null)}
        onfocus={() => (hoveredNav = panel.title)}
        onblur={() => (hoveredNav = null)}
      >
        {#if app.view === viewID}
          <span class="absolute inset-0 bg-[var(--color-accent)]/10"></span>
          <span class="absolute left-0 inset-y-0 w-1 bg-[var(--color-accent)] shadow-[0_0_12px_var(--color-accent)] rounded-r-sm"></span>
        {/if}
        <Puzzle size="14" />
        {#if hoveredNav === panel.title}
          <div class="absolute left-full z-50 ml-2 whitespace-nowrap border hairline-strong bg-[var(--color-surface-2)] px-2.5 py-1.5 text-xs text-[var(--color-text-1)] shadow-xl pointer-events-none fade-up">
            {panel.title}
          </div>
        {/if}
      </button>
    {/each}
  {/if}
</nav>
