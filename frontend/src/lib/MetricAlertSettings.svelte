<script lang="ts">
  import { onMount } from "svelte";
  import { MetricsService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { MetricAlertConfig } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { app } from "./state.svelte";
  let config = $state<MetricAlertConfig>({ enabled: true, cpuThreshold: 90, memoryThreshold: 90, diskThreshold: 90, holdSeconds: 0, recoveryNotifications: true });
  let busy = $state(true);
  let error = $state("");
  onMount(() => { void MetricsService.GetAlertConfig().then((cfg) => config = cfg).catch((e) => error = String(e)).finally(() => busy = false); });
  async function save() {
    busy = true; error = "";
    try { await MetricsService.SetAlertConfig($state.snapshot(config)); app.toast("ok", "Alert rules saved"); }
    catch (e) { error = String(e); }
    finally { busy = false; }
  }
</script>

<details class="shrink-0 border-b hairline surface-1 px-4 py-3">
  <summary class="cursor-pointer type-caption">Alert rules</summary>
  <form class="mt-3 space-y-3" onsubmit={(e) => { e.preventDefault(); void save(); }}>
    {#if error}<p role="alert" class="type-caption text-[var(--color-danger)]">{error}</p>{/if}
    <fieldset disabled={busy} class="space-y-3">
      <label class="flex items-center gap-2 type-caption"><input type="checkbox" bind:checked={config.enabled} />Enable metric alerts</label>
      <div class="grid grid-cols-4 gap-3 type-caption">
        <label>CPU threshold (%)<input required type="number" min="1" max="100" step="0.1" bind:value={config.cpuThreshold} class="mt-1 w-full rounded border hairline surface-3 p-2" /></label>
        <label>Memory threshold (%)<input required type="number" min="1" max="100" step="0.1" bind:value={config.memoryThreshold} class="mt-1 w-full rounded border hairline surface-3 p-2" /></label>
        <label>Disk threshold (%)<input required type="number" min="1" max="100" step="0.1" bind:value={config.diskThreshold} class="mt-1 w-full rounded border hairline surface-3 p-2" /></label>
        <label>Sustained for (seconds)<input required type="number" min="0" max="3600" bind:value={config.holdSeconds} class="mt-1 w-full rounded border hairline surface-3 p-2" /></label>
      </div>
      <label class="flex items-center gap-2 type-caption"><input type="checkbox" bind:checked={config.recoveryNotifications} />Notify when a metric recovers</label>
      <p class="type-caption text-[var(--color-text-3)]">Applies to hosts being polled. A sustained alert repeats at most every five minutes. Recovery needs a reading at least three percentage points below the threshold (half the threshold when it is below 6%).</p>
      <button class="rounded border hairline px-3 py-2 type-caption disabled:opacity-40">Save alert rules</button>
    </fieldset>
  </form>
</details>
