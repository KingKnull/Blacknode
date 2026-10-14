<script lang="ts">
  import { onMount } from 'svelte';
  import { SSMService, PlatformService } from '../../bindings/github.com/blacknode/blacknode/internal/service';
  import type { SSMProfile, SSMAvailability } from '../../bindings/github.com/blacknode/blacknode/internal/service/models';
  import { unpack, type Versioned } from './operationRecords';
  import PageHeader from './PageHeader.svelte';
  import InfrastructureTerminal from './InfrastructureTerminal.svelte';
  import ConfirmDanger from './ConfirmDanger.svelte';
  import { Cloud } from '@lucide/svelte';
  const blank = (): SSMProfile => ({id:'',name:'',target:'',region:'us-east-1',awsProfile:'',environment:'dev'});
  let profiles = $state<Versioned<SSMProfile>[]>([]);let draft = $state(blank());let revision = $state(0);let available = $state<SSMAvailability>({aws:false,plugin:false});let identity = $state('');let error = $state('');let busy = $state(false);let confirm = $state(false);let connection = $state<{id:string;profileID:string} | null>(null);
  async function refresh(){const [p,a] = await Promise.all([SSMService.Profiles(),SSMService.Available()]);profiles = p.map(r => unpack<SSMProfile>(r));available = a;}
  async function action(fn:()=>Promise<unknown>){busy = true;error = '';try{await fn();}catch(e){error = String(e);}finally{busy = false;}}
  function select(id:string){const p = profiles.find(p => p.id === id);draft = p ? {...p} : blank();revision = p?.revision ?? 0;identity = '';}
  function connect(confirmed = false){const profile = profiles.find(p => p.id === draft.id);if(!profile)return;if(profile.environment === 'production' && !confirmed){confirm = true;return;}confirm = false;connection = {id:`ssm-${crypto.randomUUID()}`,profileID:profile.id};}
  onMount(() => {void action(refresh);});
</script>
<div class="flex h-full min-h-0 flex-col"><PageHeader icon={Cloud} title="AWS Session Manager" subtitle="Interactive AWS sessions using your existing AWS sign-in" /><div class="ops-body ops-form">
  {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
  <p class="ops-muted">AWS CLI: {available.aws ? 'available' : 'missing'} · Session Manager plugin: {available.plugin ? 'available' : 'missing'}. Instances must be configured for SSM and your IAM identity must allow StartSession. AWS credentials stay in the AWS provider chain.</p>
  <label>Session profile<select value={draft.id} disabled={!!connection || busy} onchange={e => select(e.currentTarget.value)}><option value="">New profile</option>{#each profiles as p (p.id)}<option value={p.id}>{p.name} · {p.target}</option>{/each}</select></label>
  <fieldset class="ops-grid" disabled={!!connection || busy}><label>Name<input bind:value={draft.name} /></label><label>Instance / managed-node ID<input bind:value={draft.target} placeholder="i-0123456789abcdef0" /></label><label>AWS region<input bind:value={draft.region} /></label><label>AWS profile<input bind:value={draft.awsProfile} placeholder="Default provider chain" /></label><label>Environment<select bind:value={draft.environment}><option value="dev">Development</option><option value="staging">Staging</option><option value="production">Production</option></select></label></fieldset>
  <div class="ops-actions"><button disabled={busy || !!connection} onclick={() => action(async () => {const d = await SSMService.Save(draft,revision);await refresh();select(d.id);})}>Save profile</button><button disabled={busy || !draft.id || !!connection} onclick={() => action(async () => identity = await SSMService.Identity(draft.id))}>Check AWS identity</button><button class="primary" disabled={busy || !draft.id || !!connection || !available.aws || !available.plugin} onclick={() => connect()}>Connect saved profile</button>{#if connection}<button onclick={() => connection = null}>Disconnect</button>{:else if draft.id}<button disabled={busy} onclick={() => action(async () => {await PlatformService.DeleteRecord('ssm-profile',draft.id,revision);await refresh();select('');})}>Delete profile</button>{/if}</div>
  {#if identity}<pre class="ops-code">{identity}</pre>{/if}
  {#if connection}{@const c = connection}<InfrastructureTerminal sessionID={c.id} connect={(cols,rows) => SSMService.Connect(c.id,c.profileID,cols,rows)} write={data => SSMService.Write(c.id,data)} resize={(cols,rows) => SSMService.Resize(c.id,cols,rows)} disconnect={() => SSMService.Disconnect(c.id)} />{/if}
</div></div>
{#if confirm}<ConfirmDanger title="Connect to production" body={`Start an AWS session on ${profiles.find(p => p.id === draft.id)?.target}?`} severity="warn" productionHosts={[profiles.find(p => p.id === draft.id)?.name ?? 'Production instance']} onCancel={() => confirm = false} onConfirm={() => connect(true)} />{/if}
