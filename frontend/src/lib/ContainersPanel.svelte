<script lang="ts">
  import { ContainerService } from "../../bindings/github.com/blacknode/blacknode";
  import type {
    Container,
    Pod,
  } from "../../bindings/github.com/blacknode/blacknode/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import {
    Container as ContainerIcon,
    Boxes,
    RefreshCw,
    ScrollText,
    X,
    Loader2,
    Play as PlayIcon,
    Square,
  } from "@lucide/svelte";

  type Tab = "docker" | "k8s";
  let tab = $state<Tab>("docker");

  let containers = $state<Container[]>([]);
  let pods = $state<Pod[]>([]);
  let namespaces = $state<string[]>([]);
  let namespace = $state(""); // empty = all namespaces

  let loading = $state(false);
  let err = $state("");
  let includeStopped = $state(false);

  let logs = $state<{ title: string; body: string; loading: boolean } | null>(null);

  let host = $derived(
    app.selectedHostID ? app.hosts.find((h) => h.id === app.selectedHostID) : null,
  );

  async function loadContainers() {
    if (!host) return;
    loading = true;
    err = "";
    try {
      const password = app.hostPasswords[host.id] ?? "";
      containers = ((await ContainerService.Containers(
        host.id,
        password,
        includeStopped,
      )) ?? []) as Container[];
    } catch (e: any) {
      err = String(e?.message ?? e);
      containers = [];
    } finally {
      loading = false;
    }
  }

  async function loadNamespaces() {
    if (!host) return;
    try {
      const password = app.hostPasswords[host.id] ?? "";
      namespaces = ((await ContainerService.Namespaces(host.id, password)) ??
        []) as string[];
    } catch {
      namespaces = [];
    }
  }

  async function loadPods() {
    if (!host) return;
    loading = true;
    err = "";
    try {
      const password = app.hostPasswords[host.id] ?? "";
      pods = ((await ContainerService.Pods(host.id, password, namespace)) ??
        []) as Pod[];
    } catch (e: any) {
      err = String(e?.message ?? e);
      pods = [];
    } finally {
      loading = false;
    }
  }

  async function showContainerLogs(c: Container) {
    if (!host) return;
    logs = { title: `${c.name} (${c.id})`, body: "", loading: true };
    try {
      const password = app.hostPasswords[host.id] ?? "";
      logs.body = (await ContainerService.ContainerLogs(
        host.id,
        password,
        c.id,
        500,
      )) as string;
    } catch (e: any) {
      logs.body = String(e?.message ?? e);
    } finally {
      logs.loading = false;
    }
  }

  async function showPodLogs(p: Pod) {
    if (!host) return;
    logs = { title: `${p.namespace}/${p.name}`, body: "", loading: true };
    try {
      const password = app.hostPasswords[host.id] ?? "";
      logs.body = (await ContainerService.PodLogs(
        host.id,
        password,
        p.namespace,
        p.name,
        "",
        500,
      )) as string;
    } catch (e: any) {
      logs.body = String(e?.message ?? e);
    } finally {
      logs.loading = false;
    }
  }

  // Reload when host or tab changes.
  $effect(() => {
    if (!host) {
      containers = [];
      pods = [];
      namespaces = [];
      return;
    }
    if (tab === "docker") void loadContainers();
    if (tab === "k8s") {
      void loadNamespaces();
      void loadPods();
    }
  });

  function statusColor(s: string) {
    const lower = s.toLowerCase();
    if (lower === "running" || lower.startsWith("up")) return "text-[var(--color-accent)]";
    if (lower === "pending" || lower.startsWith("creating")) return "text-[var(--color-warn)]";
    if (lower === "succeeded") return "text-[var(--color-info)]";
    if (lower === "failed" || lower.startsWith("exited") || lower.startsWith("error"))
      return "text-[var(--color-danger)]";
    return "text-[var(--color-text-3)]";
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Boxes}
    title="Containers"
    subtitle={host ? `Docker + Kubernetes on ${host.name}` : "Pick a host to inspect"}
  >
    {#snippet actions()}
      <button
        class="flex items-center gap-1 rounded-md border hairline-strong px-2.5 py-1 text-[11px] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
        disabled={!host || loading}
        onclick={() => (tab === "docker" ? loadContainers() : loadPods())}
      >
        {#if loading}<Loader2 size="11" class="animate-spin" />{:else}<RefreshCw
            size="11"
          />{/if}
        refresh
      </button>
    {/snippet}
  </PageHeader>

  {#if !host}
    <div class="flex flex-1 items-center justify-center">
      <div class="text-center">
        <Boxes size="22" class="mx-auto text-[var(--color-text-4)]" />
        <p class="mt-2 text-xs text-[var(--color-text-3)]">
          Select a host on the left, then list its containers or pods. Commands
          run remotely over SSH — no local docker/kubectl install needed.
        </p>
      </div>
    </div>
  {:else}
    <div class="flex items-center gap-1 border-b hairline surface-1 px-3 py-1.5">
      <button
        class="flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[11px] {tab ===
        'docker'
          ? 'bg-[var(--color-surface-3)] text-[var(--color-text-1)]'
          : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-1)]'}"
        onclick={() => (tab = "docker")}
      >
        <ContainerIcon size="11" /> Docker
      </button>
      <button
        class="flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[11px] {tab ===
        'k8s'
          ? 'bg-[var(--color-surface-3)] text-[var(--color-text-1)]'
          : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-1)]'}"
        onclick={() => (tab = "k8s")}
      >
        <Boxes size="11" /> Kubernetes
      </button>

      {#if tab === "docker"}
        <label
          class="ml-auto flex items-center gap-1.5 text-[11px] text-[var(--color-text-3)]"
        >
          <input
            type="checkbox"
            class="accent-[var(--color-accent)]"
            bind:checked={includeStopped}
            onchange={loadContainers}
          />
          show stopped
        </label>
      {:else if tab === "k8s"}
        <select
          class="ml-auto rounded-md border hairline bg-[var(--color-surface-3)] px-2 py-1 text-[11px] outline-none"
          bind:value={namespace}
          onchange={loadPods}
        >
          <option value="">(all namespaces)</option>
          {#each namespaces as ns (ns)}
            <option value={ns}>{ns}</option>
          {/each}
        </select>
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

      {#if tab === "docker"}
        {#if containers.length === 0 && !loading && !err}
          <div class="p-6 text-center text-xs text-[var(--color-text-3)]">
            no containers running on this host
          </div>
        {:else}
          <table class="w-full text-xs">
            <thead
              class="sticky top-0 surface-1 text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >
              <tr>
                <th class="px-3 py-2 text-left font-medium">Name</th>
                <th class="px-3 py-2 text-left font-medium">Image</th>
                <th class="px-3 py-2 text-left font-medium">Status</th>
                <th class="px-3 py-2 text-left font-medium">Ports</th>
                <th class="px-3 py-2 text-left font-medium">ID</th>
                <th class="w-16"></th>
              </tr>
            </thead>
            <tbody>
              {#each containers as c (c.id)}
                <tr class="border-b hairline hover:bg-[var(--color-surface-2)]">
                  <td class="truncate px-3 py-1.5">{c.name}</td>
                  <td
                    class="truncate px-3 py-1.5 font-mono text-[10px] text-[var(--color-text-3)]"
                    >{c.image}</td
                  >
                  <td class="px-3 py-1.5 {statusColor(c.state || c.status)}">
                    <span class="flex items-center gap-1">
                      {#if (c.state || c.status).toLowerCase().startsWith("running") || (c.state || c.status).toLowerCase().startsWith("up")}
                        <PlayIcon size="9" class="fill-current" />
                      {:else}
                        <Square size="9" class="fill-current" />
                      {/if}
                      {c.status}
                    </span>
                  </td>
                  <td
                    class="truncate px-3 py-1.5 font-mono text-[10px] text-[var(--color-text-3)]"
                    >{c.ports}</td
                  >
                  <td
                    class="px-3 py-1.5 font-mono text-[10px] text-[var(--color-text-4)]"
                    >{c.id}</td
                  >
                  <td class="px-2 py-1.5">
                    <button
                      class="flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
                      onclick={() => showContainerLogs(c)}
                      title="View logs"
                    >
                      <ScrollText size="10" /> logs
                    </button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      {:else if tab === "k8s"}
        {#if pods.length === 0 && !loading && !err}
          <div class="p-6 text-center text-xs text-[var(--color-text-3)]">
            no pods in {namespace || "any namespace"}
          </div>
        {:else}
          <table class="w-full text-xs">
            <thead
              class="sticky top-0 surface-1 text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >
              <tr>
                <th class="px-3 py-2 text-left font-medium">Pod</th>
                <th class="px-3 py-2 text-left font-medium">Namespace</th>
                <th class="px-3 py-2 text-left font-medium">Ready</th>
                <th class="px-3 py-2 text-left font-medium">Status</th>
                <th class="px-3 py-2 text-right font-medium">Restarts</th>
                <th class="px-3 py-2 text-left font-medium">Age</th>
                <th class="px-3 py-2 text-left font-medium">Node</th>
                <th class="w-16"></th>
              </tr>
            </thead>
            <tbody>
              {#each pods as p (p.namespace + "/" + p.name)}
                <tr class="border-b hairline hover:bg-[var(--color-surface-2)]">
                  <td class="truncate px-3 py-1.5">{p.name}</td>
                  <td
                    class="px-3 py-1.5 font-mono text-[10px] text-[var(--color-text-3)]"
                    >{p.namespace}</td
                  >
                  <td class="px-3 py-1.5 font-mono text-[10px]">{p.ready}</td>
                  <td class="px-3 py-1.5 {statusColor(p.status)}">{p.status}</td>
                  <td
                    class="px-3 py-1.5 text-right font-mono text-[10px] {p.restarts >
                    0
                      ? 'text-[var(--color-warn)]'
                      : 'text-[var(--color-text-3)]'}">{p.restarts}</td
                  >
                  <td
                    class="px-3 py-1.5 font-mono text-[10px] text-[var(--color-text-3)]"
                    >{p.age}</td
                  >
                  <td
                    class="truncate px-3 py-1.5 font-mono text-[10px] text-[var(--color-text-3)]"
                    >{p.node}</td
                  >
                  <td class="px-2 py-1.5">
                    <button
                      class="flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
                      onclick={() => showPodLogs(p)}
                    >
                      <ScrollText size="10" /> logs
                    </button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      {/if}
    </div>
  {/if}
</div>

{#if logs}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) logs = null;
    }}
  >
    <div
      class="flex max-h-[85vh] w-[min(95vw,1100px)] flex-col overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/60"
    >
      <div class="flex items-center gap-2 border-b hairline px-4 py-2.5">
        <ScrollText size="14" class="text-[var(--color-accent)]" />
        <span class="truncate text-sm font-semibold">{logs.title}</span>
        <span class="ml-2 text-[10px] text-[var(--color-text-3)]">last 500 lines</span>
        <button
          class="ml-auto rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={() => (logs = null)}
        >
          <X size="14" />
        </button>
      </div>
      <div class="flex-1 overflow-auto bg-black/40 p-3">
        {#if logs.loading}
          <div
            class="flex h-32 items-center justify-center gap-2 text-xs text-[var(--color-text-3)]"
          >
            <Loader2 size="14" class="animate-spin" /> fetching logs…
          </div>
        {:else}
          <pre
            class="overflow-x-auto whitespace-pre-wrap font-mono text-[11px] text-[var(--color-text-1)]">{logs.body || "(no output)"}</pre>
        {/if}
      </div>
    </div>
  </div>
{/if}

