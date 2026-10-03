<script lang="ts">
  import { onMount } from "svelte";
  import { ExecService, OpsConsoleService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { ExecResult } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import type {
    ConnectionHealth,
    DiagnosticSection,
    IncidentSnapshot,
    MultiplexerSession,
  } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { app } from "./state.svelte";
  import { bus } from "./events";
  import PageHeader from "./PageHeader.svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";
  import { checkCommand, anyProduction } from "./danger";
  import { envBadge } from "./envColor";
  import {
    Activity,
    AlertTriangle,
    Check,
    Cpu,
    Loader2,
    Radio,
    RefreshCw,
    Server,
    ShieldCheck,
    TerminalSquare,
  } from "@lucide/svelte";

  type Tab = "matrix" | "diagnostics" | "health" | "multiplexer" | "incident";
  let activeTab = $state<Tab>("matrix");
  const TABS: { id: Tab; label: string; Icon: any }[] = [
    { id: "matrix", label: "Fleet", Icon: Radio },
    { id: "diagnostics", label: "Diagnostics", Icon: Cpu },
    { id: "health", label: "Health", Icon: Activity },
    { id: "multiplexer", label: "Sessions", Icon: TerminalSquare },
    { id: "incident", label: "Incident", Icon: AlertTriangle },
  ];

  const sshHosts = $derived(app.hosts.filter((h) => !h.protocol || h.protocol === "ssh"));
  let selectedHostID = $state("");
  let selectedHosts = $state<Set<string>>(new Set());

  onMount(() => {
    selectedHostID = app.selectedHostID ?? sshHosts[0]?.id ?? "";
    selectedHosts = new Set(sshHosts.slice(0, 8).map((h) => h.id));
  });

  function toggleHost(id: string) {
    const next = new Set(selectedHosts);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    selectedHosts = next;
  }

  // ── Fleet command matrix ──────────────────────────────────────────
  let commandText = $state("uptime\ndf -h\nfree -m");
  let matrixBusy = $state(false);
  let matrixResults = $state<Record<string, ExecResult>>({});
  let selectedCell = $state<{ command: string; hostID: string } | null>(null);

  let commands = $derived(
    commandText.split("\n").map((line) => line.trim()).filter(Boolean).slice(0, 12),
  );

  type MatrixConfirmation = {
    title: string;
    body: string;
    severity: "warn" | "block-without-confirm";
    requirePhrase?: string;
    productionHosts: string[];
  };
  let matrixConfirm = $state<MatrixConfirmation | null>(null);

  function confirmMatrix(): MatrixConfirmation | null {
    const hosts = sshHosts.filter((h) => selectedHosts.has(h.id));
    const production = hosts.filter((h) => h.environment?.toLowerCase() === "production").map((h) => h.name);
    const dangerous = commands.map(checkCommand).find(Boolean);
    if (dangerous?.level === "block-without-confirm") {
      return {
        title: `Dangerous matrix — ${dangerous.reason}`,
        body: `A command matches “${dangerous.matched}” and will run on ${hosts.length} hosts.`,
        severity: "block-without-confirm",
        requirePhrase: production.length ? "destroy production" : "I understand",
        productionHosts: production,
      };
    }
    if (dangerous || anyProduction(hosts.map((h) => h.environment))) {
      return {
        title: dangerous ? `Risky matrix — ${dangerous.reason}` : "Production hosts in scope",
        body: dangerous
          ? `A command matches “${dangerous.matched}” and will run on ${hosts.length} hosts.`
          : `${production.length} production host(s) are selected for this matrix.`,
        severity: "warn",
        productionHosts: production,
      };
    }
    return null;
  }

  async function runMatrix(retryFailed = false) {
    if (!commands.length || !selectedHosts.size) return;
    const confirmation = retryFailed ? null : confirmMatrix();
    if (confirmation) {
      matrixConfirm = confirmation;
      return;
    }
    matrixConfirm = null;
    matrixBusy = true;
    const runID = crypto.randomUUID();
    if (!retryFailed) matrixResults = {};
    try {
      for (let commandIndex = 0; commandIndex < commands.length; commandIndex++) {
        const command = commands[commandIndex];
        let hostIDs = [...selectedHosts];
        if (retryFailed) {
          hostIDs = hostIDs.filter((hostID) => {
            const result = matrixResults[`${commandIndex}:${hostID}`];
            return !result || result.exitCode !== 0 || result.error;
          });
        }
        if (!hostIDs.length) continue;
        const results = await ExecService.Run(`${runID}:${commandIndex}`, command, hostIDs, 60);
        for (const result of results ?? []) {
          matrixResults[`${commandIndex}:${result.hostID}`] = result;
        }
      }
    } catch (error: any) {
      app.toast("error", "Fleet matrix failed", String(error?.message ?? error));
    } finally {
      matrixBusy = false;
    }
  }

  function cellResult(commandIndex: number, hostID: string) {
    return matrixResults[`${commandIndex}:${hostID}`];
  }

  function failedCount() {
    return Object.values(matrixResults).filter((result) => result.exitCode !== 0 || result.error).length;
  }

  // ── Diagnostics, health, multiplexer, incident ────────────────────
  let diagnosticsBusy = $state(false);
  let diagnostics = $state<DiagnosticSection[]>([]);
  let healthBusy = $state(false);
  let health = $state<ConnectionHealth | null>(null);
  let muxBusy = $state(false);
  let muxSessions = $state<MultiplexerSession[]>([]);
  let incidentBusy = $state(false);
  let incidents = $state<IncidentSnapshot[]>([]);

  async function runDiagnostics() {
    if (!selectedHostID) return;
    diagnosticsBusy = true;
    try {
      diagnostics = (await OpsConsoleService.Diagnostics(selectedHostID)) ?? [];
    } catch (error: any) {
      app.toast("error", "Diagnostics failed", String(error?.message ?? error));
    } finally {
      diagnosticsBusy = false;
    }
  }

  async function runHealth() {
    if (!selectedHostID) return;
    healthBusy = true;
    try {
      health = await OpsConsoleService.ConnectionHealth(selectedHostID);
    } catch (error: any) {
      app.toast("error", "Health check failed", String(error?.message ?? error));
    } finally {
      healthBusy = false;
    }
  }

  async function loadMultiplexer() {
    if (!selectedHostID) return;
    muxBusy = true;
    try {
      muxSessions = (await OpsConsoleService.MultiplexerSessions(selectedHostID)) ?? [];
    } catch (error: any) {
      app.toast("error", "Session discovery failed", String(error?.message ?? error));
    } finally {
      muxBusy = false;
    }
  }

  async function captureIncident() {
    if (!selectedHosts.size) return;
    incidentBusy = true;
    const runID = `INC-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, "")}`;
    try {
      incidents = (await OpsConsoleService.IncidentSnapshot(runID, [...selectedHosts])) ?? [];
      app.toast("ok", "Incident snapshot captured", `${incidents.length} host(s) · ${runID}`);
    } catch (error: any) {
      app.toast("error", "Incident capture failed", String(error?.message ?? error));
    } finally {
      incidentBusy = false;
    }
  }

  function attachMultiplexer(session: MultiplexerSession) {
    const command = session.manager === "tmux"
      ? `tmux attach-session -t ${JSON.stringify(session.name)}`
      : `zellij attach ${session.name}`;
    app.view = "terminals";
    bus.emit("insert-into-active-terminal", command + "\n");
  }

  async function copyIncidentEvidence() {
    try {
      await navigator.clipboard.writeText(JSON.stringify(incidents, null, 2));
      app.toast("ok", "Evidence copied", "Incident snapshot JSON copied to clipboard.");
    } catch {
      app.toast("error", "Copy failed", "Clipboard is unavailable in this webview.");
    }
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={ShieldCheck}
    title="Ops console"
    subtitle="Fleet matrix, zero-agent diagnostics, connection health, multiplexers, and incident evidence"
  />

  <div class="flex items-center gap-1 border-b hairline surface-1 px-3 py-2">
    {#each TABS as tab (tab.id)}
      <button
        class="flex items-center gap-1.5 rounded px-2.5 py-1.5 type-caption transition-colors {activeTab === tab.id
          ? 'bg-[var(--color-accent)]/12 text-[var(--color-accent)]'
          : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-1)]'}"
        onclick={() => (activeTab = tab.id)}
      >
        <tab.Icon size="12" />{tab.label}
      </button>
    {/each}
    <div class="ml-auto flex items-center gap-2 type-caption text-[var(--color-text-4)]">
      <select
        class="rounded border hairline bg-[var(--color-surface-3)] px-2 py-1 font-mono type-micro text-[var(--color-text-1)]"
        bind:value={selectedHostID}
        onchange={() => {
          diagnostics = [];
          health = null;
          muxSessions = [];
        }}
      >
        <option value="">Select host…</option>
        {#each sshHosts as host (host.id)}
          <option value={host.id}>{host.name}</option>
        {/each}
      </select>
    </div>
  </div>

  <div class="grid flex-1 overflow-hidden grid-cols-[240px_1fr]">
    <aside class="overflow-y-auto border-r hairline">
      <div class="flex items-center justify-between px-3 py-2 type-eyebrow text-[var(--color-text-4)]">
        Hosts
        <span>{selectedHosts.size}/{sshHosts.length}</span>
      </div>
      {#each sshHosts as host (host.id)}
        {@const env = envBadge(host.environment)}
        <label class="flex cursor-pointer items-center gap-2 border-b hairline px-3 py-2 type-caption hover:bg-[var(--color-surface-2)]">
          <input type="checkbox" checked={selectedHosts.has(host.id)} onchange={() => toggleHost(host.id)} />
          <Server size="12" class="text-[var(--color-text-3)]" />
          <span class="min-w-0 flex-1 truncate">{host.name}</span>
          {#if env.label}<span class="type-nano" style:color={env.color}>{env.label}</span>{/if}
        </label>
      {/each}
    </aside>

    <section class="overflow-y-auto">
      {#if activeTab === "matrix"}
        <div class="space-y-3 p-4">
          <label class="block">
            <span class="type-caption text-[var(--color-text-3)]">Commands — one per line, up to 12</span>
            <textarea
              class="mt-1 h-28 w-full resize-y rounded border hairline bg-[var(--color-surface-3)] p-2 font-mono type-caption text-[var(--color-text-1)] outline-none"
              bind:value={commandText}
            ></textarea>
          </label>
          <div class="flex flex-wrap gap-2">
            <button class="flex items-center gap-1.5 rounded bg-[var(--color-accent)] px-3 py-2 type-caption text-[var(--color-surface-0)] disabled:opacity-40" disabled={matrixBusy || !commands.length || !selectedHosts.size} onclick={() => runMatrix()}>
              {#if matrixBusy}<Loader2 size="12" class="animate-spin" />{:else}<Radio size="12" />{/if}
              Run matrix
            </button>
            <button class="flex items-center gap-1.5 rounded border hairline px-3 py-2 type-caption disabled:opacity-40" disabled={matrixBusy || !Object.keys(matrixResults).length} onclick={() => runMatrix(true)}>
              <RefreshCw size="12" />Retry failed {failedCount() ? `(${failedCount()})` : ""}
            </button>
          </div>
          {#if commands.length}
            <div class="overflow-x-auto rounded border hairline">
              <table class="w-full border-collapse font-mono type-caption">
                <thead class="surface-1">
                  <tr>
                    <th class="sticky left-0 z-10 border-b border-r hairline surface-1 px-3 py-2 text-left">Host</th>
                    {#each commands as command, i (command)}
                      <th class="border-b hairline px-3 py-2 text-left text-[var(--color-text-3)]">{command}</th>
                    {/each}
                  </tr>
                </thead>
                <tbody>
                  {#each sshHosts.filter((host) => selectedHosts.has(host.id)) as host (host.id)}
                    <tr>
                      <td class="sticky left-0 z-10 border-b border-r hairline surface-1 px-3 py-2">{host.name}</td>
                      {#each commands as command, i (command)}
                        {@const result = cellResult(i, host.id)}
                        <td class="border-b hairline px-3 py-2">
                          <button
                            class="flex items-center gap-1 {result ? (result.exitCode === 0 && !result.error ? 'text-[var(--color-success)]' : 'text-[var(--color-danger)]') : 'text-[var(--color-text-4)]'}"
                            onclick={() => (selectedCell = { command, hostID: host.id })}
                          >
                            {#if result}
                              {#if result.exitCode === 0 && !result.error}<Check size="11" />{:else}<AlertTriangle size="11" />{/if}
                              {result.error ? "error" : `exit ${result.exitCode}`}
                            {:else if matrixBusy}
                              <Loader2 size="11" class="animate-spin" />
                            {:else}
                              pending
                            {/if}
                          </button>
                        </td>
                      {/each}
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </div>
      {:else if activeTab === "diagnostics"}
        <div class="p-4">
          <button class="flex items-center gap-1.5 rounded bg-[var(--color-accent)] px-3 py-2 type-caption text-[var(--color-surface-0)] disabled:opacity-40" disabled={!selectedHostID || diagnosticsBusy} onclick={runDiagnostics}>
            {#if diagnosticsBusy}<Loader2 size="12" class="animate-spin" />{:else}<Cpu size="12" />{/if}
            Run zero-agent diagnostics
          </button>
          <div class="mt-3 space-y-2">
            {#each diagnostics as section (section.title)}
              <details class="rounded border hairline surface-2" open={section.exitCode !== 0}>
                <summary class="cursor-pointer px-3 py-2 type-caption">
                  {section.title}
                  <span class="ml-2 text-[var(--color-text-4)]">{section.durationMs}ms</span>
                </summary>
                <pre class="max-h-56 overflow-auto border-t hairline bg-[var(--color-code-bg)] p-3 font-mono type-caption">{section.output || section.error || "(empty)"}</pre>
              </details>
            {:else}
              <p class="type-caption text-[var(--color-text-4)]">Select a host and run diagnostics.</p>
            {/each}
          </div>
        </div>
      {:else if activeTab === "health"}
        <div class="p-4">
          <button class="flex items-center gap-1.5 rounded bg-[var(--color-accent)] px-3 py-2 type-caption text-[var(--color-surface-0)] disabled:opacity-40" disabled={!selectedHostID || healthBusy} onclick={runHealth}>
            {#if healthBusy}<Loader2 size="12" class="animate-spin" />{:else}<Activity size="12" />{/if}
            Check connection health
          </button>
          {#if health}
            <div class="mt-3 grid gap-3 sm:grid-cols-4">
              {#each [["Latency", `${health.latencyMs}ms`], ["Jitter", `${health.jitterMs}ms`], ["Loss", `${health.loss.toFixed(0)}%`], ["Quality", health.quality]] as [label, value] (label)}
                <div class="rounded border hairline surface-2 p-3">
                  <div class="type-micro text-[var(--color-text-4)]">{label}</div>
                  <div class="mt-1 font-mono type-body text-[var(--color-text-1)]">{value}</div>
                </div>
              {/each}
            </div>
          {:else}
            <p class="mt-3 type-caption text-[var(--color-text-4)]">Runs five SSH keepalive round trips and reports median latency, jitter, and loss.</p>
          {/if}
        </div>
      {:else if activeTab === "multiplexer"}
        <div class="p-4">
          <button class="flex items-center gap-1.5 rounded bg-[var(--color-accent)] px-3 py-2 type-caption text-[var(--color-surface-0)] disabled:opacity-40" disabled={!selectedHostID || muxBusy} onclick={loadMultiplexer}>
            {#if muxBusy}<Loader2 size="12" class="animate-spin" />{:else}<TerminalSquare size="12" />{/if}
            Find tmux / Zellij sessions
          </button>
          <div class="mt-3 space-y-2">
            {#each muxSessions as session (session.manager + session.name)}
              <button class="flex w-full items-center gap-3 rounded border hairline surface-2 px-3 py-2 text-left hover:border-[var(--color-accent)]/40" onclick={() => attachMultiplexer(session)}>
                <span class="rounded border border-[var(--color-accent)]/30 bg-[var(--color-accent)]/10 px-1.5 py-0.5 font-mono type-nano text-[var(--color-accent)]">{session.manager.toUpperCase()}</span>
                <span class="min-w-0 flex-1 truncate font-mono type-caption">{session.name}</span>
                <span class="truncate type-micro text-[var(--color-text-4)]">{session.detail}</span>
              </button>
            {:else}
              <p class="type-caption text-[var(--color-text-4)]">No remote multiplexer sessions found.</p>
            {/each}
          </div>
        </div>
      {:else if activeTab === "incident"}
        <div class="p-4">
          <div class="flex flex-wrap gap-2">
            <button class="flex items-center gap-1.5 rounded bg-[var(--color-danger)] px-3 py-2 type-caption text-[var(--color-surface-0)] disabled:opacity-40" disabled={!selectedHosts.size || incidentBusy} onclick={captureIncident}>
              {#if incidentBusy}<Loader2 size="12" class="animate-spin" />{:else}<AlertTriangle size="12" />{/if}
              Capture incident snapshot
            </button>
            <button class="rounded border hairline px-3 py-2 type-caption disabled:opacity-40" disabled={!incidents.length} onclick={copyIncidentEvidence}>Copy evidence JSON</button>
          </div>
          <div class="mt-3 space-y-3">
            {#each incidents as incident (incident.runID + incident.hostID)}
              <details class="rounded border hairline surface-2" open>
                <summary class="cursor-pointer px-3 py-2 type-caption">{incident.hostName || incident.hostID}</summary>
                {#each incident.sections as section (incident.hostID + section.title)}
                  <details class="border-t hairline">
                    <summary class="cursor-pointer px-3 py-2 type-micro">{section.title}</summary>
                    <pre class="max-h-48 overflow-auto bg-[var(--color-code-bg)] p-3 font-mono type-caption">{section.output || section.error || "(empty)"}</pre>
                  </details>
                {/each}
              </details>
            {:else}
              <p class="type-caption text-[var(--color-text-4)]">Capture system, disk, memory, process, port, and service evidence from selected hosts.</p>
            {/each}
          </div>
        </div>
      {/if}
    </section>
  </div>

  {#if selectedCell}
    {@const host = app.hosts.find((h) => h.id === selectedCell!.hostID)}
    {@const result = matrixResults[`${commands.indexOf(selectedCell.command)}:${selectedCell.hostID}`]}
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-6" role="presentation" onclick={(e) => { if (e.target === e.currentTarget) selectedCell = null; }}>
      <div class="max-h-[75vh] w-[min(90vw,900px)] overflow-hidden rounded border hairline-strong surface-2">
        <div class="flex items-center gap-2 border-b hairline px-4 py-2">
          <span class="font-mono type-caption">{host?.name ?? selectedCell.hostID}</span>
          <span class="text-[var(--color-text-4)]">·</span>
          <span class="font-mono type-caption">{selectedCell.command}</span>
          <button class="ml-auto rounded px-2 py-1 type-caption hover:bg-[var(--color-surface-3)]" onclick={() => (selectedCell = null)}>Close</button>
        </div>
        <pre class="max-h-[65vh] overflow-auto p-4 font-mono type-caption">{result?.stdout ?? ""}{result?.stderr ?? ""}{result?.error ?? ""}</pre>
      </div>
    </div>
  {/if}

  {#if matrixConfirm}
    <ConfirmDanger
      title={matrixConfirm.title}
      body={matrixConfirm.body}
      severity={matrixConfirm.severity}
      productionHosts={matrixConfirm.productionHosts}
      requirePhrase={matrixConfirm.requirePhrase}
      onCancel={() => (matrixConfirm = null)}
      onConfirm={() => runMatrix()}
    />
  {/if}
</div>
