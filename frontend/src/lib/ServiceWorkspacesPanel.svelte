<script lang="ts">
  import { onMount } from "svelte";
  import { DBService, PlatformService, PortForwardService, SyncService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { WorkspaceTemplate as SyncedWorkspaceTemplate } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { readLocalTemplates, safeTemplateConfig, TEMPLATE_STORAGE_KEY, type SafeTemplateConfig } from "./workspaceTemplates";
  import type { ServiceWorkspace, SavedConnection, DeploymentProject } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { unpack, type Versioned } from "./operationRecords";
  import { app } from "./state.svelte";
  import { bus } from "./events";
  import HostSelection from "./HostSelection.svelte";
  import PageHeader from "./PageHeader.svelte";
  import { Layers } from "@lucide/svelte";
  const blank = (): Versioned<ServiceWorkspace> => ({ id: "", revision: 0, name: "", environment: "dev", hostIDs: [], kubeHostID: "", kubeContext: "", kubeNamespace: "default", forwardIDs: [], metricHostIDs: [], logCommand: "", dbConnectionID: "", projectID: "", stateJSON: "" });
  type WorkspaceTemplateConfig = SafeTemplateConfig & Partial<ServiceWorkspace>;
  type WorkspaceTemplate = { id: string; name: string; description: string; config: WorkspaceTemplateConfig };
  const builtInTemplates: WorkspaceTemplate[] = [
    { id: "builtin:production-operations", name: "Production operations", description: "A production service view for routine investigation and operations.", config: { name: "Production operations", environment: "production", hostIDs: [], kubeHostID: "", kubeContext: "", kubeNamespace: "default", forwardIDs: [], metricHostIDs: [], logCommand: "journalctl -u service --since '15 minutes ago' --no-pager", dbConnectionID: "", projectID: "" } },
    { id: "builtin:kubernetes-incident", name: "Kubernetes incident", description: "A focused cluster incident starting point with recent events and a default namespace.", config: { name: "Kubernetes incident", environment: "production", hostIDs: [], kubeHostID: "", kubeContext: "", kubeNamespace: "default", forwardIDs: [], metricHostIDs: [], logCommand: "kubectl get events --sort-by=.lastTimestamp | tail -n 50", dbConnectionID: "", projectID: "" } },
    { id: "builtin:database-maintenance", name: "Database maintenance", description: "A maintenance workspace for database hosts and recent service logs.", config: { name: "Database maintenance", environment: "staging", hostIDs: [], kubeHostID: "", kubeContext: "", kubeNamespace: "default", forwardIDs: [], metricHostIDs: [], logCommand: "journalctl -u database --since '30 minutes ago' --no-pager", dbConnectionID: "", projectID: "" } },
    { id: "builtin:disaster-recovery", name: "Disaster recovery", description: "A production recovery workspace ready for explicitly chosen failover hosts and tunnels.", config: { name: "Disaster recovery", environment: "production", hostIDs: [], kubeHostID: "", kubeContext: "", kubeNamespace: "default", forwardIDs: [], metricHostIDs: [], logCommand: "journalctl -u service --since '1 hour ago' --no-pager", dbConnectionID: "", projectID: "" } },
  ];
  let workspaces = $state<Versioned<ServiceWorkspace>[]>([]);
  let editing = $state(blank());
  let forwards = $state<{id: string; name: string}[]>([]);
  let databases = $state<SavedConnection[]>([]);
  let projects = $state<DeploymentProject[]>([]);
  let templates = $state<WorkspaceTemplate[]>([]);
  let selectedTemplateID = $state("");
  let templateName = $state("");
  let syncedTemplates = $state<SyncedWorkspaceTemplate[]>([]);
  let templateScope = $state("local");
  let templateBusy = $state(false);
  let templatesLoaded = $state(false);
  let busy = $state(false);
  let error = $state("");
  function persistTemplates(next: WorkspaceTemplate[]): boolean {
    try {
      localStorage.setItem(TEMPLATE_STORAGE_KEY, JSON.stringify(next.map((item) => ({ id: item.id, name: item.name, description: "Local-only starting point.", config: safeTemplateConfig(item.config) }))));
      templates = next;
      return true;
    } catch (e) { error = `Could not save local template: ${e}`; return false; }
  }
  function selectedTemplate(): WorkspaceTemplate | undefined {
    const synced = syncedTemplates.find((template) => template.id === selectedTemplateID);
    return builtInTemplates.find((template) => template.id === selectedTemplateID) ?? templates.find((template) => template.id === selectedTemplateID) ?? (synced ? { id: synced.id, name: synced.name, description: "Synced starting point · choose device-specific connections below.", config: safeTemplateConfig({ name: synced.workspaceName, environment: synced.environment, kubeNamespace: synced.kubeNamespace }) } : undefined);
  }
  function applyTemplate() {
    const template = selectedTemplate();
    if (!template) return;
    editing = { ...blank(), ...structuredClone($state.snapshot(template.config)) };
    error = "";
  }
  async function saveTemplate() {
    const name = templateName.trim().slice(0, 80);
    if (!name) return;
    const catalog = templateScope === "synced" ? syncedTemplates : templates;
    if (catalog.some((template) => template.name.toLowerCase() === name.toLowerCase())) {
      error = "A template with that name already exists in this catalog.";
      return;
    }
    if (catalog.length >= 50) {
      error = "You can save up to 50 templates per catalog.";
      return;
    }
    const config = safeTemplateConfig(editing);
    if (templateScope === "synced") {
      templateBusy = true;
      try {
        const saved = await SyncService.SaveWorkspaceTemplate({ id: "", name, workspaceName: config.name, environment: config.environment, kubeNamespace: config.kubeNamespace, updatedAt: 0 }, 0);
        await refreshSyncedTemplates();
        selectedTemplateID = saved.id; templateName = ""; error = "";
        app.toast("ok", "Template ready for encrypted sync", saved.name);
      } catch (e) { error = String(e); } finally { templateBusy = false; }
      return;
    }
    const template: WorkspaceTemplate = { id: `local:${crypto.randomUUID()}`, name, description: "Local-only starting point.", config };
    if (persistTemplates([...templates, template])) {
      selectedTemplateID = template.id;
      templateName = "";
      error = "";
      app.toast("ok", "Template saved locally", template.name);
    }
  }
  function removeTemplate() {
    const template = templates.find((item) => item.id === selectedTemplateID);
    if (!template || !confirm(`Delete local template “${template.name}”?`)) return;
    if (persistTemplates(templates.filter((item) => item.id !== template.id))) selectedTemplateID = "";
  }
  async function refreshSyncedTemplates() { syncedTemplates = (await SyncService.ListWorkspaceTemplates()) ?? []; }
  async function syncLocalTemplate() {
    const local = templates.find((item) => item.id === selectedTemplateID);
    if (!local) return;
    templateBusy = true;
    try {
      const config = safeTemplateConfig(local.config);
      const saved = await SyncService.SaveWorkspaceTemplate({ id: "", name: local.name, workspaceName: config.name, environment: config.environment, kubeNamespace: config.kubeNamespace, updatedAt: 0 }, 0);
      await refreshSyncedTemplates(); selectedTemplateID = saved.id; error = "";
    } catch (e) { error = String(e); } finally { templateBusy = false; }
  }
  async function removeSyncedTemplate() {
    const template = syncedTemplates.find((item) => item.id === selectedTemplateID);
    if (!template || !confirm(`Delete synced template “${template.name}”? The deletion is shared on the next sync.`)) return;
    templateBusy = true;
    try { await SyncService.DeleteWorkspaceTemplate(template.id, template.updatedAt); await refreshSyncedTemplates(); selectedTemplateID = ""; }
    catch (e) { error = String(e); } finally { templateBusy = false; }
  }
  async function updateSyncedTemplate() {
    const template = syncedTemplates.find((item) => item.id === selectedTemplateID);
    if (!template) return;
    templateBusy = true;
    try {
      const config = safeTemplateConfig(editing);
      await SyncService.SaveWorkspaceTemplate({ id: template.id, name: template.name, workspaceName: config.name, environment: config.environment, kubeNamespace: config.kubeNamespace, updatedAt: template.updatedAt }, template.updatedAt);
      await refreshSyncedTemplates(); error = "";
      app.toast("ok", "Synced template updated", template.name);
    } catch (e) { error = String(e); } finally { templateBusy = false; }
  }
  async function refresh() { workspaces = (await PlatformService.ListRecords("workspace")).map((d) => unpack<ServiceWorkspace>(d)); }
  onMount(() => { void Promise.all([
    Promise.resolve().then(() => { templates = readLocalTemplates(localStorage); templatesLoaded = true; }),
    refreshSyncedTemplates(),
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
    <div class="ops-card ops-form">
      <div class="ops-actions"><h3 class="type-body">Workspace templates</h3><span class="ops-muted">Credential-free starting points</span></div>
      <div class="ops-grid">
        <label>Template<select aria-label="Workspace template" bind:value={selectedTemplateID}>
          <option value="">Choose a template</option>
          <optgroup label="Built-in">{#each builtInTemplates as template (template.id)}<option value={template.id}>{template.name}</option>{/each}</optgroup>
          {#if templates.length}<optgroup label="Local-only">{#each templates as template (template.id)}<option value={template.id}>{template.name} · Local-only</option>{/each}</optgroup>{/if}
          {#if syncedTemplates.length}<optgroup label="Synced catalog">{#each syncedTemplates as template (template.id)}<option value={template.id}>{template.name} · Synced</option>{/each}</optgroup>{/if}
        </select></label>
        <div class="ops-actions">
          <button onclick={applyTemplate} disabled={!selectedTemplateID}>Use template</button>
          {#if selectedTemplateID.startsWith("local:")}<button onclick={removeTemplate} disabled={templateBusy}>Delete local template</button><button onclick={syncLocalTemplate} disabled={templateBusy}>Create synced copy</button>{/if}
          {#if selectedTemplateID.startsWith("template:")}<button onclick={updateSyncedTemplate} disabled={templateBusy}>Update synced template from editor</button><button onclick={removeSyncedTemplate} disabled={templateBusy}>Delete synced template</button>{/if}
          <button onclick={() => refreshSyncedTemplates().catch((e) => error = String(e))} disabled={templateBusy}>Refresh synced catalog</button>
        </div>
      </div>
      {#if selectedTemplate()}<p class="ops-muted">{selectedTemplate()?.description}</p>{/if}
      {#if selectedTemplateID}<p class="ops-muted">{selectedTemplateID.startsWith("builtin:") ? "Built-in · available on this device" : selectedTemplateID.startsWith("local:") ? "Local-only · never uploaded" : "Synced · included in encrypted configuration sync; upload/download status is in Settings → Cloud sync"}</p>{/if}
      <div class="ops-actions"><label class="flex-1">Save starting point as template<input bind:value={templateName} maxlength="80" placeholder="Payments production" /></label><label>Storage<select bind:value={templateScope}><option value="local">Local-only</option><option value="synced">Synced catalog</option></select></label><button onclick={saveTemplate} disabled={!templateName.trim() || templateBusy || (templateScope === "local" && !templatesLoaded)}>Save template</button></div>
      <p class="ops-muted">Only template/service names, environment, and namespace are saved. Credentials, host/tunnel/profile IDs, Kubernetes contexts, commands, and terminal state are excluded. Older local templates remain local-only; their original browser data is retained as a recovery copy.</p>
      <p class="ops-muted">Use Settings → Cloud sync to preview conflicts and push/pull the synced catalog. Newer timestamps win; equal timestamps keep this device’s version. Refresh this catalog after a pull. Importing never opens or activates a workspace.</p>
      <p class="ops-muted">Applying a template only prefills this editor. Choose and review hosts, monitoring targets, tunnels, and saved profiles below before saving the workspace.</p>
    </div>
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
