<script lang="ts">
  import { onMount } from "svelte";
  import { PortForwardService } from "../../bindings/github.com/blacknode/blacknode";
  import type { ActiveForward } from "../../bindings/github.com/blacknode/blacknode/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import {
    Network,
    Plus,
    Play,
    Square,
    Trash2,
    X,
    Loader2,
    ArrowRight,
    ArrowLeft,
    Globe,
  } from "@lucide/svelte";

  let forwards = $state<ActiveForward[]>([]);
  let creating = $state(false);
  let busyID = $state<string | null>(null);
  let err = $state("");

  // create form
  let cName = $state("");
  let cKind = $state<"local" | "remote" | "dynamic">("local");
  let cHostID = $state("");
  let cLocalAddr = $state("127.0.0.1");
  let cLocalPort = $state(5432);
  let cRemoteAddr = $state("localhost");
  let cRemotePort = $state(5432);
  let saving = $state(false);

  onMount(refresh);

  async function refresh() {
    try {
      forwards = ((await PortForwardService.List()) ??
        []) as ActiveForward[];
    } catch (e: any) {
      err = String(e?.message ?? e);
    }
  }

  async function start(f: ActiveForward) {
    busyID = f.id;
    err = "";
    try {
      const password = app.hostPasswords[f.hostID] ?? "";
      await PortForwardService.Start(f.id, password);
      await refresh();
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busyID = null;
    }
  }

  async function stop(f: ActiveForward) {
    busyID = f.id;
    try {
      await PortForwardService.Stop(f.id);
      await refresh();
    } finally {
      busyID = null;
    }
  }

  async function del(f: ActiveForward) {
    if (!confirm(`Delete forward "${f.name}"?`)) return;
    await PortForwardService.Delete(f.id);
    await refresh();
  }

  async function save() {
    err = "";
    if (!cName || !cHostID) {
      err = "Name and host required";
      return;
    }
    saving = true;
    try {
      await PortForwardService.Create({
        name: cName,
        hostID: cHostID,
        kind: cKind,
        localAddr: cLocalAddr,
        localPort: cLocalPort,
        remoteAddr: cKind === "dynamic" ? "" : cRemoteAddr,
        remotePort: cKind === "dynamic" ? 0 : cRemotePort,
        autoStart: false,
      } as any);
      creating = false;
      cName = "";
      await refresh();
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      saving = false;
    }
  }

  function applyPreset(name: string) {
    if (name === "postgres") {
      cKind = "local";
      cLocalPort = 5432;
      cRemoteAddr = "localhost";
      cRemotePort = 5432;
      cName = cName || "postgres";
    } else if (name === "mysql") {
      cKind = "local";
      cLocalPort = 3306;
      cRemoteAddr = "localhost";
      cRemotePort = 3306;
      cName = cName || "mysql";
    } else if (name === "redis") {
      cKind = "local";
      cLocalPort = 6379;
      cRemoteAddr = "localhost";
      cRemotePort = 6379;
      cName = cName || "redis";
    } else if (name === "socks") {
      cKind = "dynamic";
      cLocalPort = 1080;
      cName = cName || "socks";
    }
  }

  function hostName(id: string) {
    return app.hosts.find((h) => h.id === id)?.name ?? id.slice(0, 8);
  }

  function kindIcon(kind: string) {
    if (kind === "remote") return ArrowLeft;
    if (kind === "dynamic") return Globe;
    return ArrowRight;
  }

  function kindLabel(f: ActiveForward) {
    if (f.kind === "dynamic") return `socks5 ${f.localAddr}:${f.localPort}`;
    if (f.kind === "remote")
      return `remote ${f.remotePort} → ${f.localAddr}:${f.localPort}`;
    return `${f.localAddr}:${f.localPort} → ${f.remoteAddr}:${f.remotePort}`;
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Network}
    title="Port forwards"
    subtitle="Local, remote, and SOCKS5 tunnels — survive across reconnects"
  >
    {#snippet actions()}
      <button
        class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-2.5 py-1 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90"
        onclick={() => (creating = true)}
      >
        <Plus size="11" /> new forward
      </button>
    {/snippet}
  </PageHeader>

  {#if err}
    <div
      class="m-4 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-3 text-xs text-[var(--color-danger)]"
    >
      {err}
    </div>
  {/if}

  <div class="flex-1 overflow-y-auto p-4">
    <div class="space-y-2">
      {#each forwards as f (f.id)}
        {@const Icon = kindIcon(f.kind)}
        <div
          class="flex items-center gap-3 rounded-lg border hairline surface-2 p-3 {f.active
            ? 'border-[var(--color-accent)]/30'
            : ''}"
        >
          <span
            class="h-2 w-2 rounded-full {f.active
              ? 'bg-[var(--color-accent)] pulse-soft'
              : 'bg-[var(--color-text-4)]'}"
          ></span>
          <Icon size="14" class="text-[var(--color-text-3)]" />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2 text-sm">
              <span class="font-medium">{f.name}</span>
              <span
                class="rounded-sm border hairline px-1 text-[9px] uppercase tracking-wider text-[var(--color-text-3)]"
                >{f.kind}</span
              >
            </div>
            <div class="font-mono text-[10px] text-[var(--color-text-3)]">
              {hostName(f.hostID)} · {kindLabel(f)}
            </div>
          </div>
          {#if f.active}
            <button
              class="flex items-center gap-1 rounded-md border hairline-strong px-2 py-1 text-[11px] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
              disabled={busyID === f.id}
              onclick={() => stop(f)}
            >
              {#if busyID === f.id}<Loader2 size="11" class="animate-spin" />{:else}<Square
                  size="10"
                />{/if}
              stop
            </button>
          {:else}
            <button
              class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-2 py-1 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
              disabled={busyID === f.id}
              onclick={() => start(f)}
            >
              {#if busyID === f.id}<Loader2 size="11" class="animate-spin" />{:else}<Play
                  size="10"
                />{/if}
              start
            </button>
          {/if}
          <button
            class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
            onclick={() => del(f)}
            title="Delete"
          >
            <Trash2 size="11" />
          </button>
        </div>
      {/each}
    </div>

    {#if forwards.length === 0 && !creating}
      <div class="flex h-full items-center justify-center">
        <div class="text-center">
          <Network size="22" class="mx-auto text-[var(--color-text-4)]" />
          <p class="mt-2 text-xs text-[var(--color-text-3)]">
            No port forwards yet. Click "new forward" to create one.
          </p>
        </div>
      </div>
    {/if}
  </div>
</div>

{#if creating}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) creating = false;
    }}
  >
    <div
      class="w-[560px] overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/50"
    >
      <div class="flex items-center gap-2 border-b hairline px-5 py-3">
        <Network size="14" class="text-[var(--color-accent)]" />
        <h3 class="text-sm font-semibold">New port forward</h3>
        <button
          class="ml-auto rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={() => (creating = false)}
        >
          <X size="14" />
        </button>
      </div>

      <div class="space-y-3 p-5 text-sm">
        <div class="flex flex-wrap items-center gap-1.5 text-[10px]">
          <span class="text-[var(--color-text-3)]">presets:</span>
          {#each ["postgres", "mysql", "redis", "socks"] as p (p)}
            <button
              class="rounded-md border hairline-strong px-2 py-0.5 hover:bg-[var(--color-surface-3)]"
              onclick={() => applyPreset(p)}>{p}</button
            >
          {/each}
        </div>

        <label class="block">
          <span
            class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >Name</span
          >
          <input
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            bind:value={cName}
            placeholder="prod-db-tunnel"
          />
        </label>
        <div class="grid grid-cols-2 gap-2">
          <label class="block">
            <span
              class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Host</span
            >
            <select
              class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
              bind:value={cHostID}
            >
              <option value="">— select —</option>
              {#each app.hosts as h (h.id)}
                <option value={h.id}>{h.name}</option>
              {/each}
            </select>
          </label>
          <label class="block">
            <span
              class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Kind</span
            >
            <select
              class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
              bind:value={cKind}
            >
              <option value="local">local — bind here, dial through SSH</option>
              <option value="remote">remote — bind on host, dial back here</option
              >
              <option value="dynamic">dynamic — SOCKS5 proxy</option>
            </select>
          </label>
        </div>
        <div class="grid grid-cols-[1fr_88px] gap-2">
          <label class="block">
            <span
              class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Local bind</span
            >
            <input
              class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono outline-none"
              bind:value={cLocalAddr}
              placeholder="127.0.0.1"
            />
          </label>
          <label class="block">
            <span
              class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Port</span
            >
            <input
              type="number"
              class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
              bind:value={cLocalPort}
            />
          </label>
        </div>
        {#if cKind !== "dynamic"}
          <div class="grid grid-cols-[1fr_88px] gap-2">
            <label class="block">
              <span
                class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
                >Remote target</span
              >
              <input
                class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono outline-none"
                bind:value={cRemoteAddr}
                placeholder="localhost"
              />
            </label>
            <label class="block">
              <span
                class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
                >Port</span
              >
              <input
                type="number"
                class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
                bind:value={cRemotePort}
              />
            </label>
          </div>
        {/if}
      </div>

      <div class="flex items-center justify-end gap-2 border-t hairline px-5 py-3">
        <button
          class="rounded-md px-3 py-1.5 text-xs text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={() => (creating = false)}>Cancel</button
        >
        <button
          class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
          disabled={saving || !cName || !cHostID}
          onclick={save}
        >
          {#if saving}<Loader2 size="11" class="animate-spin" />{:else}Save{/if}
        </button>
      </div>
    </div>
  </div>
{/if}
