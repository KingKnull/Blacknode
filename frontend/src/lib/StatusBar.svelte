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

<footer class="flex h-5 shrink-0 items-center gap-5 border-t hairline px-3 font-mono type-micro text-[var(--color-text-4)] select-none">
  <span class="flex items-center gap-1.5">
    <span class="text-[var(--color-accent)]/50">//</span>
    <Server size="8" /> {app.hosts.length} HOST{app.hosts.length === 1 ? '' : 'S'}
  </span>
  <span class="flex items-center gap-1.5">
    <TerminalSquare size="8" /> {tabCount} TAB{tabCount === 1 ? '' : 'S'}{activeLeafCount > 1 ? ` · ${activeLeafCount} PANES` : ''}
  </span>
  {#if app.settings.hasAnthropicKey}
    <span class="flex items-center gap-1.5 text-[var(--color-accent)]/50">
      <Sparkles size="8" /> AI_READY
    </span>
  {/if}
  {#if app.broadcastEnabled}
    <span class="flex items-center gap-1.5 text-[var(--color-warn)]">
      <Radio size="8" class="pulse-soft" /> BROADCASTING
    </span>
  {/if}
  <span class="ml-auto text-[var(--color-text-4)]/60">v0.1-alpha</span>
</footer>
