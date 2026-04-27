<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { MetricsService } from "../../bindings/github.com/blacknode/blacknode";
  import type { HostMetrics } from "../../bindings/github.com/blacknode/blacknode/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import {
    Activity,
    Server,
    Play,
    Square,
    Cpu,
    MemoryStick,
    HardDrive,
    Network,
  } from "@lucide/svelte";

  type Series = {
    cpu: number[];
    mem: number[];
    disk: number[];
    // Combined rx + tx bytes/sec, used for the network sparkline.
    netRate: number[];
    // Track the rolling max so the network sparkline auto-scales — unlike
    // CPU/MEM/DISK which have a fixed 0–100% range, network throughput has
    // no natural ceiling.
    netMax: number;
  };
  const HISTORY = 60;

  let latest = $state<Record<string, HostMetrics>>({});
  let history = $state<Record<string, Series>>({});
  let polling = $state<Set<string>>(new Set());
  let off: (() => void) | undefined;

  onMount(() => {
    off = Events.On("metrics:update", (e: any) => {
      const m: HostMetrics = e?.data;
      if (!m) return;
      latest[m.hostID] = m;
      const s = history[m.hostID] ?? {
        cpu: [],
        mem: [],
        disk: [],
        netRate: [],
        netMax: 1,
      };
      s.cpu = [...s.cpu, m.cpuPercent].slice(-HISTORY);
      s.mem = [...s.mem, m.memPercent].slice(-HISTORY);
      s.disk = [...s.disk, m.diskPercent].slice(-HISTORY);
      const totalRate = (m.rxBytesPerSec ?? 0) + (m.txBytesPerSec ?? 0);
      s.netRate = [...s.netRate, totalRate].slice(-HISTORY);
      // Rolling max with a small floor so the line isn't pinned to the top
      // when nothing's happening.
      s.netMax = Math.max(1024, ...s.netRate);
      history[m.hostID] = s;
    });
  });

  onDestroy(() => {
    off?.();
    for (const id of polling) {
      void MetricsService.Stop(id);
    }
  });

  async function start(hostID: string) {
    const password = app.hostPasswords[hostID] ?? "";
    await MetricsService.Start(hostID, password, 5);
    polling = new Set([...polling, hostID]);
  }

  async function stop(hostID: string) {
    await MetricsService.Stop(hostID);
    const next = new Set(polling);
    next.delete(hostID);
    polling = next;
  }

  function spark(values: number[], max = 100) {
    if (values.length === 0) return "";
    const w = 220;
    const h = 36;
    const step = w / Math.max(1, HISTORY - 1);
    return values
      .map((v, i) => {
        const x = (i + (HISTORY - values.length)) * step;
        const y = h - (Math.min(v, max) / max) * h;
        return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(" ");
  }

  function sparkArea(values: number[], max = 100) {
    const path = spark(values, max);
    if (!path) return "";
    const w = 220;
    const h = 36;
    const lastX = ((HISTORY - 1) * w) / Math.max(1, HISTORY - 1);
    return `${path} L${lastX.toFixed(1)},${h} L0,${h} Z`;
  }

  function fmtBytes(n: number): string {
    if (!isFinite(n) || n < 0) return "0 B/s";
    if (n < 1024) return `${n.toFixed(0)} B/s`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB/s`;
    if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(2)} MB/s`;
    return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB/s`;
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Activity}
    title="Metrics"
    subtitle="Live host telemetry — CPU, memory, disk"
  />

  <div class="grid flex-1 grid-cols-2 gap-3 overflow-y-auto p-4">
    {#each app.hosts as h (h.id)}
      {@const m = latest[h.id]}
      {@const s = history[h.id]}
      {@const isPolling = polling.has(h.id)}
      <div
        class="overflow-hidden rounded-lg border hairline surface-2 transition-colors {isPolling
          ? 'border-[var(--color-accent)]/30'
          : ''}"
      >
        <div class="flex items-center gap-2 border-b hairline px-4 py-2.5">
          <Server size="13" class="text-[var(--color-text-3)]" />
          <div class="min-w-0 flex-1">
            <div class="truncate text-sm font-medium">{h.name}</div>
            <div class="truncate text-[10px] text-[var(--color-text-3)]">
              {h.username}@{h.host}
            </div>
          </div>
          {#if isPolling}
            <span
              class="h-1.5 w-1.5 rounded-full bg-[var(--color-accent)] pulse-soft"
            ></span>
            <button
              class="flex items-center gap-1 rounded-md border hairline-strong px-2 py-1 text-[11px] text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)]"
              onclick={() => stop(h.id)}
            >
              <Square size="10" />stop
            </button>
          {:else}
            <button
              class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-2 py-1 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90"
              onclick={() => start(h.id)}
            >
              <Play size="10" />start
            </button>
          {/if}
        </div>

        {#if m?.error}
          <div class="px-4 py-3 text-xs text-[var(--color-danger)]">{m.error}</div>
        {:else if m}
          <div class="grid grid-cols-4 divide-x divide-[var(--color-line)]">
            <div class="p-3">
              <div class="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-[var(--color-text-3)]">
                <Cpu size="10" /> CPU
              </div>
              <div class="mt-0.5 font-mono text-base text-[var(--color-accent)]">
                {m.cpuPercent.toFixed(1)}<span class="text-xs">%</span>
              </div>
              {#if s}
                <svg viewBox="0 0 220 36" class="mt-1 h-9 w-full text-[var(--color-accent)]">
                  <path d={sparkArea(s.cpu)} fill="currentColor" opacity="0.12"></path>
                  <path d={spark(s.cpu)} fill="none" stroke="currentColor" stroke-width="1.5"></path>
                </svg>
              {/if}
            </div>
            <div class="p-3">
              <div class="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-[var(--color-text-3)]">
                <MemoryStick size="10" /> MEM
              </div>
              <div class="mt-0.5 font-mono text-base text-[var(--color-info)]">
                {m.memPercent.toFixed(1)}<span class="text-xs">%</span>
              </div>
              {#if s}
                <svg viewBox="0 0 220 36" class="mt-1 h-9 w-full text-[var(--color-info)]">
                  <path d={sparkArea(s.mem)} fill="currentColor" opacity="0.12"></path>
                  <path d={spark(s.mem)} fill="none" stroke="currentColor" stroke-width="1.5"></path>
                </svg>
              {/if}
            </div>
            <div class="p-3">
              <div class="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-[var(--color-text-3)]">
                <HardDrive size="10" /> DISK
              </div>
              <div class="mt-0.5 font-mono text-base text-[var(--color-warn)]">
                {m.diskPercent.toFixed(1)}<span class="text-xs">%</span>
              </div>
              {#if s}
                <svg viewBox="0 0 220 36" class="mt-1 h-9 w-full text-[var(--color-warn)]">
                  <path d={sparkArea(s.disk)} fill="currentColor" opacity="0.12"></path>
                  <path d={spark(s.disk)} fill="none" stroke="currentColor" stroke-width="1.5"></path>
                </svg>
              {/if}
            </div>
            <div class="p-3">
              <div class="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-[var(--color-text-3)]">
                <Network size="10" /> NET
              </div>
              <div class="mt-0.5 flex flex-col font-mono text-[11px] text-[var(--color-text-1)]">
                <span title="Receive rate">
                  <span class="text-[var(--color-accent)]">↓</span>
                  {fmtBytes(m.rxBytesPerSec ?? 0)}
                </span>
                <span title="Transmit rate">
                  <span class="text-[var(--color-warn)]">↑</span>
                  {fmtBytes(m.txBytesPerSec ?? 0)}
                </span>
              </div>
              {#if s && s.netRate.length > 0}
                <svg viewBox="0 0 220 36" class="mt-1 h-9 w-full" style="color: #a855f7">
                  <path
                    d={sparkArea(s.netRate, s.netMax)}
                    fill="currentColor"
                    opacity="0.12"
                  ></path>
                  <path
                    d={spark(s.netRate, s.netMax)}
                    fill="none"
                    stroke="currentColor"
                    stroke-width="1.5"
                  ></path>
                </svg>
              {/if}
            </div>
          </div>
          <div class="border-t hairline px-4 py-1.5 text-[10px] text-[var(--color-text-3)]">
            load1 <span class="font-mono">{m.loadAvg1.toFixed(2)}</span>
            <span class="ml-3">{new Date(m.timestamp * 1000).toLocaleTimeString()}</span>
          </div>
        {:else}
          <div class="px-4 py-6 text-center text-xs text-[var(--color-text-3)]">
            Click <span class="font-medium text-[var(--color-text-2)]">start</span> to poll.
          </div>
        {/if}
      </div>
    {/each}
    {#if app.hosts.length === 0}
      <div class="col-span-2 flex h-full items-center justify-center">
        <div class="text-center">
          <Activity size="22" class="mx-auto text-[var(--color-text-4)]" />
          <p class="mt-2 text-xs text-[var(--color-text-3)]">
            Add a host to see metrics.
          </p>
        </div>
      </div>
    {/if}
  </div>
</div>
