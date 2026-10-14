<script lang="ts">
  import { onMount } from "svelte";
  import { PlatformService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { FleetSnapshot, FleetDifference, DynamicGroup } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import type { Host } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { unpack, type Versioned } from "./operationRecords";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import HostSelection from "./HostSelection.svelte";
  import CloudImport from "./CloudImport.svelte";
  import { Server } from "@lucide/svelte";
  let query = $state("");
  let groupName = $state("");
  let groups = $state<Versioned<DynamicGroup>[]>([]);
  let matches = $state<Host[]>([]);
  let hosts = $state<string[]>(app.selectedHostID ? [app.selectedHostID] : []);
  let files = $state("");
  let snapshots = $state<Versioned<FleetSnapshot>[]>([]);
  let baselines = $state<Versioned<FleetSnapshot>[]>([]);
  let snapshotID = $state("");
  let baselineID = $state("");
  let baselineName = $state("");
  let differences = $state<FleetDifference[] | null>(null);
  let error = $state("");
  let busy = $state(false);
  let progress = $state("");
  let cloudOpen = $state(false);
  async function refresh() { const [g,s,b] = await Promise.all(['dynamic-group','fleet-snapshot','fleet-baseline'].map((kind) => PlatformService.ListRecords(kind))); groups = g.map((r) => unpack<DynamicGroup>(r)); snapshots = s.map((r) => unpack<FleetSnapshot>(r)); baselines = b.map((r) => unpack<FleetSnapshot>(r)); }
  onMount(() => { void refresh().catch((e) => error = String(e)); });
  async function search() { error = ''; try { matches = await PlatformService.QueryFleet(query); hosts = matches.map((h) => h.id); } catch (e) { error = String(e); } }
  async function saveGroup() { try { await PlatformService.SaveDynamicGroup({id:'',name:groupName,query},0); await refresh(); groupName = ''; } catch (e) { error = String(e); } }
  async function capture() {
    busy = true; error = ''; differences = null;
    try { for (let i=0; i<hosts.length; i++) { progress = `Capturing ${i+1} / ${hosts.length}`; const result = await PlatformService.CaptureFleet(hosts[i],files.split('\n').map((p) => p.trim()).filter(Boolean)); snapshotID = result.id; } await refresh(); }
    catch (e) { error = String(e); await refresh(); } finally { busy = false; progress = ''; }
  }
  async function baseline() { try { await PlatformService.SaveFleetBaseline(snapshotID,baselineName); await refresh(); baselineName = ''; } catch (e) { error = String(e); } }
  async function compare() { try { differences = await PlatformService.CompareFleet(baselineID,snapshotID); } catch (e) { error = String(e); } }
</script>
<div class="flex h-full flex-col"><PageHeader icon={Server} title="Fleet inventory" subtitle="Find hosts, capture configuration and compare machines against a baseline" />
  <div class="ops-body ops-form">
    {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
    <div class="ops-actions"><input class="flex-1" aria-label="Fleet query" bind:value={query} placeholder="env=production tag=api" onkeydown={(e) => { if (e.key === 'Enter') void search(); }} /><button onclick={search}>Find hosts</button><button onclick={() => cloudOpen = true}>Cloud inventory</button></div>
    <p class="ops-muted">Combine env, group, tag, name, os and protocol filters. Use != to exclude a value.</p>
    <div class="ops-actions"><select aria-label="Saved dynamic group" onchange={(e) => { query = groups.find((g) => g.id === e.currentTarget.value)?.query ?? ''; void search(); }}><option value="">Dynamic groups</option>{#each groups as g}<option value={g.id}>{g.name}</option>{/each}</select><input aria-label="Dynamic group name" bind:value={groupName} placeholder="Save query as…" /><button onclick={saveGroup} disabled={!groupName || !query}>Save group</button><span class="ops-muted">{hosts.length} hosts selected</span></div>
    <HostSelection bind:selected={hosts} />
    <label>Configuration files to compare (one absolute path per line)<textarea rows="3" bind:value={files} placeholder="/etc/nginx/nginx.conf"></textarea></label>
    <div class="ops-actions"><button class="primary" onclick={capture} disabled={busy || !hosts.length}>{progress || 'Capture fleet state'}</button><span class="ops-muted">Packages, services, container images and selected files.</span></div>
    <div class="ops-grid"><label>Snapshot<select bind:value={snapshotID}>{#each snapshots as snapshot}<option value={snapshot.id}>{snapshot.hostName} · {new Date(snapshot.capturedAt).toLocaleString()}</option>{/each}</select></label><label>Baseline<select bind:value={baselineID} onchange={() => { const baseline = baselines.find((b) => b.id === baselineID); if (baseline) files = baseline.files.map((f) => f.path).join('\n'); }}><option value="">Choose baseline</option>{#each baselines as baseline}<option value={baseline.id}>{baseline.name}</option>{/each}</select></label></div>
    <div class="ops-actions"><button onclick={compare} disabled={!snapshotID || !baselineID}>Compare state</button><input aria-label="Baseline name" bind:value={baselineName} placeholder="New baseline name" /><button onclick={baseline} disabled={!snapshotID || !baselineName}>Use snapshot as baseline</button></div>
    {#if differences !== null}{#if !differences.length}<p class="ops-success">No differences in the captured sections.</p>{/if}{#each differences as difference}<details class="ops-card" open><summary>{difference.section} · +{difference.added?.length ?? 0} / −{difference.removed?.length ?? 0}</summary>{#if difference.error}<p class="ops-warning">{difference.error}</p>{/if}<div class="ops-grid mt-2"><div><p class="ops-muted">Baseline</p><pre class="ops-code">{difference.before}</pre></div><div><p class="ops-muted">Current</p><pre class="ops-code">{difference.after}</pre></div></div></details>{/each}{/if}
  </div>
</div>
{#if cloudOpen}<CloudImport onclose={() => { cloudOpen = false; void app.refreshHosts(); }} />{/if}
