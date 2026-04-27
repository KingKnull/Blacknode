<script lang="ts">
  import { NetworkService } from "../../bindings/github.com/blacknode/blacknode";
  import type {
    PingResult,
    DNSResult,
    PortScanResult,
    SSLResult,
  } from "../../bindings/github.com/blacknode/blacknode/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import {
    Radar,
    Globe,
    ScanLine,
    ShieldCheck,
    Loader2,
    Play as PlayIcon,
    Server,
    Check,
    X as XIcon,
    AlertTriangle,
  } from "@lucide/svelte";

  type Tool = "ping" | "dns" | "scan" | "ssl";
  let tool = $state<Tool>("ping");

  // Inputs (kept per-tool so switching doesn't lose state)
  let target = $state("");
  let pingCount = $state(4);
  let dnsType = $state("A");
  let portList = $state(
    "22, 80, 443, 3306, 5432, 6379, 8080, 8443, 9090, 9200",
  );
  let sslPort = $state(443);

  let busy = $state(false);
  let err = $state("");

  let pingResult = $state<PingResult | null>(null);
  let dnsResult = $state<DNSResult | null>(null);
  let scanResult = $state<PortScanResult | null>(null);
  let sslResult = $state<SSLResult | null>(null);

  let host = $derived(
    app.selectedHostID ? app.hosts.find((h) => h.id === app.selectedHostID) : null,
  );

  function clearResults() {
    pingResult = null;
    dnsResult = null;
    scanResult = null;
    sslResult = null;
    err = "";
  }

  function parsePorts(s: string): number[] {
    const out: number[] = [];
    for (const part of s.split(",")) {
      const t = part.trim();
      if (!t) continue;
      // support ranges like "1000-1010"
      if (t.includes("-")) {
        const [a, b] = t.split("-").map((x) => parseInt(x, 10));
        if (Number.isFinite(a) && Number.isFinite(b)) {
          for (let p = Math.min(a, b); p <= Math.max(a, b); p++) {
            if (p >= 1 && p <= 65535) out.push(p);
          }
        }
      } else {
        const p = parseInt(t, 10);
        if (Number.isFinite(p) && p >= 1 && p <= 65535) out.push(p);
      }
    }
    return [...new Set(out)];
  }

  async function run() {
    if (!host) return;
    if (!target.trim()) {
      err = "target required";
      return;
    }
    busy = true;
    clearResults();
    try {
      const password = app.hostPasswords[host.id] ?? "";
      if (tool === "ping") {
        pingResult = (await NetworkService.Ping(
          host.id,
          password,
          target,
          pingCount,
        )) as PingResult;
      } else if (tool === "dns") {
        dnsResult = (await NetworkService.DNSLookup(
          host.id,
          password,
          target,
          dnsType,
        )) as DNSResult;
      } else if (tool === "scan") {
        const ports = parsePorts(portList);
        if (ports.length === 0) {
          err = "no valid ports";
          return;
        }
        scanResult = (await NetworkService.PortScan(
          host.id,
          password,
          target,
          ports,
        )) as PortScanResult;
      } else if (tool === "ssl") {
        const t = target.includes(":") ? target : `${target}:${sslPort}`;
        sslResult = (await NetworkService.SSLCert(host.id, password, t)) as SSLResult;
      }
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = false;
    }
  }

  function fmtTime(unix: number) {
    if (!unix) return "";
    return new Date(unix * 1000).toLocaleString();
  }

  function expiryColor(days: number) {
    if (days < 0) return "text-[var(--color-danger)]";
    if (days < 14) return "text-[var(--color-danger)]";
    if (days < 30) return "text-[var(--color-warn)]";
    return "text-[var(--color-accent)]";
  }

  const TOOLS: { id: Tool; label: string; icon: any; placeholder: string }[] = [
    { id: "ping", label: "Ping", icon: Radar, placeholder: "host or IP — e.g. 1.1.1.1" },
    { id: "dns", label: "DNS lookup", icon: Globe, placeholder: "domain — e.g. example.com" },
    { id: "scan", label: "Port scan", icon: ScanLine, placeholder: "host or IP" },
    { id: "ssl", label: "SSL cert", icon: ShieldCheck, placeholder: "host or host:port" },
  ];
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Radar}
    title="Network diagnostics"
    subtitle={host
      ? `Probes run from ${host.name}'s network — see what the host sees`
      : "Pick a host to run diagnostics from"}
  />

  {#if !host}
    <div class="flex flex-1 items-center justify-center">
      <div class="text-center">
        <Radar size="22" class="mx-auto text-[var(--color-text-4)]" />
        <p class="mt-2 text-xs text-[var(--color-text-3)]">
          Select a host on the left. Ping/DNS/port-scan/SSL all run *through*
          that host, so you can probe internal services from a bastion.
        </p>
      </div>
    </div>
  {:else}
    <div class="flex items-center gap-1 border-b hairline surface-1 px-3 py-1.5">
      {#each TOOLS as t (t.id)}
        <button
          class="flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[11px] {tool ===
          t.id
            ? 'bg-[var(--color-surface-3)] text-[var(--color-text-1)]'
            : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-1)]'}"
          onclick={() => {
            tool = t.id;
            clearResults();
          }}
        >
          <t.icon size="11" />
          {t.label}
        </button>
      {/each}
      <span class="ml-auto flex items-center gap-1 text-[10px] text-[var(--color-text-3)]">
        <Server size="10" />
        {host.name}
      </span>
    </div>

    <div class="border-b hairline surface-1 px-4 py-3">
      <div class="flex items-end gap-2">
        <label class="flex flex-1 flex-col gap-1">
          <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >Target</span
          >
          <input
            class="rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-sm outline-none"
            placeholder={TOOLS.find((t) => t.id === tool)!.placeholder}
            bind:value={target}
            onkeydown={(e) => e.key === "Enter" && run()}
          />
        </label>

        {#if tool === "ping"}
          <label class="flex flex-col gap-1">
            <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Count</span
            >
            <input
              type="number"
              min="1"
              max="50"
              class="w-20 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
              bind:value={pingCount}
            />
          </label>
        {:else if tool === "dns"}
          <label class="flex flex-col gap-1">
            <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Record</span
            >
            <select
              class="w-24 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
              bind:value={dnsType}
            >
              {#each ["A", "AAAA", "MX", "TXT", "CNAME", "NS", "SOA", "SRV"] as t (t)}
                <option value={t}>{t}</option>
              {/each}
            </select>
          </label>
        {:else if tool === "ssl"}
          <label class="flex flex-col gap-1">
            <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Port</span
            >
            <input
              type="number"
              class="w-24 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
              bind:value={sslPort}
            />
          </label>
        {/if}

        <button
          class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
          disabled={busy || !target}
          onclick={run}
        >
          {#if busy}<Loader2 size="14" class="animate-spin" />{:else}<PlayIcon size="14" />{/if}
          Run
        </button>
      </div>

      {#if tool === "scan"}
        <label class="mt-2 block">
          <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >Ports — comma-separated, ranges like 1000-1010 supported</span
          >
          <input
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-xs outline-none"
            bind:value={portList}
          />
        </label>
      {/if}
    </div>

    <div class="flex-1 overflow-y-auto">
      {#if err}
        <div
          class="m-4 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-3 font-mono text-[11px] whitespace-pre-wrap text-[var(--color-danger)]"
        >
          {err}
        </div>
      {/if}

      {#if tool === "ping" && pingResult}
        <div class="m-4 space-y-3">
          <div class="grid grid-cols-4 gap-3">
            <div class="rounded-lg border hairline surface-2 p-3">
              <div class="text-[10px] uppercase tracking-wider text-[var(--color-text-3)]">
                Reachable
              </div>
              <div
                class="mt-0.5 text-base font-mono {pingResult.reachable
                  ? 'text-[var(--color-accent)]'
                  : 'text-[var(--color-danger)]'}"
              >
                {pingResult.reachable ? "yes" : "no"}
              </div>
            </div>
            <div class="rounded-lg border hairline surface-2 p-3">
              <div class="text-[10px] uppercase tracking-wider text-[var(--color-text-3)]">
                Loss
              </div>
              <div class="mt-0.5 font-mono text-base">
                {pingResult.lossPercent.toFixed(0)}%
              </div>
              <div class="text-[10px] text-[var(--color-text-3)]">
                {pingResult.lost} / {pingResult.sent}
              </div>
            </div>
            <div class="rounded-lg border hairline surface-2 p-3">
              <div class="text-[10px] uppercase tracking-wider text-[var(--color-text-3)]">
                Avg latency
              </div>
              <div class="mt-0.5 font-mono text-base">
                {pingResult.avgLatencyMs.toFixed(2)}<span class="text-xs"> ms</span>
              </div>
            </div>
            <div class="rounded-lg border hairline surface-2 p-3">
              <div class="text-[10px] uppercase tracking-wider text-[var(--color-text-3)]">
                Min / Max
              </div>
              <div class="mt-0.5 font-mono text-sm">
                {pingResult.minLatencyMs.toFixed(1)} / {pingResult.maxLatencyMs.toFixed(1)}
                <span class="text-xs">ms</span>
              </div>
            </div>
          </div>
          <details class="rounded-md border hairline surface-2">
            <summary
              class="cursor-pointer px-4 py-2 text-[11px] text-[var(--color-text-3)]"
              >raw output</summary
            >
            <pre
              class="overflow-x-auto bg-black/30 px-4 py-2 font-mono text-[11px] text-[var(--color-text-2)]">{pingResult.rawOutput}</pre>
          </details>
        </div>
      {/if}

      {#if tool === "dns" && dnsResult}
        <div class="m-4 space-y-3">
          {#if dnsResult.answers.length === 0}
            <div
              class="rounded-md border border-[var(--color-warn)]/30 bg-[var(--color-warn)]/10 p-3 text-xs text-[var(--color-warn)]"
            >
              No structured answers parsed — see raw output below.
            </div>
          {:else}
            <table class="w-full text-xs">
              <thead
                class="text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >
                <tr>
                  <th class="px-3 py-2 text-left font-medium">Type</th>
                  <th class="px-3 py-2 text-left font-medium">Value</th>
                </tr>
              </thead>
              <tbody>
                {#each dnsResult.answers as a, i (i)}
                  <tr class="border-b hairline">
                    <td
                      class="px-3 py-1.5 font-mono text-[11px] text-[var(--color-accent)]"
                      >{a.type}</td
                    >
                    <td class="px-3 py-1.5 font-mono">{a.value}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
          <details class="rounded-md border hairline surface-2">
            <summary
              class="cursor-pointer px-4 py-2 text-[11px] text-[var(--color-text-3)]"
              >raw output</summary
            >
            <pre
              class="overflow-x-auto bg-black/30 px-4 py-2 font-mono text-[11px] text-[var(--color-text-2)]">{dnsResult.rawOutput}</pre>
          </details>
        </div>
      {/if}

      {#if tool === "scan" && scanResult}
        <div class="m-4">
          <div class="mb-3 text-[11px] text-[var(--color-text-3)]">
            scanned {scanResult.results.length} ports on
            <span class="font-mono">{scanResult.target}</span>
            ·
            <span class="text-[var(--color-accent)]"
              >{scanResult.results.filter((r) => r.open).length} open</span
            >
            ·
            <span class="text-[var(--color-text-4)]"
              >{scanResult.results.filter((r) => !r.open).length} closed/filtered</span
            >
          </div>
          <table class="w-full text-xs">
            <thead
              class="text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >
              <tr>
                <th class="px-3 py-2 text-left font-medium">Port</th>
                <th class="px-3 py-2 text-left font-medium">Status</th>
                <th class="px-3 py-2 text-left font-medium">Latency</th>
                <th class="px-3 py-2 text-left font-medium">Banner</th>
              </tr>
            </thead>
            <tbody>
              {#each scanResult.results as r (r.port)}
                <tr
                  class="border-b hairline {r.open
                    ? ''
                    : 'opacity-50'}"
                >
                  <td class="px-3 py-1.5 font-mono">{r.port}</td>
                  <td class="px-3 py-1.5">
                    {#if r.open}
                      <span class="flex items-center gap-1 text-[var(--color-accent)]">
                        <Check size="10" /> open
                      </span>
                    {:else}
                      <span class="flex items-center gap-1 text-[var(--color-text-4)]">
                        <XIcon size="10" /> closed
                      </span>
                    {/if}
                  </td>
                  <td class="px-3 py-1.5 font-mono text-[10px] text-[var(--color-text-3)]">
                    {r.open ? `${r.latencyMs.toFixed(1)} ms` : ""}
                  </td>
                  <td
                    class="px-3 py-1.5 font-mono text-[10px] text-[var(--color-text-3)]"
                  >
                    {r.banner || ""}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}

      {#if tool === "ssl" && sslResult}
        <div class="m-4 space-y-3">
          {#if sslResult.error}
            <div
              class="rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-3 text-xs text-[var(--color-danger)]"
            >
              <AlertTriangle size="12" class="mb-1 inline" />
              {sslResult.error}
            </div>
          {:else if sslResult.handshakeOK}
            <div class="grid grid-cols-3 gap-3">
              <div class="rounded-lg border hairline surface-2 p-3">
                <div class="text-[10px] uppercase tracking-wider text-[var(--color-text-3)]">
                  TLS
                </div>
                <div class="mt-0.5 font-mono text-sm">{sslResult.tlsVersion}</div>
                <div class="mt-1 truncate font-mono text-[10px] text-[var(--color-text-3)]"
                  title={sslResult.cipherSuite}>
                  {sslResult.cipherSuite}
                </div>
              </div>
              <div class="rounded-lg border hairline surface-2 p-3">
                <div class="text-[10px] uppercase tracking-wider text-[var(--color-text-3)]">
                  Expires
                </div>
                <div class="mt-0.5 font-mono text-sm {expiryColor(sslResult.cert.daysUntilExpiry)}">
                  {sslResult.cert.daysUntilExpiry}d
                </div>
                <div class="mt-1 text-[10px] text-[var(--color-text-3)]">
                  {fmtTime(sslResult.cert.notAfter)}
                </div>
              </div>
              <div class="rounded-lg border hairline surface-2 p-3">
                <div class="text-[10px] uppercase tracking-wider text-[var(--color-text-3)]">
                  Issued
                </div>
                <div class="mt-0.5 font-mono text-[11px]">
                  {fmtTime(sslResult.cert.notBefore)}
                </div>
              </div>
            </div>

            <div class="rounded-lg border hairline surface-2 p-4">
              <div class="grid grid-cols-[140px_1fr] gap-y-2 text-xs">
                <span class="text-[var(--color-text-3)]">Subject</span>
                <span class="font-mono">{sslResult.cert.subject}</span>
                <span class="text-[var(--color-text-3)]">Issuer</span>
                <span class="font-mono">{sslResult.cert.issuer}</span>
                <span class="text-[var(--color-text-3)]">Serial</span>
                <span class="font-mono break-all text-[10px]"
                  >{sslResult.cert.serialNumber}</span
                >
                <span class="text-[var(--color-text-3)]">Fingerprint</span>
                <span class="font-mono break-all text-[10px]"
                  >{sslResult.cert.fingerprint}</span
                >
                <span class="text-[var(--color-text-3)]">DNS names</span>
                <span class="font-mono text-[11px]"
                  >{(sslResult.cert.dnsNames ?? []).join(", ") || "—"}</span
                >
                <span class="text-[var(--color-text-3)]">Chain</span>
                <span class="space-y-0.5 text-[11px]">
                  {#each sslResult.cert.chain as c, i (i)}
                    <div class="font-mono">
                      <span class="text-[var(--color-text-4)]">{i}.</span>
                      {c}
                    </div>
                  {/each}
                </span>
              </div>
            </div>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>
