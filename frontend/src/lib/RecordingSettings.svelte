<script lang="ts">
  import { onMount } from "svelte";
  import { RecordingService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { RecordingPolicy, RecordingStorage } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { app } from "./state.svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";
  let { onchanged }: { onchanged: () => Promise<void> } = $props();
  let policy = $state<RecordingPolicy>({ retentionDays: 0, maxStorageMB: 0, hostModes: {} });
  let storage = $state<RecordingStorage>({ usedBytes: 0, limitBytes: 0 });
  let busy = $state(true);
  let error = $state("");
  let pending = $state<RecordingPolicy | null>(null);
  onMount(() => {
    void Promise.all([RecordingService.Policy(), RecordingService.Storage()]).then(([cfg, usage]) => { policy = cfg; storage = usage; }).catch((e) => error = String(e)).finally(() => busy = false);
    const timer = setInterval(() => { void RecordingService.Storage().then((usage) => storage = usage).catch(() => {}); }, 10000);
    return () => clearInterval(timer);
  });
  function setMode(id: string, mode: string) {
    const next = { ...policy.hostModes };
    if (!mode) delete next[id]; else next[id] = mode;
    policy.hostModes = next;
  }
  function review() {
    const cfg = $state.snapshot(policy);
    if (cfg.retentionDays || cfg.maxStorageMB) pending = cfg;
    else void save(cfg);
  }
  async function save(cfg: RecordingPolicy) {
    pending = null; busy = true; error = "";
    try { await RecordingService.SetPolicy(cfg); storage = await RecordingService.Storage(); await onchanged(); app.toast("ok", "Recording preferences saved"); }
    catch (e) { error = String(e); }
    finally { busy = false; }
  }
</script>

<details class="shrink-0 border-b hairline surface-1 px-4 py-3">
  <summary class="cursor-pointer type-caption">Recording preferences · {(storage.usedBytes/1024/1024).toFixed(1)} MiB used{storage.limitBytes ? ` / ${(storage.limitBytes/1024/1024).toFixed(0)} MiB` : ''}</summary>
  <form class="mt-3 space-y-3" onsubmit={(e) => { e.preventDefault(); review(); }}>
    {#if error}<p role="alert" class="type-caption text-[var(--color-danger)]">{error}</p>{/if}
    <fieldset disabled={busy} class="space-y-3">
      <div class="grid grid-cols-2 gap-3 type-caption">
        <label>Keep completed recordings (days)<input required type="number" min="0" max="3650" bind:value={policy.retentionDays} class="mt-1 w-full rounded border hairline surface-3 p-2" /></label>
        <label>Total recording storage limit (MiB)<input required type="number" min="0" max="1048576" bind:value={policy.maxStorageMB} class="mt-1 w-full rounded border hairline surface-3 p-2" /></label>
      </div>
      <p class="type-caption text-[var(--color-text-3)]">Zero means unlimited. Limits delete expired or oldest completed recordings automatically. Active recordings are never deleted; capture pauses when storage fills. Resume from the terminal after freeing space.</p>
      <div class="grid max-h-48 grid-cols-2 gap-2 overflow-y-auto">
        {#each [{ id: '', name: 'Local shells' }, ...app.hosts.filter((h) => !h.protocol || h.protocol === 'ssh')] as host (host.id)}
          <label class="flex items-center justify-between gap-2 type-caption"><span class="truncate">{host.name}</span><select aria-label={`Recording mode for ${host.name}`} value={policy.hostModes[host.id] ?? ''} onchange={(e) => setMode(host.id, e.currentTarget.value)} class="rounded border hairline surface-3 p-1"><option value="">Use global setting</option><option value="always">Always record</option><option value="never">Never record</option></select></label>
        {/each}
      </div>
      <p class="type-caption text-[var(--color-text-3)]">Host preferences apply to new SSH/local sessions. Mosh, Telnet, and Serial sessions are not recorded. Pause and resume an active recording with its REC button.</p>
      <button class="rounded border hairline px-3 py-2 type-caption disabled:opacity-40">Save recording preferences</button>
    </fieldset>
  </form>
</details>

{#if pending}
  <ConfirmDanger portal title="Apply recording limits" body="Saving these preferences deletes expired and oldest completed recordings to meet the limits. This also runs automatically every minute. Deleted recordings cannot be restored." severity="warn" productionHosts={[]} allowEnterConfirm={false} onCancel={() => pending = null} onConfirm={() => { if (pending) void save(pending); }} />
{/if}
