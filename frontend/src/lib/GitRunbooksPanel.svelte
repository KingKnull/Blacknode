<script lang="ts">
  import { onMount } from 'svelte';
  import { InfrastructureService, PlatformService } from '../../bindings/github.com/blacknode/blacknode/internal/service';
  import type { GitRunbookRepository, GitRunbookState, ChangePlan } from '../../bindings/github.com/blacknode/blacknode/internal/service/models';
  import { unpack, type Versioned } from './operationRecords';
  import PageHeader from './PageHeader.svelte';
  import HostSelection from './HostSelection.svelte';
  import ChangeReview from './ChangeReview.svelte';
  import ToolAvailability from './ToolAvailability.svelte';
  import ConfirmDanger from './ConfirmDanger.svelte';
  import { GitBranch } from '@lucide/svelte';
  const blank = (): GitRunbookRepository => ({id:'',name:'',directory:'',file:'runbooks.yaml',remote:'origin',branch:'main'});
  let repositories = $state<Versioned<GitRunbookRepository>[]>([]); let draft = $state(blank()); let revision = $state(0);
  let ref = $state('HEAD'); let current = $state<GitRunbookState | null>(null); let bookID = $state(''); let hosts = $state<string[]>([]); let values = $state('{}'); let message = $state('Update Blacknode runbooks');
  let plan = $state<ChangePlan | null>(null); let publish = $state(false); let busy = $state(false); let error = $state(''); let result = $state('');
  async function refresh() { repositories = (await PlatformService.ListRecords('git-runbooks')).map(r => unpack<GitRunbookRepository>(r)); }
  function select(id: string) { const repo = repositories.find(r => r.id === id); draft = repo ? {...repo} : blank(); revision = repo?.revision ?? 0; current = null; bookID = ''; }
  async function action(fn: () => Promise<unknown>) { busy = true; error = ''; result = ''; try {await fn();}catch(e){error = String(e);}finally{busy = false;} }
  async function load() { await action(async () => { current = await InfrastructureService.GitRunbooks(draft.id,ref); bookID = current.books[0]?.id ?? ''; }); }
  function variables(): Record<string,string> { const parsed = JSON.parse(values); if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object' || Object.values(parsed).some(v => typeof v !== 'string')) throw new Error('Variables must be a JSON object with string values'); return parsed; }
  onMount(() => {void action(refresh);});
</script>
<div class="flex h-full min-h-0 flex-col"><PageHeader icon={GitBranch} title="Runbooks in Git" subtitle="Review definitions, preserve versions and run exact commits" /><div class="ops-body ops-form">
  {#if error}<p class="ops-error" role="alert">{error}</p>{/if}{#if result}<p class="ops-success">{result}</p>{/if}
  <ToolAvailability tools={['git']} />
  <p class="ops-muted">Use an existing local Git checkout. Fetch and push use your Git credentials. Export commits only the runbook YAML file; other working files stay untouched.</p>
  <label>Repository<select value={draft.id} onchange={e => select(e.currentTarget.value)}><option value="">New repository</option>{#each repositories as r (r.id)}<option value={r.id}>{r.name}</option>{/each}</select></label>
  <div class="ops-grid"><label>Name<input bind:value={draft.name} /></label><label>Repository root<input bind:value={draft.directory} placeholder="/home/user/operations" /></label><label>Runbook YAML path<input bind:value={draft.file} /></label><label>Remote<input bind:value={draft.remote} /></label><label>Publish branch<input bind:value={draft.branch} /></label></div>
  <div class="ops-actions"><button disabled={busy} onclick={() => action(async () => {const d = await InfrastructureService.SaveGitRepository(draft,revision);await refresh();select(d.id);})}>Save repository</button>{#if draft.id}<button disabled={busy} onclick={() => action(async () => {await PlatformService.DeleteRecord('git-runbooks',draft.id,revision);await refresh();select('');})}>Remove repository profile</button>{/if}</div>
  {#if draft.id}<div class="ops-card ops-form"><label>Commit message<input bind:value={message} /></label><div class="ops-actions"><button disabled={busy || !message} onclick={() => action(async () => plan = await InfrastructureService.PreviewGitExport(draft.id,message))}>Review export and commit</button><button disabled={busy} onclick={() => action(async () => {await InfrastructureService.FetchGitRunbooks(draft.id);result = 'Fetched configured branch.';})}>Fetch branch</button><button disabled={busy} onclick={() => publish = true}>Push configured branch</button></div></div>
    <div class="ops-actions"><label>Commit or ref<input bind:value={ref} placeholder="HEAD or commit hash" /></label><button disabled={busy} onclick={load}>Load committed runbooks</button></div>
  {/if}
  {#if current}<p class="ops-muted break-all">Pinned commit: {current.commit}</p><details><summary>Working tree status and diff</summary><pre class="ops-code">{current.status || 'Clean'}{'\n'}{current.diff}</pre></details><details><summary>Committed YAML</summary><pre class="ops-code">{current.yaml}</pre></details>
    <label>Runbook<select bind:value={bookID}>{#each current.books as b (b.id)}<option value={b.id}>{b.name}</option>{/each}</select></label>
    <div><button disabled={busy || !bookID} onclick={() => action(async () => plan = await InfrastructureService.PreviewGitImport(draft.id,current!.commit,bookID))}>Review import / restore</button></div>
    <HostSelection bind:selected={hosts} /><label>Variables (JSON string values)<textarea rows="3" bind:value={values}></textarea></label><div><button class="primary" disabled={busy || !bookID || !hosts.length} onclick={() => action(async () => plan = await InfrastructureService.PreviewGitExecution(draft.id,current!.commit,bookID,hosts,variables()))}>Review execution at this commit</button></div>
  {/if}
</div></div>
{#if plan}<ChangeReview {plan} onClose={() => plan = null} onComplete={outcome => {result = outcome.exitCode === 0 && !outcome.error ? 'Operation completed. Started jobs appear in Runbooks → execution history.' : '';}} />{/if}
{#if publish}<ConfirmDanger title="Push runbook branch" body={`Push the current commit to ${draft.remote}/${draft.branch} from ${draft.directory}?`} severity="warn" productionHosts={[]} onCancel={() => publish = false} onConfirm={async () => {publish = false;await action(async () => {await InfrastructureService.PublishGitRunbooks(draft.id);result = 'Branch pushed.';});}} />{/if}
