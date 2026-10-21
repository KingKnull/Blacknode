<script lang="ts">
  import { InfrastructureService, PlatformService } from '../../bindings/github.com/blacknode/blacknode/internal/service';
  import type { ScanProfile, VulnerabilityScan, ScanComparison } from '../../bindings/github.com/blacknode/blacknode/internal/service/models';
  import { downloadText, type Versioned } from './operationRecords';
  import { app } from './state.svelte';
  import { useOperationTarget } from './operationSelection.svelte';
  import { selectableOperationRecords } from './operationTargets';
  import PageHeader from './PageHeader.svelte';
  import ToolAvailability from './ToolAvailability.svelte';
  import { ShieldCheck } from '@lucide/svelte';
  const blank = (): ScanProfile => ({id:'',name:'',hostID:app.selectedHostID ?? '',kind:'image',target:''});
  let profiles = $state<Versioned<ScanProfile>[]>([]); let scans = $state<Versioned<VulnerabilityScan>[]>([]); let draft = $state(blank()); let revision = $state(0);
  let selected = $state(''); let before = $state(''); let severity = $state(''); let query = $state(''); let error = $state(''); let busy = $state(false); let comparison = $state<ScanComparison | null>(null);
  let scanSelection = 0;
  const current = $derived(scans.find(s => s.id === selected));
  const findings = $derived((current?.findings ?? []).filter(f => (!severity || f.severity === severity) && `${f.id} ${f.package} ${f.target} ${f.title}`.toLowerCase().includes(query.toLowerCase())));
  async function fetchRecords() { return Promise.all([PlatformService.ListRecords('scan-profile'),PlatformService.ListRecords('vulnerability-scan')]); }
  function setRecords([p,s]: Awaited<ReturnType<typeof fetchRecords>>) { profiles = selectableOperationRecords<ScanProfile>(p); scans = selectableOperationRecords<VulnerabilityScan>(s); }
  async function action(fn: (isCurrent: () => boolean) => Promise<unknown>) { const selection = scanSelection; const isCurrent = () => selection === scanSelection; busy = true; error = ''; try { await fn(isCurrent); } catch(e) { if (isCurrent()) error = String(e); } finally { if (isCurrent()) busy = false; } }
  async function saveProfile() { await action(async isCurrent => { const saved = await InfrastructureService.SaveScanProfile(draft,revision); const records = await fetchRecords(); if (!isCurrent()) return; setRecords(records); choose(saved.id); }); }
  async function runScan() { await action(async isCurrent => { const scan = await InfrastructureService.RunSecurityScan(draft.id); const records = await fetchRecords(); if (!isCurrent()) return; setRecords(records); selected = scan.id; comparison = null; }); }
  async function deleteProfile() { await action(async isCurrent => { await PlatformService.DeleteRecord('scan-profile',draft.id,revision); const records = await fetchRecords(); if (!isCurrent()) return; setRecords(records); choose(''); }); }
  async function compareScans() { if (!current) return; const id = current.id; await action(async isCurrent => { const result = await InfrastructureService.CompareSecurityScans(before,id); if (isCurrent() && selected === id) comparison = result; }); }
  function choose(id: string) { const p = profiles.find(p => p.id === id); draft = p ? {...p} : blank(); revision = p?.revision ?? 0; }
  useOperationTarget('security-scans', {
    selection: () => `${draft.id}:${selected}`,
    load: fetchRecords,
    clear: () => { scanSelection++; choose(''); selected = ''; before = ''; comparison = null; query = ''; severity = ''; error = ''; busy = false; },
    apply: (records, target) => {
      setRecords(records);
      if (!target) return;
      if (target.kind === 'scan-profile') {
        if (!profiles.some(p => p.id === target.id)) throw new Error('This scan profile is no longer available.');
        choose(target.id);
      } else {
        if (!scans.some(s => s.id === target.id)) throw new Error('This security scan is no longer available.');
        selected = target.id;
      }
    },
    error: message => error = message,
  });
</script>
<div class="flex h-full min-h-0 flex-col"><PageHeader icon={ShieldCheck} title="Security scans" subtitle="Vulnerabilities, configuration findings and secret locations" /><div class="ops-body ops-form">
  {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
  <ToolAvailability hostID={draft.hostID} tools={['trivy','sh']} />
  <p class="ops-muted">Install Trivy on the selected machine. Scans can download vulnerability databases. Secret findings retain locations and rule names; matched secret values are discarded.</p>
  <label>Scan profile<select value={draft.id} onchange={e => { scanSelection++; busy = false; choose(e.currentTarget.value); }}><option value="">New profile</option>{#each profiles as p (p.id)}<option value={p.id}>{p.name}</option>{/each}</select></label>
  <div class="ops-grid"><label>Name<input bind:value={draft.name} /></label><label>Machine<select bind:value={draft.hostID}><option value="">Local machine</option>{#each app.hosts.filter(h => !h.protocol || h.protocol === 'ssh') as h (h.id)}<option value={h.id}>{h.name} · {h.host}</option>{/each}</select></label><label>Scan type<select bind:value={draft.kind}><option value="image">Container image</option><option value="fs">Project filesystem</option><option value="rootfs">Host root filesystem</option></select></label><label>Target<input bind:value={draft.target} placeholder={draft.kind === 'image' ? 'nginx:1.27' : '/srv/app'} /></label></div>
  <div class="ops-actions"><button disabled={busy} onclick={saveProfile}>Save profile</button><button class="primary" disabled={busy || !draft.id} onclick={runScan}>{busy ? 'Working…' : 'Run saved scan'}</button>{#if draft.id}<button disabled={busy} onclick={deleteProfile}>Delete profile</button>{/if}</div>
  <label>Scan history<select bind:value={selected} onchange={() => { scanSelection++; busy = false; comparison = null; }}><option value="">Choose scan</option>{#each scans as s (s.id)}<option value={s.id}>{s.profile.name} · {new Date(s.at).toLocaleString()}</option>{/each}</select></label>
  {#if current}<p>{current.findings.length} findings · {current.findings.filter(f => f.severity === 'CRITICAL').length} critical · {current.findings.filter(f => f.fixed).length} with a fix version</p><div class="ops-actions"><input aria-label="Search findings" bind:value={query} placeholder="Package, CVE, file or title" /><select aria-label="Severity" bind:value={severity}><option value="">All severities</option>{#each ['CRITICAL','HIGH','MEDIUM','LOW','UNKNOWN'] as s}<option value={s}>{s}</option>{/each}</select><button onclick={() => downloadText(`scan-${current.id}.json`,JSON.stringify(current,null,2),'application/json')}>Export report</button><select aria-label="Previous scan" bind:value={before}><option value="">Compare with scan</option>{#each scans.filter(s => s.id !== current.id && s.profile.hostID === current.profile.hostID && s.profile.kind === current.profile.kind && s.profile.target === current.profile.target) as s (s.id)}<option value={s.id}>{new Date(s.at).toLocaleString()}</option>{/each}</select><button disabled={!before || busy} onclick={compareScans}>Compare</button></div>
    {#if comparison}<p class="ops-card">{comparison.added.length} new · {comparison.resolved.length} resolved · {comparison.remaining} remaining</p><details><summary>Resolved findings</summary>{#each comparison.resolved as f (f.key)}<p>{f.id} · {f.package || f.target}</p>{/each}</details>{/if}
    <div class="overflow-auto"><table class="ops-table"><thead><tr><th>Severity</th><th>Finding</th><th>Package / location</th><th>Installed</th><th>Fixed in</th></tr></thead><tbody>{#each findings as f (f.key)}<tr><td>{f.severity}</td><td>{#if /^https?:\/\//.test(f.url)}<a href={f.url} target="_blank" rel="noreferrer">{f.id}</a>{:else}{f.id}{/if}<p class="ops-muted">{f.title}</p></td><td>{f.package}<p class="ops-muted break-all">{f.target}</p></td><td>{f.installed || '—'}</td><td>{f.fixed || '—'}</td></tr>{:else}<tr><td colspan="5">No matching findings.</td></tr>{/each}</tbody></table></div>
  {/if}
</div></div>
