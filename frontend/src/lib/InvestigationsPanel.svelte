<script lang="ts">
  import { onMount } from "svelte";
  import { PlatformService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { ChangePlan } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { unpack, type Versioned, type Investigation, type Incident } from "./operationRecords";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import HostSelection from "./HostSelection.svelte";
  import ChangeReview from "./ChangeReview.svelte";
  import { Sparkles } from "@lucide/svelte";
  let question = $state('');
  let selection = $state('');
  let hosts = $state<string[]>(app.serviceContext?.hostIDs ?? (app.selectedHostID ? [app.selectedHostID] : []));
  let metrics = $state(true);
  let diagnostics = $state(false);
  let incidentID = $state('');
  let incidents = $state<Versioned<Incident>[]>([]);
  let investigations = $state<Versioned<Investigation>[]>([]);
  let investigation = $state<Versioned<Investigation> | null>(null);
  let plan = $state<ChangePlan | null>(null);
  let busy = $state(false);
  let error = $state('');
  async function refresh() { investigations = (await PlatformService.ListRecords('investigation')).map((r) => unpack<Investigation>(r)); incidents = (await PlatformService.ListRecords('incident')).map((r) => unpack<Incident>(r)); }
  onMount(() => { void refresh().catch((e) => error = String(e)); });
  async function collect() { busy = true; error = ''; try { investigation = unpack<Investigation>(await PlatformService.CollectEvidence({ question, selection, hostIDs:hosts, incidentID, metrics, diagnostics, workspaceID:app.serviceContext?.id ?? '' })); await refresh(); } catch (e) { error = String(e); } finally { busy = false; } }
  async function analyze() { if (!investigation) return; busy = true; error = ''; try { investigation = unpack<Investigation>(await PlatformService.AnalyzeEvidence(investigation.id)); await refresh(); } catch (e) { error = String(e); } finally { busy = false; } }
  async function preview(index: number) { if (!investigation) return; try { plan = await PlatformService.PreviewRemediation(investigation.id,index); } catch (e) { error = String(e); } }
</script>
<div class="flex h-full flex-col"><PageHeader icon={Sparkles} title="AI investigations" subtitle="Review selected evidence, investigate findings and preview proposed commands" />
  <div class="ops-body ops-form">
    {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
    <select aria-label="Saved investigation" value={investigation?.id ?? ''} onchange={(e) => investigation = investigations.find((i) => i.id === e.currentTarget.value) ?? null}><option value="">New investigation</option>{#each investigations as item}<option value={item.id}>{item.request.question.slice(0,100)}</option>{/each}</select>
    <label>What are you investigating?<textarea rows="2" bind:value={question} placeholder="Why did API response times increase after the last deployment?"></textarea></label>
    <HostSelection bind:selected={hosts} />
    <div class="ops-actions"><label class="!flex-row items-center"><input type="checkbox" bind:checked={metrics} />Recent metrics</label><label class="!flex-row items-center"><input type="checkbox" bind:checked={diagnostics} />Collect host diagnostics</label><select aria-label="Incident evidence" bind:value={incidentID}><option value="">No saved incident</option>{#each incidents as incident}<option value={incident.id}>{incident.name}</option>{/each}</select></div>
    <label>Selected terminal output or logs<textarea rows="5" bind:value={selection} placeholder="Paste the output you want to include"></textarea></label>
    <div><button class="primary" onclick={collect} disabled={busy || !question}>{busy ? 'Working…' : 'Collect & review evidence'}</button></div>
    {#if investigation}
      <h3 class="type-body">Evidence for: {investigation.request.question}</h3><p class="ops-muted">Recognized credentials are redacted. Review these excerpts before sending them to the model configured in Settings.</p>
      {#each investigation.evidence as evidence (evidence.id)}<details class="ops-card"><summary>{evidence.id} · {evidence.source} · {app.hosts.find((h) => h.id === evidence.hostID)?.name ?? ''}</summary><pre class="ops-code">{evidence.content}</pre></details>{/each}
      <div><button class="primary" onclick={analyze} disabled={busy}>{busy ? 'Analyzing…' : 'Send reviewed evidence for analysis'}</button></div>
      {#if investigation.analysis}<article class="ops-card ops-form"><h3 class="type-body">Findings</h3><p>{investigation.analysis.summary}</p>{#each investigation.analysis.findings as finding}<p>{finding.explanation} <span class="ops-muted">[{finding.evidenceIDs.join(', ')}]</span></p>{/each}</article>
      {#each investigation.analysis.actions as action,index}<article class="ops-card ops-form"><p>{action.reason} <span class="ops-muted">[{action.evidenceIDs.join(', ')}]</span></p><p>Target: {app.hosts.find((h) => h.id === action.hostID)?.name ?? action.hostID}</p><pre class="ops-code">{action.command}</pre><div><button onclick={() => preview(index)}>Review command</button></div></article>{/each}{/if}
    {/if}
  </div>
</div>
{#if plan}<ChangeReview {plan} onClose={() => plan = null} />{/if}
