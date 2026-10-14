<script lang="ts">
  import { onMount } from "svelte";
  import { PlatformService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { KubeContext, KubeResource, KubeTarget, KubeForward, ChangePlan } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { app } from "./state.svelte";
  import { bus } from "./events";
  import PageHeader from "./PageHeader.svelte";
  import ChangeReview from "./ChangeReview.svelte";
  import { Boxes } from "@lucide/svelte";
  let hostID = $state(app.serviceContext?.kubeHostID ?? app.selectedHostID ?? "");
  let context = $state(app.serviceContext?.kubeContext ?? "");
  let namespace = $state(app.serviceContext?.kubeNamespace ?? "default");
  let contexts = $state<KubeContext[]>([]);
  let namespaces = $state<KubeResource[]>([]);
  let resources = $state<KubeResource[]>([]);
  let kind = $state("pods");
  let selected = $state<KubeResource | null>(null);
  let manifest = $state("");
  let output = $state("");
  let error = $state("");
  let busy = $state(false);
  let autoRefresh = $state(false);
  let replicas = $state(1);
  let container = $state("");
  let localPort = $state(8080);
  let targetPort = $state(80);
  let forwards = $state<KubeForward[]>([]);
  let plan = $state<ChangePlan | null>(null);
  const target = $derived<KubeTarget>({ hostID, context, namespace, workspaceID: app.serviceContext?.id ?? "" });
  let sequence = 0;
  async function refresh() {
    const ticket = ++sequence; busy = true; error = "";
    try { const items = await PlatformService.KubeResources($state.snapshot(target),kind); if (ticket === sequence) resources = items ?? []; }
    catch (e) { if (ticket === sequence) error = String(e); } finally { if (ticket === sequence) busy = false; }
  }
  async function loadContexts() {
    resources = []; selected = null; manifest = ""; output = ""; error = "";
    try {
      contexts = await PlatformService.KubeContexts(hostID);
      if (!contexts.some((c) => c.name === context)) context = contexts.find((c) => c.current)?.name ?? contexts[0]?.name ?? "";
      await changeContext();
    } catch (e) { error = String(e); }
  }
  async function changeContext() {
    selected = null; manifest = "";
    try { namespaces = await PlatformService.KubeResources({ ...$state.snapshot(target), namespace: "" },"namespaces"); if (!namespaces.some((n) => n.name === namespace)) namespace = contexts.find((c) => c.name === context)?.namespace || "default"; await refresh(); }
    catch (e) { error = String(e); }
  }
  onMount(() => { void loadContexts(); void PlatformService.KubeForwards().then((f) => forwards = f); const timer = setInterval(() => { if (autoRefresh && !busy) void refresh(); },5000); return () => { sequence++; clearInterval(timer); }; });
  async function inspect(resource: KubeResource) {
    selected = resource; output = "";
    try { manifest = await PlatformService.KubeManifest($state.snapshot(target),kind,resource.name); } catch (e) { error = String(e); }
  }
  async function observe(view: string) { busy = true; error = ""; try { output = await PlatformService.KubeObserve($state.snapshot(target),kind,selected?.name ?? "",view); } catch (e) { error = String(e); } finally { busy = false; } }
  async function preview(action = "manifest") {
    busy = true; error = "";
    try { plan = action === "manifest" ? await PlatformService.PreviewKubeManifest($state.snapshot(target),manifest) : await PlatformService.PreviewKubeAction($state.snapshot(target),kind,selected!.name,action,replicas); }
    catch (e) { error = String(e); } finally { busy = false; }
  }
  async function shell() { if (!selected) return; try { const command = await PlatformService.KubeExecCommand($state.snapshot(target),selected.name,container); bus.emit('open-host-command',{hostID,command}); } catch (e) { error = String(e); } }
  async function forward() { if (!selected) return; busy = true; try { await PlatformService.StartKubeForward($state.snapshot(target),kind,selected.name,localPort,targetPort); forwards = await PlatformService.KubeForwards(); } catch (e) { error = String(e); } finally { busy = false; } }
  async function stopForward(id: string) { await PlatformService.StopKubeForward(id); forwards = await PlatformService.KubeForwards(); }
</script>
<div class="flex h-full flex-col"><PageHeader icon={Boxes} title="Kubernetes" subtitle="Inspect clusters, review manifests and follow workloads through a rollout" />
  <div class="ops-body ops-form">
    <div class="ops-grid"><label>Access through<select bind:value={hostID} onchange={loadContexts}><option value="">Local kubectl</option>{#each app.hosts as host}<option value={host.id}>{host.name}</option>{/each}</select></label><label>Context<select bind:value={context} onchange={changeContext}>{#each contexts as c}<option value={c.name}>{c.name} · {c.cluster}</option>{/each}</select></label><label>Namespace<select bind:value={namespace} onchange={() => { selected = null; void refresh(); }}>{#each namespaces as n}<option value={n.name}>{n.name}</option>{/each}{#if !namespaces.length}<option value="default">default</option>{/if}</select></label><label>Resources<select bind:value={kind} onchange={() => { selected = null; void refresh(); }}>{#each ['pods','deployments','statefulsets','daemonsets','services','nodes','ingresses','configmaps','jobs','cronjobs'] as k}<option value={k}>{k}</option>{/each}</select></label></div>
    <div class="ops-actions"><button onclick={refresh} disabled={busy}>{busy ? 'Loading…' : 'Refresh'}</button><button onclick={() => observe('events')} disabled={busy}>Events</button><button onclick={() => observe('usage')} disabled={busy}>Resource usage</button><label class="!flex-row items-center"><input type="checkbox" bind:checked={autoRefresh} />Refresh every 5s</label></div>
    {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
    <div class="overflow-x-auto"><table class="ops-table"><thead><tr><th>Name</th><th>Status</th><th>Ready</th><th>Restarts</th><th>Images</th></tr></thead><tbody>{#each resources as resource (resource.namespace + '/' + resource.name)}<tr><td><button onclick={() => inspect(resource)}>{resource.name}</button></td><td>{resource.status || '—'}</td><td>{resource.ready || '—'}</td><td>{resource.restarts}</td><td class="max-w-72 break-all">{resource.images.join(', ')}</td></tr>{/each}</tbody></table></div>
    {#if !busy && !resources.length}<p class="ops-muted">No resources loaded for this selection.</p>{/if}
    {#if selected}<article class="ops-card ops-form"><strong>{selected.kind} / {selected.name}</strong><div class="ops-actions">
      {#if kind === 'pods'}<button onclick={() => observe('logs')}>Logs</button><input class="ops-input" aria-label="Container name" placeholder="Container (optional)" bind:value={container} /><button onclick={shell}>Open pod shell</button>{/if}
      {#if ['deployments','statefulsets','daemonsets'].includes(kind)}<button onclick={() => observe('rollout')}>Rollout status</button><button onclick={() => preview('restart')}>Review restart</button><button onclick={() => preview('undo')}>Review rollback</button>{/if}
      {#if ['deployments','statefulsets'].includes(kind)}<input class="ops-input w-20" aria-label="Replica count" type="number" min="0" max="10000" bind:value={replicas} /><button onclick={() => preview('scale')}>Review scale</button>{/if}
    </div>
    {#if ['pods','services','deployments'].includes(kind)}<div class="ops-grid"><label>Local port<input type="number" min="1" max="65535" bind:value={localPort} /></label><label>Workload port<input type="number" min="1" max="65535" bind:value={targetPort} /></label><button onclick={forward} disabled={busy}>Start port forward</button></div>{/if}
    </article>{/if}
    {#each forwards as f (f.id)}<div class="ops-card ops-actions"><span>127.0.0.1:{f.localPort} → {f.resource}:{f.targetPort} · {f.status}</span><button onclick={() => stopForward(f.id)}>Stop</button>{#if f.error}<span class="ops-error">{f.error}</span>{/if}</div>{/each}
    {#if output}<pre class="ops-code">{output}</pre>{/if}
    <label>Manifest<textarea rows="12" class="font-mono" bind:value={manifest} placeholder="Select a resource to edit its manifest, or paste a new manifest." spellcheck="false"></textarea></label>
    <div class="ops-actions"><button class="primary" onclick={() => preview()} disabled={busy || !manifest.trim()}>Validate &amp; review manifest</button><span class="ops-muted">Server validation and a live diff precede application.</span></div>
  </div>
</div>
{#if plan}<ChangeReview {plan} onClose={() => plan = null} onComplete={() => void refresh()} />{/if}
