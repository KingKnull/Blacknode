<script lang="ts">
  import { InfrastructureService } from '../../bindings/github.com/blacknode/blacknode/internal/service';
  import type { KubeTarget, HelmRelease, HelmRevision, ChangePlan } from '../../bindings/github.com/blacknode/blacknode/internal/service/models';
  import { app } from './state.svelte';
  import PageHeader from './PageHeader.svelte';
  import ChangeReview from './ChangeReview.svelte';
  import { Boxes } from '@lucide/svelte';
  let hostID = $state(app.serviceContext?.kubeHostID ?? ''); let context = $state(app.serviceContext?.kubeContext ?? ''); let namespace = $state(app.serviceContext?.kubeNamespace ?? '');
  let releases = $state<HelmRelease[]>([]); let release = $state<HelmRelease | null>(null); let history = $state<HelmRevision[]>([]); let values = $state(''); let chart = $state(''); let version = $state(''); let rollback = $state(0); let plan = $state<ChangePlan | null>(null); let busy = $state(false); let error = $state('');
  const target = (): KubeTarget => ({hostID,context,namespace:release?.namespace ?? namespace,workspaceID:app.serviceContext?.id ?? ''});
  async function action(fn: () => Promise<unknown>) {busy = true;error = '';try{await fn();}catch(e){error = String(e);}finally{busy = false;}}
  async function load() {await action(async () => {release = null;history = [];releases = await InfrastructureService.HelmReleases(target());});}
  async function select(r: HelmRelease) {await action(async () => {release = r;const [v,h] = await Promise.all([InfrastructureService.HelmValues(target(),r.name),InfrastructureService.HelmHistory(target(),r.name)]);values = v;history = h;rollback = h.find(h => String(h.revision) !== r.revision)?.revision ?? 0;});}
  function clear() {releases = [];release = null;history = [];}
</script>
<div class="flex h-full min-h-0 flex-col"><PageHeader icon={Boxes} title="Helm releases" subtitle="Inspect chart values and review upgrades or rollbacks" /><div class="ops-body ops-form">
  {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
  <div class="ops-grid"><label>Run Helm on<select bind:value={hostID} disabled={busy} onchange={clear}><option value="">Local machine</option>{#each app.hosts.filter(h => !h.protocol || h.protocol === 'ssh') as h (h.id)}<option value={h.id}>{h.name} · {h.host}</option>{/each}</select></label><label>Kubernetes context<input bind:value={context} disabled={busy} oninput={clear} placeholder="Current context" /></label><label>Namespace<input bind:value={namespace} disabled={busy} oninput={clear} placeholder="All namespaces" /></label></div><div><button class="primary" disabled={busy} onclick={load}>{busy ? 'Loading…' : 'Load releases'}</button></div>
  <p class="ops-muted">Helm must be installed on the selected machine with access to the cluster. Upgrade review downloads a pinned chart version and runs a server dry run.</p>
  <div class="overflow-auto"><table class="ops-table"><thead><tr><th>Release</th><th>Namespace</th><th>Chart</th><th>Revision</th><th>Status</th></tr></thead><tbody>{#each releases as r (`${r.namespace}:${r.name}`)}<tr><td><button disabled={busy} onclick={() => select(r)}>{r.name}</button></td><td>{r.namespace}</td><td>{r.chart}</td><td>{r.revision}</td><td>{r.status}</td></tr>{:else}<tr><td colspan="5">Load releases from a cluster to inspect them.</td></tr>{/each}</tbody></table></div>
  {#if release}<div class="ops-card ops-form"><h3>{release.name} · {release.namespace}</h3><label>Values YAML<textarea class="font-mono" rows="12" bind:value={values}></textarea></label><div class="ops-grid"><label>Chart reference<input bind:value={chart} placeholder="bitnami/nginx or oci://registry/chart" /></label><label>Exact chart version<input bind:value={version} placeholder="1.2.3" /></label></div><div><button disabled={busy || !chart || !version} onclick={() => action(async () => plan = await InfrastructureService.PreviewHelmUpgrade(target(),release!.name,chart,version,values))}>Review upgrade</button></div><div class="ops-actions"><label>Rollback revision<select bind:value={rollback}><option value={0}>Choose revision</option>{#each history as h (h.revision)}<option value={h.revision}>{h.revision} · {h.chart} · {h.status}</option>{/each}</select></label><button disabled={busy || !rollback} onclick={() => action(async () => plan = await InfrastructureService.PreviewHelmRollback(target(),release!.name,rollback))}>Review rollback</button></div></div>{/if}
</div></div>
{#if plan}<ChangeReview {plan} onClose={() => plan = null} />{/if}
