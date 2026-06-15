<script lang="ts">
  import { app } from "./state.svelte";
  import {
    TerminalSquare,
    Server,
    Sparkles,
    Radio,
  } from "@lucide/svelte";

  type Props = {
    tabCount: number;
    activeLeafCount: number;
  };
  let { tabCount, activeLeafCount }: Props = $props();
</script>

<footer class="flex h-6 shrink-0 items-center gap-4 border-t hairline surface-1 px-3 type-micro text-[var(--color-text-4)] select-none">
  <span class="flex items-center gap-1.5">
    <Server size="11" /> <span class="tabular">{app.hosts.length}</span> {app.hosts.length === 1 ? 'host' : 'hosts'}
  </span>
  <span class="flex items-center gap-1.5">
    <TerminalSquare size="11" /> <span class="tabular">{tabCount}</span> {tabCount === 1 ? 'tab' : 'tabs'}{activeLeafCount > 1 ? ` · ${activeLeafCount} panes` : ''}
  </span>
  {#if app.settings.hasAnthropicKey}
    <span class="flex items-center gap-1.5 text-[var(--color-accent)]">
      <Sparkles size="11" /> AI ready
    </span>
  {/if}
  {#if app.broadcastEnabled}
    <span class="flex items-center gap-1.5 text-[var(--color-warn)]">
      <Radio size="11" class="pulse-soft" /> Broadcasting
    </span>
  {/if}
  <span class="ml-auto text-[var(--color-text-4)]">v0.1-alpha</span>
</footer>
