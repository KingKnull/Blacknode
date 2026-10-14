<script lang="ts">
  import { onMount } from "svelte";
  import { PlatformService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { DeploymentProject, Integration, PipelineRun, ChangePlan } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { unpack, type Versioned } from "./operationRecords";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import ChangeReview from "./ChangeReview.svelte";
  import IntegrationsPanel from "./IntegrationsPanel.svelte";
  import { GitBranch } from "@lucide/svelte";
  const blank = (): Versioned<DeploymentProject> => ({ id:'', revision:0, name:'', hostID:app.selectedHostID ?? '', directory:'', tool:'tofu', workspaceID:app.serviceContext?.id ?? '', playbook:'site.yml', inventory:'inventory.ini', integrationID:'', repository:'' });
  let project = $state(blank());
  let projects = $state<Versioned<DeploymentProject>[]>([]);
  let integrations = $state<Versioned<Integration>[]>([]);
  let pipelines = $state<PipelineRun[]>([]);
  let plan = $state<ChangePlan | null>(null);
  let busy = $state(false);
  let error = $state('');
  async function refresh() { projects = (await PlatformService.ListRecords('project')).map((r) => unpack<DeploymentProject>(r)); integrations = (await PlatformService.ListRecords('integration')).map((r) => unpack<Integration>(r)).filter((i) => ['github','gitlab'].includes(i.kind)); }
  onMount(() => { void refresh().then(() => { const p = projects.find((p) => p.id === app.serviceContext?.projectID); if (p) project = structuredClone($state.snapshot(p)); }).catch((e) => error = String(e)); });
  async function save() { try { const {revision,...value} = $state.snapshot(project); project = unpack<DeploymentProject>(await PlatformService.SaveProject(value,revision)); await refresh(); } catch (e) { error = String(e); } }
  async function preview(init = false) { busy = true; error = ''; try { plan = init ? await PlatformService.PreviewProjectInit(project.id) : await PlatformService.PreviewDeployment(project.id); } catch (e) { error = String(e); } finally { busy = false; } }
  async function loadPipelines() { busy = true; error = ''; try { pipelines = await PlatformService.PipelineRuns(project.integrationID,project.repository); } catch (e) { error = String(e); } finally { busy = false; } }
  async function pin(run: PipelineRun) { try { await PlatformService.PinPipeline(project.workspaceID,project.integrationID,project.repository,run.id); app.toast('ok','Deployment added to service timeline'); } catch (e) { error = String(e); } }
</script>
<div class="flex h-full flex-col"><PageHeader icon={GitBranch} title="Deployments" subtitle="Review infrastructure changes and connect deployments to their pipeline and commit" />
  <div class="ops-body ops-form">
    {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
    <select aria-label="Saved project" value={project.id} onchange={(e) => { const found = projects.find((p) => p.id === e.currentTarget.value); project = found ? structuredClone($state.snapshot(found)) : blank(); pipelines = []; }}><option value="">New project</option>{#each projects as p}<option value={p.id}>{p.name}</option>{/each}</select>
    <div class="ops-grid"><label>Name<input bind:value={project.name} /></label><label>Tool<select bind:value={project.tool}><option value="tofu">OpenTofu</option><option value="terraform">Terraform</option><option value="ansible">Ansible</option></select></label><label>Run on<select bind:value={project.hostID}><option value="">Local machine</option>{#each app.scopedHosts as h}<option value={h.id}>{h.name}</option>{/each}</select></label><label>Working directory<input bind:value={project.directory} placeholder="/srv/infrastructure" /></label></div>
    {#if project.tool === 'ansible'}<div class="ops-grid"><label>Playbook<input bind:value={project.playbook} /></label><label>Inventory<input bind:value={project.inventory} /></label></div>{/if}
    <div class="ops-grid"><label>Pipeline provider<select bind:value={project.integrationID}><option value="">No pipeline integration</option>{#each integrations as i}<option value={i.id}>{i.name}</option>{/each}</select></label><label>Repository<input bind:value={project.repository} placeholder="owner/project" /></label></div>
    <div class="ops-actions"><button onclick={save} disabled={busy || !project.name || !project.directory}>Save project</button>{#if project.tool !== 'ansible'}<button onclick={() => preview(true)} disabled={busy || !project.id}>Review initialization</button>{/if}<button class="primary" onclick={() => preview()} disabled={busy || !project.id}>{busy ? 'Preparing…' : project.tool === 'ansible' ? 'Check playbook & review changes' : 'Generate infrastructure plan'}</button><button onclick={loadPipelines} disabled={busy || !project.integrationID || !project.repository}>Load pipeline runs</button></div>
    <p class="ops-muted">Saved project settings determine the execution target. Terraform and OpenTofu apply the exact saved plan shown during review.</p>
    {#if pipelines.length}<table class="ops-table"><thead><tr><th>Pipeline</th><th>Branch / commit</th><th>Result</th><th>Timeline</th></tr></thead><tbody>{#each pipelines as run (run.id)}<tr><td>{run.name} #{run.id}</td><td>{run.branch} · {run.commit.slice(0,10)}</td><td>{run.status} {run.conclusion}</td><td><button onclick={() => pin(run)}>Add deployment marker</button></td></tr>{/each}</tbody></table>{/if}
    <IntegrationsPanel onSaved={() => void refresh()} />
  </div>
</div>
{#if plan}<ChangeReview {plan} onClose={() => plan = null} />{/if}
