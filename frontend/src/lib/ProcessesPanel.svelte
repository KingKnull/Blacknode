<script lang="ts">
  import { ProcessService } from "../../bindings/github.com/blacknode/blacknode";
  import type {
    ProcessInfo,
    SystemdUnit,
  } from "../../bindings/github.com/blacknode/blacknode/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";
  import {
    Activity,
    Cpu,
    RefreshCw,
    Search,
    Skull,
    Loader2,
    Server,
    Cog,
    Play as PlayIcon,
    Square,
    RotateCcw,
    Check,
    Circle,
    AlertTriangle,
  } from "@lucide/svelte";

  type Tab = "procs" | "services";
  let tab = $state<Tab>("procs");

  let procs = $state<ProcessInfo[]>([]);
  let units = $state<SystemdUnit[]>([]);
  let filter = $state("");
  let loading = $state(false);
  let err = $state("");

  let useSudo = $state(false);
  let killing = $state<number | null>(null);
  let pendingKill: ProcessInfo | null = $state(null);

  let serviceBusy = $state<string | null>(null);
  let serviceLog: { unit: string; body: string } | null = $state(null);

  type SortKey = "cpu" | "mem" | "rss" | "pid" | "user" | "command";
  let sortKey = $state<SortKey>("cpu");
  let sortDir = $state<"asc" | "desc">("desc");

  let host = $derived(
    app.selectedHostID ? app.hosts.find((h) => h.id === app.selectedHostID) : null,
  );

  async function refresh() {
    if (!host) return;
    loading = true;
    err = "";
    try {
      const password = app.hostPasswords[host.id] ?? "";
      if (tab === "procs") {
        procs = ((await ProcessService.Top(host.id, password, 200)) ??
          []) as ProcessInfo[];
      } else {
        units = ((await ProcessService.Services(host.id, password)) ??
          []) as SystemdUnit[];
      }
    } catch (e: any) {
      err = String(e?.message ?? e);
      if (tab === "procs") procs = [];
      else units = [];
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    if (host) void refresh();
    else {
      procs = [];
      units = [];
    }
  });

  $effect(() => {
    void tab;
    if (host) void refresh();
  });

  let visibleProcs = $derived(() => {
    let out = procs;
    if (filter) {
      const f = filter.toLowerCase();
      out = out.filter(
        (p) =>
          p.command.toLowerCase().includes(f) ||
          p.user.toLowerCase().includes(f) ||
          String(p.pid).includes(f),
      );
    }
    const dir = sortDir === "desc" ? -1 : 1;
    return [...out].sort((a, b) => {
      switch (sortKey) {
        case "cpu":
          return dir * (a.cpuPct - b.cpuPct);
        case "mem":
          return dir * (a.memPct - b.memPct);
        case "rss":
          return dir * (a.rssKB - b.rssKB);
        case "pid":
          return dir * (a.pid - b.pid);
        case "user":
          return dir * a.user.localeCompare(b.user);
        case "command":
          return dir * a.command.localeCompare(b.command);
      }
    });
  });

  let visibleUnits = $derived(() => {
    if (!filter) return units;
    const f = filter.toLowerCase();
    return units.filter(
      (u) =>
        u.name.toLowerCase().includes(f) ||
        u.description.toLowerCase().includes(f),
    );
  });

  function flipSort(k: SortKey) {
    if (sortKey === k) sortDir = sortDir === "desc" ? "asc" : "desc";
    else {
      sortKey = k;
      sortDir = k === "user" || k === "command" ? "asc" : "desc";
    }
  }

  function fmtRSS(kb: number) {
    if (kb < 1024) return `${kb} K`;
    if (kb < 1024 * 1024) return `${(kb / 1024).toFixed(1)} M`;
    return `${(kb / 1024 / 1024).toFixed(2)} G`;
  }

  function dangerLevel(p: ProcessInfo): "block-without-confirm" | "warn" {
    if (p.pid < 1000 || p.user === "root") return "block-without-confirm";
    return "warn";
  }

  async function doKill(p: ProcessInfo, signal: string) {
    if (!host) return;
    killing = p.pid;
    pendingKill = null;
    try {
      const password = app.hostPasswords[host.id] ?? "";
      await ProcessService.Kill(host.id, password, p.pid, signal, useSudo);
      await refresh();
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      killing = null;
    }
  }

  async function unitAction(u: SystemdUnit, action: string) {
    if (!host) return;
    serviceBusy = u.name + ":" + action;
    err = "";
    try {
      const password = app.hostPasswords[host.id] ?? "";
      const out = (await ProcessService.ServiceAction(
        host.id,
        password,
        u.name,
        action,
        useSudo,
      )) as string;
      if (action === "status") {
        serviceLog = { unit: u.name, body: out };
      }
      await refresh();
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      serviceBusy = null;
    }
  }

  function activeColor(state: string) {
    const s = state.toLowerCase();
    if (s === "active") return "text-[var(--color-accent)]";
    if (s === "failed") return "text-[var(--color-danger)]";
    if (s === "activating" || s === "deactivating") return "text-[var(--color-warn)]";
    return "text-[var(--color-text-3)]";
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Activity}
    title="Processes"
    subtitle={host
      ? `top + systemd on ${host.name}`
      : "Pick a host to inspect remote processes"}
  >
    {#snippet actions()}
      <label
        class="flex items-center gap-1.5 rounded-md border hairline-strong px-2.5 py-1 text-[11px] text-[var(--color-text-2)]"
        title="Run kill / systemctl with `sudo -n` (requires passwordless sudo)"
      >
        <input
          type="checkbox"
          class="accent-[var(--color-accent)]"
          bind:checked={useSudo}
        />
        sudo
      </label>
      <button
        class="flex items-center gap-1 rounded-md border hairline-strong px-2.5 py-1 text-[11px] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
        disabled={!host || loading}
        onclick={refresh}
      >
        {#if loading}
          <Loader2 size="11" class="animate-spin" />
        {:else}
          <RefreshCw size="11" />
        {/if}
        refresh
      </button>
    {/snippet}
  </PageHeader>

  {#if !host}
    <div class="flex flex-1 items-center justify-center">
      <div class="text-center">
        <Activity size="22" class="mx-auto text-[var(--color-text-4)]" />
        <p class="mt-2 text-xs text-[var(--color-text-3)]">
          Select a host to view its top processes and systemd units. All
          operations run remotely via SSH.
        </p>
      </div>
    </div>
  {:else}
    <div class="flex items-center gap-1 border-b hairline surface-1 px-3 py-1.5">
      <button
        class="flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[11px] {tab ===
        'procs'
          ? 'bg-[var(--color-surface-3)] text-[var(--color-text-1)]'
          : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-1)]'}"
        onclick={() => (tab = "procs")}
      >
        <Cpu size="11" /> Processes
      </button>
      <button
        class="flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[11px] {tab ===
        'services'
          ? 'bg-[var(--color-surface-3)] text-[var(--color-text-1)]'
          : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-1)]'}"
        onclick={() => (tab = "services")}
      >
        <Cog size="11" /> systemd services
      </button>

      <div class="ml-auto flex items-center gap-2">
        <div
          class="relative flex items-center rounded-md border hairline bg-[var(--color-surface-3)]"
        >
          <Search size="11" class="absolute left-2 text-[var(--color-text-4)]" />
          <input
            class="w-56 bg-transparent py-1 pl-7 pr-2 text-xs outline-none placeholder:text-[var(--color-text-4)]"
            placeholder={tab === "procs" ? "filter cmd / user / pid…" : "filter unit / description…"}
            bind:value={filter}
          />
        </div>
        <span class="flex items-center gap-1 text-[10px] text-[var(--color-text-3)]">
          <Server size="10" />
          {host.name}
        </span>
      </div>
    </div>

    {#if err}
      <div
        class="mx-4 mt-3 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-3 font-mono text-[11px] whitespace-pre-wrap text-[var(--color-danger)]"
      >
        {err}
      </div>
    {/if}

    <div class="flex-1 overflow-y-auto">
      {#if tab === "procs"}
        {@const list = visibleProcs()}
        <table class="w-full text-xs">
          <thead
            class="sticky top-0 z-10 surface-1 text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]"
          >
            <tr>
              <th class="cursor-pointer px-3 py-2 text-left font-medium hover:text-[var(--color-text-1)]"
                onclick={() => flipSort("pid")}
                >PID {sortKey === "pid" ? (sortDir === "desc" ? "↓" : "↑") : ""}</th
              >
              <th
                class="cursor-pointer px-3 py-2 text-left font-medium hover:text-[var(--color-text-1)]"
                onclick={() => flipSort("user")}
                >User {sortKey === "user" ? (sortDir === "desc" ? "↓" : "↑") : ""}</th
              >
              <th
                class="cursor-pointer px-3 py-2 text-right font-medium hover:text-[var(--color-text-1)]"
                onclick={() => flipSort("cpu")}
                >CPU% {sortKey === "cpu" ? (sortDir === "desc" ? "↓" : "↑") : ""}</th
              >
              <th
                class="cursor-pointer px-3 py-2 text-right font-medium hover:text-[var(--color-text-1)]"
                onclick={() => flipSort("mem")}
                >MEM% {sortKey === "mem" ? (sortDir === "desc" ? "↓" : "↑") : ""}</th
              >
              <th
                class="cursor-pointer px-3 py-2 text-right font-medium hover:text-[var(--color-text-1)]"
                onclick={() => flipSort("rss")}
                >RSS {sortKey === "rss" ? (sortDir === "desc" ? "↓" : "↑") : ""}</th
              >
              <th class="px-3 py-2 text-left font-medium">State</th>
              <th class="px-3 py-2 text-left font-medium">Time</th>
              <th
                class="cursor-pointer px-3 py-2 text-left font-medium hover:text-[var(--color-text-1)]"
                onclick={() => flipSort("command")}
                >Command {sortKey === "command" ? (sortDir === "desc" ? "↓" : "↑") : ""}</th
              >
              <th class="w-10"></th>
            </tr>
          </thead>
          <tbody>
            {#each list as p (p.pid)}
              <tr
                class="border-b hairline hover:bg-[var(--color-surface-2)] {p.cpuPct >
                70
                  ? 'bg-[var(--color-warn)]/5'
                  : ''}"
              >
                <td class="px-3 py-1 font-mono">{p.pid}</td>
                <td
                  class="px-3 py-1 font-mono text-[10px] {p.user === 'root'
                    ? 'text-[var(--color-danger)]'
                    : 'text-[var(--color-text-3)]'}">{p.user}</td
                >
                <td class="px-3 py-1 text-right font-mono">{p.cpuPct.toFixed(1)}</td>
                <td class="px-3 py-1 text-right font-mono">{p.memPct.toFixed(1)}</td>
                <td
                  class="px-3 py-1 text-right font-mono text-[10px] text-[var(--color-text-3)]"
                  >{fmtRSS(p.rssKB)}</td
                >
                <td class="px-3 py-1 font-mono text-[10px] text-[var(--color-text-3)]"
                  >{p.state}</td
                >
                <td class="px-3 py-1 font-mono text-[10px] text-[var(--color-text-3)]"
                  >{p.startTime}</td
                >
                <td class="max-w-0 truncate px-3 py-1 font-mono text-[11px]"
                  title={p.command}>{p.command}</td
                >
                <td class="px-2 py-1">
                  <button
                    class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)] disabled:opacity-30"
                    title="Kill"
                    disabled={killing === p.pid}
                    onclick={() => (pendingKill = p)}
                  >
                    {#if killing === p.pid}
                      <Loader2 size="11" class="animate-spin" />
                    {:else}
                      <Skull size="11" />
                    {/if}
                  </button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
        {#if list.length === 0 && !loading}
          <div class="p-6 text-center text-xs text-[var(--color-text-3)]">
            no processes match the filter
          </div>
        {/if}
      {:else}
        {@const list = visibleUnits()}
        <table class="w-full text-xs">
          <thead
            class="sticky top-0 z-10 surface-1 text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]"
          >
            <tr>
              <th class="px-3 py-2 text-left font-medium">Unit</th>
              <th class="px-3 py-2 text-left font-medium">Active</th>
              <th class="px-3 py-2 text-left font-medium">Sub</th>
              <th class="px-3 py-2 text-left font-medium">Description</th>
              <th class="w-44"></th>
            </tr>
          </thead>
          <tbody>
            {#each list as u (u.name)}
              <tr class="border-b hairline hover:bg-[var(--color-surface-2)]">
                <td class="px-3 py-1.5 font-mono text-[11px]">{u.name}</td>
                <td class="px-3 py-1.5">
                  <span class="flex items-center gap-1 {activeColor(u.activeState)}">
                    {#if u.activeState === "active"}
                      <Circle size="8" class="fill-current" />
                    {:else if u.activeState === "failed"}
                      <AlertTriangle size="9" />
                    {:else}
                      <Square size="8" class="fill-current opacity-40" />
                    {/if}
                    {u.activeState}
                  </span>
                </td>
                <td class="px-3 py-1.5 font-mono text-[10px] text-[var(--color-text-3)]"
                  >{u.subState}</td
                >
                <td class="max-w-0 truncate px-3 py-1.5 text-[11px]"
                  title={u.description}>{u.description}</td
                >
                <td class="flex justify-end gap-0.5 px-2 py-1.5">
                  <button
                    class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-accent)] disabled:opacity-30"
                    title="Start"
                    disabled={serviceBusy === u.name + ":start"}
                    onclick={() => unitAction(u, "start")}
                  >
                    <PlayIcon size="11" />
                  </button>
                  <button
                    class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-accent)] disabled:opacity-30"
                    title="Restart"
                    disabled={serviceBusy === u.name + ":restart"}
                    onclick={() => unitAction(u, "restart")}
                  >
                    <RotateCcw size="11" />
                  </button>
                  <button
                    class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-danger)] disabled:opacity-30"
                    title="Stop"
                    disabled={serviceBusy === u.name + ":stop"}
                    onclick={() => unitAction(u, "stop")}
                  >
                    <Square size="11" />
                  </button>
                  <button
                    class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
                    title="Status"
                    onclick={() => unitAction(u, "status")}
                  >
                    <Check size="11" />
                  </button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
        {#if list.length === 0 && !loading}
          <div class="p-6 text-center text-xs text-[var(--color-text-3)]">
            {units.length === 0
              ? "no systemd units found — host may not run systemd"
              : "no units match the filter"}
          </div>
        {/if}
      {/if}
    </div>
  {/if}
</div>

{#if pendingKill}
  <ConfirmDanger
    title={`kill -${dangerLevel(pendingKill) === "block-without-confirm" ? "TERM" : "TERM"} ${pendingKill.pid}`}
    body={`Send TERM to ${pendingKill.command} (PID ${pendingKill.pid}, owner ${pendingKill.user}). The process will receive a chance to clean up before exit.`}
    severity={dangerLevel(pendingKill)}
    productionHosts={[]}
    requirePhrase={dangerLevel(pendingKill) === "block-without-confirm" ? "kill it" : undefined}
    onCancel={() => (pendingKill = null)}
    onConfirm={() => {
      const p = pendingKill!;
      void doKill(p, "TERM");
    }}
  />
{/if}

{#if serviceLog}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) serviceLog = null;
    }}
  >
    <div
      class="flex max-h-[80vh] w-[min(90vw,900px)] flex-col overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/60"
    >
      <div class="flex items-center gap-2 border-b hairline px-4 py-2.5">
        <Cog size="14" class="text-[var(--color-accent)]" />
        <span class="font-mono text-sm">{serviceLog.unit}</span>
        <button
          class="ml-auto rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={() => (serviceLog = null)}
        >
          ×
        </button>
      </div>
      <div class="flex-1 overflow-auto bg-[var(--color-code-bg)] p-3">
        <pre
          class="overflow-x-auto whitespace-pre-wrap font-mono text-[11px] text-[var(--color-text-1)]">{serviceLog.body || "(no output)"}</pre>
      </div>
    </div>
  </div>
{/if}
