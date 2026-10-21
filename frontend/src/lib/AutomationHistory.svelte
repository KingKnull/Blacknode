<script lang="ts">
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { JobService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Job, JobSchedule, RunbookResult } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import type { OperationEvent } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import { unpack, type Versioned } from "./operationRecords";
  import Dialog from "./Dialog.svelte";

  let { activeJobID = "", refreshKey = false }: { activeJobID?: string; refreshKey?: boolean } = $props();
  let jobs = $state<Job[]>([]);
  let schedules = $state<Versioned<JobSchedule>[]>([]);
  let selected = $state("");
  let events = $state<OperationEvent[]>([]);
  let error = $state("");
  let resumeJob = $state<Job | null>(null);
  let acknowledged = $state(false);
  let phrase = $state("");
  let busy = $state(false);
  const current = $derived(jobs.find((job) => job.id === selected));
  const productionResume = $derived(resumeJob?.hostIDs.some((id) => app.hosts.find((host) => host.id === id)?.environment === 'production'));

  async function refresh() {
    try {
      const [history, timers] = await Promise.all([JobService.List(), JobService.Schedules()]);
      jobs = history; schedules = timers.map((item) => unpack<JobSchedule>(item));
      if (selected) await loadEvents(selected);
    } catch (e) { error = String(e); }
  }
  async function loadEvents(id: string) {
    const loaded: OperationEvent[] = [];
    for (let page = 0; page < 64; page++) {
      const batch = await JobService.Events(id, loaded.at(-1)?.seq ?? 0);
      loaded.push(...batch);
      if (batch.length < 500) break;
    }
    if (selected === id) events = loaded;
  }
  $effect(() => { if (activeJobID) selected = activeJobID; });
  $effect(() => { refreshKey; void refresh(); });
  $effect(() => { const id = selected; events = []; if (id) void loadEvents(id).catch((e) => error = String(e)); });
  onMount(() => {
    const off = Events.On('job:changed', () => void refresh());
    const timer = setInterval(() => void refresh(), 5000);
    return () => { off(); clearInterval(timer); };
  });
  async function cancel(id: string) { try { await JobService.Cancel(id); await refresh(); } catch (e) { error = String(e); } }
  async function resume() {
    if (!resumeJob || !acknowledged || (productionResume && phrase !== 'production')) return;
    busy = true;
    try { const job = await JobService.Resume(resumeJob.id, true); selected = job.id; resumeJob = null; await refresh(); }
    catch (e) { error = String(e); } finally { busy = false; }
  }
  async function toggle(schedule: Versioned<JobSchedule>) {
    try { await JobService.SetScheduleEnabled(schedule.id, !schedule.enabled, schedule.revision); await refresh(); }
    catch (e) { error = String(e); }
  }
  async function remove(schedule: Versioned<JobSchedule>) {
    if (!confirm(`Delete schedule “${schedule.name}”?`)) return;
    try { await JobService.DeleteSchedule(schedule.id, schedule.revision); await refresh(); }
    catch (e) { error = String(e); }
  }
</script>

<div class="ops-form border-t hairline pt-4">
  <div class="ops-actions"><h3 class="type-body">Schedules & execution history</h3><button onclick={refresh}>Refresh</button></div>
  {#if error}<p role="alert" class="ops-error">{error}</p>{/if}
  <p class="ops-muted">Jobs retain progress when you switch views. Schedules run while this app or the optional runner is active and unlocked; missed intervals are skipped.</p>
  {#if schedules.length}<table class="ops-table"><thead><tr><th>Schedule</th><th>Interval / next run</th><th>Status</th><th>Actions</th></tr></thead><tbody>
    {#each schedules as schedule (schedule.id)}<tr><td>{schedule.name}</td><td>{schedule.everySeconds / 60} min · {new Date(schedule.nextRunAt).toLocaleString()}</td><td>{schedule.enabled ? 'Enabled' : 'Paused'}{#if schedule.lastError}<p class="ops-error">{schedule.lastError}</p>{/if}</td><td><button onclick={() => toggle(schedule)}>{schedule.enabled ? 'Pause' : 'Enable'}</button> <button onclick={() => remove(schedule)}>Delete</button></td></tr>{/each}
  </tbody></table>{/if}
  <label>Execution<select bind:value={selected}><option value="">Select a run</option>{#each jobs as job (job.id)}<option value={job.id}>{job.book.name} · {job.status} · {new Date(job.createdAt).toLocaleString()}</option>{/each}</select></label>
  {#if current}
    <div class="ops-actions"><strong>{current.book.name} · {current.status}</strong>{#if ['queued','running'].includes(current.status)}<button onclick={() => cancel(current.id)}>Cancel run</button>{:else if ['failed','canceled','interrupted'].includes(current.status)}<button onclick={() => { resumeJob = current; acknowledged = false; phrase = ''; }}>Review unfinished steps</button>{/if}</div>
     {#if current.error}<p class="ops-error">{current.error}</p>{/if}
     {#if current.steps.length}<div class="overflow-x-auto"><table class="ops-table"><thead><tr><th>Target</th><th>Step</th><th>Status</th><th>Exit</th><th>Details</th></tr></thead><tbody>
       {#each current.steps as step (step.hostID + ':' + step.index)}<tr><td>{app.hosts.find((host) => host.id === step.hostID)?.name || step.hostID}</td><td>{step.index + 1}. {step.name}</td><td>{step.status}</td><td>{step.exitCode < 0 ? '—' : step.exitCode}</td><td class="max-w-80 break-words">{#if step.error}<span>{step.error}</span>{:else if step.stdout || step.stderr}<details><summary>Output</summary><pre class="max-w-80 whitespace-pre-wrap">{step.stdout}{step.stderr}</pre></details>{:else}—{/if}</td></tr>{/each}
     </tbody></table></div>{/if}
     {#each events as event (event.seq)}
      {@const result = JSON.parse(event.body) as RunbookResult}
      <details class="ops-card" open={result.status.includes('failed')}><summary>{result.stepIndex + 1}. {result.stepName} · {result.result.hostName || app.hosts.find((host) => host.id === result.result.hostID)?.name || result.result.hostID} · {result.status}</summary><pre class="ops-output">{result.command}{'\n'}{result.result.stdout}{result.result.stderr}{result.result.error}</pre></details>
    {/each}
  {/if}
</div>

{#if resumeJob}
  <Dialog portal label="Resume interrupted job" onclose={() => resumeJob = null} panelClass="ops-form w-[min(95vw,720px)] max-h-[85vh] overflow-auto rounded-xl border hairline surface-2 p-5">
    <h2 class="type-title">Resume {resumeJob.book.name}</h2>
    <p>Completed and skipped steps stay complete. Unfinished commands can still be running on a host after a disconnect; inspect their effects before repeating them.</p>
    <p>Hosts: {resumeJob.hostIDs.map((id) => app.hosts.find((host) => host.id === id)?.name || id).join(', ')}</p>
    {#each resumeJob.book.steps as step}<details class="ops-card"><summary>{step.name}</summary><pre class="ops-output">{step.command}{step.when ? `\nCondition: ${step.when}` : ''}{step.healthCheck ? `\nHealth check: ${step.healthCheck}` : ''}{step.rollbackCommand ? `\nRollback: ${step.rollbackCommand}` : ''}</pre></details>{/each}
    <label class="ops-check"><input type="checkbox" bind:checked={acknowledged} />I inspected unfinished commands and their effects.</label>
    {#if productionResume}<label>Type production to resume<input bind:value={phrase} /></label>{/if}
    <div class="ops-actions"><button onclick={() => resumeJob = null}>Cancel</button><button class="primary" disabled={busy || !acknowledged || (!!productionResume && phrase !== 'production')} onclick={resume}>Resume unfinished steps</button></div>
  </Dialog>
{/if}
