<script lang="ts">
  import { onMount } from "svelte";
  import { InfrastructureService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { MonitorView, ServiceMonitor } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { app } from "./state.svelte";
  import { useOperationTarget } from "./operationSelection.svelte";
  import PageHeader from "./PageHeader.svelte";
  import { Activity } from "@lucide/svelte";
  const blank = (): ServiceMonitor => ({ id: "", name: "", kind: "http", target: "", hostID: "", intervalSeconds: 60, timeoutSeconds: 10, expectedStatus: 200, expectedDNS: "", certificateDays: 14, failureThreshold: 3, enabled: true, maintenanceStart: 0, maintenanceEnd: 0 });
  let monitors = $state<MonitorView[]>([]);
  let draft = $state(blank());
  let revision = $state(0);
  let maintenanceStart = $state("");
  let maintenanceEnd = $state("");
  let selected = $state("");
  let error = $state("");
  let busy = $state(false);
  let editor = $state(false);
  let showSamples = $state(false);
  useOperationTarget('uptime', {
    load: () => InfrastructureService.Monitors(),
    selection: () => selected,
    clear: () => { selected = ''; editor = false; showSamples = false; error = ''; },
    apply: (next, target) => {
      monitors = next ?? [];
      if (!target) return;
      if (!monitors.some(m => m.monitor.id === target.id)) throw new Error('This monitor is no longer available.');
      selected = target.id;
    },
    error: message => error = message,
  });
  const current = $derived(monitors.find(m => m.monitor.id === selected));
  const latest = (m: MonitorView) => m.state.samples?.at(-1);
  const status = (m: MonitorView) => !m.monitor.enabled ? 'paused' : !latest(m) ? 'pending' : Date.now() - latest(m)!.at > (m.monitor.intervalSeconds * 3 + m.monitor.timeoutSeconds) * 1000 ? 'stale' : latest(m)!.status;
  const healthy = $derived(monitors.filter(m => status(m) === 'up').length);
  const localTime = (n: number) => n ? new Date(n - new Date(n).getTimezoneOffset() * 60000).toISOString().slice(0, 16) : '';
  function edit(m?: MonitorView) { draft = m ? { ...m.monitor } : blank(); revision = m?.revision ?? 0; maintenanceStart = localTime(draft.maintenanceStart); maintenanceEnd = localTime(draft.maintenanceEnd); editor = true; }
  async function refresh() { monitors = await InfrastructureService.Monitors(); }
  async function act(fn: () => Promise<unknown>) { busy = true; error = ''; try { await fn(); await refresh(); } catch (e) { error = String(e); } finally { busy = false; } }
  async function save() { await act(async () => { await InfrastructureService.SaveMonitor({ ...draft, maintenanceStart: maintenanceStart ? new Date(maintenanceStart).getTime() : 0, maintenanceEnd: maintenanceEnd ? new Date(maintenanceEnd).getTime() : 0 }, revision); editor = false; }); }
  onMount(() => { let live = true; const load = async () => { try { const next = await InfrastructureService.Monitors(); if (live) monitors = next; } catch (e) { if (live) error = String(e); } }; const timer = setInterval(load, 5000); return () => { live = false; clearInterval(timer); }; });
  function points(samples: MonitorView['state']['samples']) { const recent = samples.slice(-100); const max = Math.max(1, ...recent.map(s => s.latencyMS)); return recent.map((s, i) => `${i * 600 / Math.max(1, recent.length - 1)},${90 - s.latencyMS * 80 / max}`).join(' '); }
</script>
<div class="flex h-full min-h-0 flex-col">
  <PageHeader icon={Activity} title="Service health" subtitle="Uptime, response times and certificate expiry" />
  <div class="ops-body ops-form">
    <div class="ops-actions"><strong>{healthy} healthy / {monitors.length} monitors</strong><button class="primary" onclick={() => edit()}>Add monitor</button><button disabled={busy} onclick={() => act(refresh)}>Refresh</button></div>
    <p class="ops-muted">Checks run from this machine while Blacknode is open. Use Team → Runner for monitoring that continues when the desktop is closed. History retains the latest 1,000 checks per monitor.</p>
    {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
    {#if editor}
      <form class="ops-card ops-form" onsubmit={e => { e.preventDefault(); void save(); }}>
        <h3>{draft.id ? 'Edit monitor' : 'New monitor'}</h3>
        <div class="ops-grid"><label>Name<input required bind:value={draft.name} /></label><label>Check<select bind:value={draft.kind}><option value="http">HTTP / HTTPS</option><option value="tcp">TCP port</option><option value="dns">DNS address</option><option value="tls">TLS certificate</option></select></label><label>Target<input required bind:value={draft.target} placeholder={draft.kind === 'http' ? 'https://app.example.com/health' : draft.kind === 'dns' ? 'app.example.com' : 'app.example.com:443'} /></label><label>Associated host<select bind:value={draft.hostID}><option value="">Unassigned</option>{#each app.hosts as h (h.id)}<option value={h.id}>{h.name} · {h.host}</option>{/each}</select></label><label>Interval (seconds)<input type="number" min="10" max="86400" bind:value={draft.intervalSeconds} /></label><label>Timeout (seconds)<input type="number" min="1" max="60" bind:value={draft.timeoutSeconds} /></label><label>Failures before alert<input type="number" min="1" max="20" bind:value={draft.failureThreshold} /></label><label>Certificate warning (days)<input type="number" min="0" max="365" bind:value={draft.certificateDays} /></label>
        {#if draft.kind === 'http'}<label>Expected HTTP status<input type="number" min="100" max="599" bind:value={draft.expectedStatus} /></label>{/if}
        {#if draft.kind === 'dns'}<label>Expected IP (optional)<input bind:value={draft.expectedDNS} /></label>{/if}
        <label>Maintenance starts<input type="datetime-local" bind:value={maintenanceStart} /></label><label>Maintenance ends<input type="datetime-local" bind:value={maintenanceEnd} /></label></div>
        <label class="ops-check"><input type="checkbox" bind:checked={draft.enabled} />Monitoring enabled</label>
        <div class="ops-actions"><button type="submit" class="primary" disabled={busy}>Save monitor</button><button type="button" onclick={() => editor = false}>Cancel</button></div>
      </form>
    {/if}
    <div class="overflow-x-auto"><table class="ops-table"><thead><tr><th>Service</th><th>Status</th><th>Latency</th><th>Uptime</th><th>Certificate expiry</th><th>Actions</th></tr></thead><tbody>
      {#each monitors as m (m.monitor.id)}<tr><td><button onclick={() => selected = m.monitor.id}>{m.monitor.name}</button><p class="ops-muted break-all">{m.monitor.target}</p></td><td><span class={status(m) === 'up' ? 'ops-success' : ['down','expiring'].includes(status(m)) ? 'ops-warning' : 'ops-muted'}>{status(m)}</span></td><td>{latest(m)?.latencyMS ?? '—'} ms</td><td>{m.state.samples.length ? `${m.uptimePercent.toFixed(2)}%` : '—'}</td><td>{latest(m)?.certificateExpires ? new Date(latest(m)!.certificateExpires).toLocaleDateString() : '—'}</td><td><div class="ops-actions"><button disabled={busy} onclick={() => act(() => InfrastructureService.CheckMonitor(m.monitor.id))}>Check now</button><button onclick={() => edit(m)}>Edit</button><button disabled={busy} onclick={() => act(() => InfrastructureService.SaveMonitor({ ...m.monitor, enabled: !m.monitor.enabled }, m.revision))}>{m.monitor.enabled ? 'Pause' : 'Enable'}</button><button disabled={busy} onclick={() => act(() => InfrastructureService.DeleteMonitor(m.monitor.id, m.revision))}>Delete</button></div></td></tr>
      {:else}<tr><td colspan="6">Add a monitor to start collecting service health.</td></tr>{/each}
    </tbody></table></div>
    {#if current}<div class="ops-card ops-form"><h3>{current.monitor.name} · response time</h3><svg viewBox="0 0 600 100" class="h-28 w-full" role="img" aria-label="Recent response times"><polyline points={points(current.state.samples)} fill="none" stroke="var(--color-accent)" stroke-width="2" /></svg><button aria-expanded={showSamples} onclick={() => showSamples = !showSamples}>{showSamples ? 'Hide check results' : 'Show check results'}</button>{#if showSamples}<div class="max-h-64 overflow-auto"><table class="ops-table"><thead><tr><th>Time</th><th>Status</th><th>Latency</th><th>Result</th></tr></thead><tbody>{#each [...current.state.samples].reverse().slice(0,100) as sample}<tr><td>{new Date(sample.at).toLocaleString()}</td><td>{sample.status}</td><td>{sample.latencyMS} ms</td><td>{sample.detail}</td></tr>{/each}</tbody></table></div>{/if}</div>{/if}
  </div>
</div>
