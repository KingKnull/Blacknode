<script lang="ts">
  import { onMount } from "svelte";
  import { DBService, PlatformService, PortForwardService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { ServiceWorkspace, SavedConnection, DeploymentProject } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { unpack, type Versioned } from "./operationRecords";
  import { app } from "./state.svelte";
  import { bus } from "./events";
  import HostSelection from "./HostSelection.svelte";
  import PageHeader from "./PageHeader.svelte";
  import { Layers } from "@lucide/svelte";
  const blank = (): Versioned<ServiceWorkspace> => ({ id: "", revision: 0, name: "", environment: "dev", hostIDs: [], kubeHostID: "", kubeContext: "", kubeNamespace: "default", forwardIDs: [], metricHostIDs: [], logCommand: "", dbConnectionID: "", projectID: "", stateJSON: "" });
  let workspaces = $state<Versioned<ServiceWorkspace>[]>([]);
  let editing = $state(blank());
  let forwards = $state<{id: string; name: string}[]>([]);
  let databases = $state<SavedConnection[]>([]);
  let projects = $state<DeploymentProject[]>([]);
  let busy = $state(false);
  let error = $state("");
  async function refresh() { workspaces = (await PlatformService.ListRecords("workspace")).map((d) => unpack<ServiceWorkspace>(d)); }
  onMount(() => { void Promise.all([
    refresh(), PortForwardService.List().then((items) => forwards = items ?? []),
    DBService.ListSavedConnections().then((items) => databases = items ?? []),
    PlatformService.ListRecords('project').then((items) => projects = items.map((item) => unpack<DeploymentProject>(item))),
  ]).catch((e) => error = String(e)); });
  async function save() {
    busy = true; error = "";
    try { const { revision, ...workspace } = $state.snapshot(editing); editing = unpack<ServiceWorkspace>(await PlatformService.SaveWorkspace(workspace,revision)); await refresh(); }
    catch (e) { error = String(e); } finally { busy = false; }
  }
  async function remove() {
    if (!editing.id || !confirm(`Delete workspace “${editing.name}”?`)) return;
    try { await PlatformService.DeleteRecord("workspace",editing.id,editing.revision); if (app.serviceContext?.id === editing.id) app.activateServiceWorkspace(null); editing = blank(); await refresh(); }
    catch (e) { error = String(e); }
  }
</script>
<div class="flex h-full flex-col"><PageHeader icon={Layers} title="Service workspaces" subtitle="Bring the hosts, cluster, logs and tunnels for a service together" />
  <div class="ops-body ops-form">
    {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
    <div class="ops-grid">{#each workspaces as workspace (workspace.id)}<article class="ops-card ops-form"><strong>{workspace.name}</strong><p class="ops-muted">{workspace.environment} · {workspace.hostIDs.length} hosts · {workspace.kubeContext || 'No cluster selected'}</p><div class="ops-actions"><button class="primary" onclick={() => bus.emit('open-service-workspace',workspace)}>Open workspace</button><button onclick={() => editing = structuredClone($state.snapshot(workspace))}>Edit</button></div></article>{/each}</div>
    <div class="ops-actions"><h3 class="type-body">{editing.id ? 'Edit workspace' : 'Create workspace'}</h3><button onclick={() => editing = blank()}>New</button></div>
    <fieldset disabled={busy} class="ops-form">
      <div class="ops-grid"><label>Service name<input bind:value={editing.name} placeholder="Payments" maxlength="120" /></label><label>Environment<select bind:value={editing.environment}><option value="dev">Development</option><option value="staging">Staging</option><option value="production">Production</option></select></label></div>
      <HostSelection bind:selected={editing.hostIDs} scoped={false} />
      <div class="ops-grid"><label>Kubernetes access<select bind:value={editing.kubeHostID}><option value="">Local kubectl</option>{#each app.hosts as host}<option value={host.id}>{host.name}</option>{/each}</select></label><label>Kubernetes context<input bind:value={editing.kubeContext} placeholder="Current context" /></label><label>Namespace<input bind:value={editing.kubeNamespace} /></label></div>
      <label>Log command<input bind:value={editing.logCommand} placeholder="journalctl -u payments --since '15 minutes ago' --no-pager" /></label>
      <div class="ops-grid">
        <label>Database profile<select bind:value={editing.dbConnectionID}><option value="">None</option>{#each databases as database}<option value={database.id}>{database.name}</option>{/each}</select></label>
        <label>Deployment project<select bind:value={editing.projectID}><option value="">None</option>{#each projects as project}<option value={project.id}>{project.name}</option>{/each}</select></label>
      </div>
      <p class="ops-muted">The incident capture action uses this command to collect logs. Opening the workspace starts the selected monitoring and tunnels.</p>
      <fieldset class="ops-card"><legend>Monitor on open</legend><div class="ops-hosts">{#each app.hosts.filter((h) => editing.hostIDs.includes(h.id)) as host}<label><input type="checkbox" checked={editing.metricHostIDs.includes(host.id)} onchange={(e) => editing.metricHostIDs = e.currentTarget.checked ? [...editing.metricHostIDs,host.id] : editing.metricHostIDs.filter((id) => id !== host.id)} />{host.name}</label>{/each}</div></fieldset>
      <fieldset class="ops-card"><legend>Tunnel presets</legend><div class="ops-hosts">{#each forwards as forward}<label><input type="checkbox" checked={editing.forwardIDs.includes(forward.id)} onchange={(e) => editing.forwardIDs = e.currentTarget.checked ? [...editing.forwardIDs,forward.id] : editing.forwardIDs.filter((id) => id !== forward.id)} />{forward.name}</label>{/each}</div></fieldset>
      <div class="ops-actions"><button class="primary" onclick={save} disabled={!editing.name || !editing.hostIDs.length}>{busy ? 'Saving…' : 'Save workspace'}</button>{#if editing.id}<button onclick={remove}>Delete</button>{/if}</div>
    </fieldset>
  </div>
</div>
