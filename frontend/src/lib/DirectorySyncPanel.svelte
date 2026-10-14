<script lang="ts">
  import { onMount } from "svelte";
  import { InfrastructureService, PlatformService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { DirectorySyncProfile, DirectorySyncPlan, SyncOutcome } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { unpack, type Versioned } from "./operationRecords";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import Dialog from "./Dialog.svelte";
  import { FolderSync } from "@lucide/svelte";
  const blank = (): DirectorySyncProfile => ({ id: '', name: '', hostID: app.selectedHostID ?? '', localPath: '', remotePath: '', direction: 'both', excludes: ['.git','node_modules'], mirror: false });
  let profiles = $state<Versioned<DirectorySyncProfile>[]>([]);
  let draft = $state(blank()); let revision = $state(0); let excludes = $state('.git\nnode_modules');
  let plan = $state<DirectorySyncPlan | null>(null); let choices = $state<Record<string,string>>({});
  let results = $state<SyncOutcome[]>([]); let error = $state(''); let busy = $state(false); let phrase = $state('');
  const production = $derived(app.hosts.find(h => h.id === plan?.profile.hostID)?.environment === 'production');
  const unresolved = $derived(Object.values(choices).some(v => v === 'conflict'));
  async function refresh() { profiles = (await PlatformService.ListRecords('sync-profile')).map(r => unpack<DirectorySyncProfile>(r)); }
  function select(id: string) { const p = profiles.find(p => p.id === id); draft = p ? { ...p } : blank(); revision = p?.revision ?? 0; excludes = draft.excludes.join('\n'); results = []; }
  async function action(fn: () => Promise<unknown>) { busy = true; error = ''; try { await fn(); } catch(e) { error = String(e); } finally { busy = false; } }
  async function save() { await action(async () => { const doc = await InfrastructureService.SaveSyncProfile({ ...draft, excludes: excludes.split('\n').map(s => s.trim()).filter(Boolean), mirror: draft.direction === 'both' ? false : draft.mirror }, revision); await refresh(); select(doc.id); }); }
  async function compare() { await action(async () => { results = []; plan = await InfrastructureService.PreviewDirectorySync(draft.id); choices = Object.fromEntries(plan.entries.map(e => [e.path,e.action])); phrase = ''; }); }
  async function apply() { if (!plan || unresolved || (production && phrase !== 'production')) return; const id = plan.id; await action(async () => { try { results = await InfrastructureService.ApplyDirectorySync(id, Object.entries(choices).map(([path,action]) => ({path,action}))); } finally { plan = null; } }); }
  onMount(() => { void action(refresh); });
</script>
<div class="flex h-full min-h-0 flex-col"><PageHeader icon={FolderSync} title="Directory sync" subtitle="Compare folders, resolve differences and apply a reviewed file set" /><div class="ops-body ops-form">
  {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
  <label>Saved profile<select value={draft.id} disabled={busy} onchange={e => select(e.currentTarget.value)}><option value="">New profile</option>{#each profiles as p (p.id)}<option value={p.id}>{p.name}</option>{/each}</select></label>
  <form class="ops-form" onsubmit={e => {e.preventDefault();void save();}}><div class="ops-grid"><label>Name<input required bind:value={draft.name} /></label><label>SSH host<select required bind:value={draft.hostID}><option value="">Choose host</option>{#each app.hosts.filter(h => !h.protocol || h.protocol === 'ssh') as h (h.id)}<option value={h.id}>{h.name} · {h.host}</option>{/each}</select></label><label>Local directory<input required bind:value={draft.localPath} placeholder="/home/user/project" /></label><label>Remote directory<input required bind:value={draft.remotePath} placeholder="/srv/project" /></label><label>Direction<select bind:value={draft.direction}><option value="both">Both directions</option><option value="upload">Local → remote</option><option value="download">Remote → local</option></select></label><label>Exclude patterns (one per line)<textarea rows="3" bind:value={excludes}></textarea></label></div>
  {#if draft.direction !== 'both'}<label class="ops-check"><input type="checkbox" bind:checked={draft.mirror} />Mirror: include destination-only files for deletion in the review</label>{/if}
  <p class="ops-muted">Both directories must exist. Comparisons hash file contents. Symbolic links must be excluded. Two-way sync keeps missing files and asks you to resolve files changed on both sides.</p>
  <div class="ops-actions"><button class="primary" type="submit" disabled={busy}>Save profile</button><button type="button" disabled={busy || !draft.id} onclick={compare}>{busy ? 'Working…' : 'Compare saved profile'}</button>{#if draft.id}<button type="button" disabled={busy} onclick={() => action(async () => { await PlatformService.DeleteRecord('sync-profile',draft.id,revision); await refresh(); select(''); })}>Delete profile</button>{/if}</div></form>
  {#if results.length}<p class="ops-success">Applied {results.length} file changes.</p>{#each results as result}<p>{result.action} · {result.path}{#if result.error}<span class="ops-error"> {result.error}</span>{/if}</p>{/each}{/if}
</div></div>
{#if plan}<Dialog portal label="Review directory synchronization" onclose={() => { if (!busy) plan = null; }} panelClass="ops-form w-[min(95vw,1000px)] overflow-auto rounded-xl border hairline surface-2 p-5">
  <h2>Review {plan.profile.name}</h2><p>{plan.profile.localPath} ↔ {app.hosts.find(h => h.id === plan?.profile.hostID)?.name}: {plan.profile.remotePath}</p>
  <div class="max-h-[50vh] overflow-auto"><table class="ops-table"><thead><tr><th>File</th><th>Local hash</th><th>Remote hash</th><th>Action</th></tr></thead><tbody>{#each plan.entries as entry (entry.path)}<tr><td>{entry.path}</td><td>{entry.localHash.slice(0,10) || 'Missing'}</td><td>{entry.remoteHash.slice(0,10) || 'Missing'}</td><td><select aria-label={`Action for ${entry.path}`} bind:value={choices[entry.path]} disabled={busy}><option value="skip">Skip</option>{#if entry.action === 'conflict'}<option value="conflict">Choose a version</option><option value="upload">Use local</option><option value="download">Use remote</option>{:else}<option value={entry.action}>{entry.action}</option>{/if}</select></td></tr>{:else}<tr><td colspan="4">Folders match.</td></tr>{/each}</tbody></table></div>
  {#if production}<label>Type production to apply<input bind:value={phrase} /></label>{/if}
  <p class="ops-muted">Only selected changes are applied. Each file is replaced atomically; a failure can leave earlier files completed. Compare again after a failure.</p>
  <div class="ops-actions"><button disabled={busy} onclick={() => plan = null}>Cancel</button><button class="primary" disabled={busy || unresolved || (production && phrase !== 'production') || !plan.entries.length} onclick={apply}>{busy ? 'Synchronizing…' : 'Apply selected changes'}</button></div>
</Dialog>{/if}
