<script lang="ts">
  import { onMount } from "svelte";
  import { PlatformService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Integration, FleetDifference } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import type { OperationEvent } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { unpack, downloadText, type Versioned, type Incident } from "./operationRecords";
  import { app } from "./state.svelte";
  import HostSelection from "./HostSelection.svelte";
  import IntegrationsPanel from "./IntegrationsPanel.svelte";
  import PageHeader from "./PageHeader.svelte";
  import { Activity } from "@lucide/svelte";
  let incidents = $state<Versioned<Incident>[]>([]);
  let selectedID = $state("");
  let name = $state("");
  let hosts = $state<string[]>(app.serviceContext?.hostIDs ?? (app.selectedHostID ? [app.selectedHostID] : []));
  let events = $state<OperationEvent[]>([]);
  let busy = $state(false);
  let error = $state("");
  let note = $state("");
  let beforeID = $state("");
  let differences = $state<FleetDifference[] | null>(null);
  let integrations = $state<Versioned<Integration>[]>([]);
  let integrationID = $state("");
  let query = $state("");
  let minutes = $state(15);
  let queryResult = $state("");
  const incident = $derived(incidents.find((i) => i.id === selectedID));
  async function refresh() { incidents = (await PlatformService.ListRecords('incident')).map((r) => unpack<Incident>(r)); }
  async function loadIntegrations() { integrations = (await PlatformService.ListRecords('integration')).map((r) => unpack<Integration>(r)).filter((i) => ['prometheus','loki'].includes(i.kind)); }
  async function load() { differences = null; try { events = selectedID ? await PlatformService.Timeline(selectedID,0) : []; } catch (e) { error = String(e); } }
  onMount(() => { void Promise.all([refresh(),loadIntegrations()]).catch((e) => error = String(e)); });
  async function capture() { busy = true; error = ''; try { const saved = await PlatformService.CaptureIncident(name,app.serviceContext?.id ?? '',hosts); selectedID = saved.id; await refresh(); await load(); name = ''; } catch (e) { error = String(e); } finally { busy = false; } }
  async function annotate() { try { await PlatformService.AddIncidentEvidence(selectedID,'note','Investigation note',note); note = ''; await load(); } catch (e) { error = String(e); } }
  async function resolve() { if (!incident) return; try { await PlatformService.SetIncidentStatus(incident.id,incident.status === 'open' ? 'resolved' : 'open',incident.revision); await refresh(); } catch (e) { error = String(e); } }
  async function exportReport(format: string) { try { downloadText(`incident-${selectedID}.${format === 'json' ? 'json' : 'md'}`,await PlatformService.ExportIncident(selectedID,format)); } catch (e) { error = String(e); } }
  async function compare() { try { differences = await PlatformService.CompareIncidents(beforeID,selectedID); } catch (e) { error = String(e); } }
  async function runQuery() { busy = true; error = ''; try { const end = Date.now(); queryResult = await PlatformService.QueryObservability(integrationID,query,end-minutes*60000,end,15); } catch (e) { error = String(e); } finally { busy = false; } }
  async function attach() { try { const integration = integrations.find((i) => i.id === integrationID); await PlatformService.AddIncidentEvidence(selectedID,integration?.kind === 'loki' ? 'logs' : 'metrics',query,queryResult); await load(); } catch (e) { error = String(e); } }
</script>
<div class="flex h-full flex-col"><PageHeader icon={Activity} title="Incidents" subtitle="Preserve evidence, compare changes and record the path to recovery" />
  <div class="ops-body ops-form">
    {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
    <details class="ops-card" open={!selectedID}><summary>Capture incident evidence</summary><div class="ops-form mt-3"><label>Incident name<input bind:value={name} placeholder="Payments latency increase" /></label><HostSelection bind:selected={hosts} /><div><button class="primary" onclick={capture} disabled={busy || !name || !hosts.length}>{busy ? 'Collecting evidence…' : 'Capture hosts, metrics and logs'}</button></div></div></details>
    <div class="ops-actions"><select aria-label="Saved incident" bind:value={selectedID} onchange={load}><option value="">Choose an incident</option>{#each incidents as item}<option value={item.id}>{item.name} · {item.status}</option>{/each}</select>{#if incident}<button onclick={resolve}>{incident.status === 'open' ? 'Resolve' : 'Reopen'}</button><button onclick={() => exportReport('markdown')}>Export report</button><button onclick={() => exportReport('json')}>Export JSON</button>{/if}</div>
    {#if incident}
      <p class="ops-muted">Captured {new Date(incident.createdAt).toLocaleString()} · {incident.hostIDs.length} hosts · {incident.recordingIDs.length} related recordings</p>
      <div class="ops-grid">
        {#each incident.snapshots as snapshot}
          {@const latest = incident.metrics[snapshot.hostID]?.at(-1)}
          <details class="ops-card">
            <summary>{snapshot.hostName || snapshot.hostID}</summary>
            {#if snapshot.error}<p class="ops-error">{snapshot.error}</p>{/if}
            {#each snapshot.sections as section}
              <details class="mt-2"><summary>{section.title}</summary><pre class="ops-code">{section.output}{section.error}</pre></details>
            {/each}
            {#if incident.logs[snapshot.hostID]}
              <details><summary>Logs</summary><pre class="ops-code">{incident.logs[snapshot.hostID]}</pre></details>
            {/if}
            {#if latest}
              <p class="ops-muted mt-2">CPU {latest.cpuPercent.toFixed(1)}% · Memory {latest.memPercent.toFixed(1)}% · Disk {latest.diskPercent.toFixed(1)}%</p>
            {/if}
          </details>
        {/each}
      </div>
      <div class="ops-actions"><select aria-label="Compare with incident" bind:value={beforeID}><option value="">Compare with earlier capture</option>{#each incidents.filter((i) => i.id !== selectedID) as item}<option value={item.id}>{item.name}</option>{/each}</select><button onclick={compare} disabled={!beforeID}>Compare evidence</button>{#if incident.recordingIDs.length}<button onclick={() => app.view = 'recordings'}>Open recordings</button>{/if}</div>
      {#if differences !== null}{#if !differences.length}<p class="ops-success">No differences in captured diagnostics and logs.</p>{/if}{#each differences as d}<details class="ops-card"><summary>{d.section}</summary><div class="ops-grid"><pre class="ops-code">{d.before}</pre><pre class="ops-code">{d.after}</pre></div></details>{/each}{/if}
      <label>Add an investigation note<textarea rows="3" bind:value={note}></textarea></label><div><button onclick={annotate} disabled={!note.trim()}>Add to timeline</button></div>
      <h3 class="type-body">Timeline</h3>
      {#each [...incident.changes,...events].sort((a,b) => a.at-b.at) as event (`${event.scope}:${event.seq}`)}<details class="ops-card"><summary>{new Date(event.at).toLocaleTimeString()} · {event.title}</summary><pre class="ops-code">{event.body}</pre></details>{/each}
    {/if}
    <details class="ops-card"><summary>Query historical metrics and logs</summary><div class="ops-form mt-3"><div class="ops-grid"><label>Integration<select bind:value={integrationID}><option value="">Choose source</option>{#each integrations as i}<option value={i.id}>{i.name} · {i.kind}</option>{/each}</select></label><label>Last minutes<input type="number" min="1" max="20160" bind:value={minutes} /></label></div><label>PromQL or LogQL<textarea rows="2" bind:value={query} placeholder="up"></textarea></label><div class="ops-actions"><button onclick={runQuery} disabled={busy || !integrationID || !query}>Run query</button><button onclick={attach} disabled={!selectedID || !queryResult}>Attach result to incident</button></div>{#if queryResult}<pre class="ops-code">{queryResult}</pre>{/if}</div></details>
    <IntegrationsPanel onSaved={() => void loadIntegrations()} />
  </div>
</div>
