<script lang="ts">
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { HostService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Host } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import HostEditor from "./HostEditor.svelte";
  import SSHConfigImport from "./SSHConfigImport.svelte";
  import { envBadge } from "./envColor";
  import {
    Search,
    Plus,
    Server,
    Pencil,
    Trash2,
    KeyRound,
    Lock,
    FileText,
    MoreVertical,
  } from "@lucide/svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";

  let editing: Host | null = $state(null);
  let creating = $state(false);
  let importing = $state(false);
  let filter = $state("");

  // Per-host action menu
  let menuHostID = $state<string | null>(null);
  let menuPos = $state<{ x: number; y: number }>({ x: 0, y: 0 });

  function openMenu(e: MouseEvent, h: Host) {
    e.stopPropagation();
    menuHostID = h.id;
    menuPos = { x: e.clientX, y: e.clientY };
  }

  function closeMenu() {
    menuHostID = null;
  }

  let visible = $derived(
    app.hosts.filter((h) => {
      if (!filter) return true;
      const f = filter.toLowerCase();
      return (
        h.name.toLowerCase().includes(f) ||
        h.host.toLowerCase().includes(f) ||
        (h.group ?? "").toLowerCase().includes(f)
      );
    }),
  );

  let groups = $derived(
    visible.reduce<Record<string, Host[]>>((acc, h) => {
      const g = h.group || "Ungrouped";
      (acc[g] ??= []).push(h);
      return acc;
    }, {}),
  );

  let hostToDelete = $state<Host | null>(null);

  async function deleteHost() {
    if (!hostToDelete) return;
    await HostService.Delete(hostToDelete.id);
    if (app.selectedHostID === hostToDelete.id) app.selectedHostID = null;
    await app.refreshHosts();
    hostToDelete = null;
  }

  const authIcon = (m: string) => {
    if (m === "key") return KeyRound;
    if (m === "agent") return Lock;
    return Lock;
  };

  // Listen for metrics:update events and store in app state.
  onMount(() => {
    return Events.On("metrics:update", (e: any) => {
      const m = e?.data;
      if (!m || !m.hostID) return;
      app.hostMetrics[m.hostID] = {
        cpuPercent: m.cpuPercent ?? 0,
        memPercent: m.memPercent ?? 0,
        diskPercent: m.diskPercent ?? 0,
      };
    });
  });

  function metricColor(pct: number): string {
    if (pct > 85) return 'var(--color-danger)';
    if (pct > 60) return 'var(--color-warn)';
    return 'var(--color-accent)';
  }
</script>

<div class="flex h-full w-full flex-col">
  <!-- Header -->
  <div class="flex items-center gap-2 border-b hairline px-3 py-2">
    <span class="text-xs font-semibold text-[var(--color-text-3)]">Hosts</span>
    <button
      class="ml-auto flex h-5 w-5 items-center justify-center border border-[var(--color-line)] text-[var(--color-text-4)] hover:border-[var(--color-accent)]/40 hover:text-[var(--color-accent)] transition-all"
      onclick={() => (importing = true)}
      title="Import from ~/.ssh/config"
    >
      <FileText size="10" />
    </button>
    <button
      class="flex h-5 w-5 items-center justify-center border border-[var(--color-line)] text-[var(--color-text-4)] hover:border-[var(--color-accent)]/40 hover:text-[var(--color-accent)] transition-all"
      onclick={() => (creating = true)}
      title="New host"
    >
      <Plus size="10" />
    </button>
  </div>

  <!-- Search -->
  <div class="border-b hairline px-2 py-2">
    <div class="relative flex items-center border hairline surface-2 focus-within:border-[var(--color-accent)]/50 transition-colors">
      <Search size="10" class="absolute left-2 text-[var(--color-text-4)]" />
      <input
        class="w-full bg-transparent py-1.5 pl-6 pr-2 font-mono text-xs outline-none placeholder:text-[var(--color-text-4)] text-[var(--color-text-2)]"
        placeholder="Filter hosts..."
        bind:value={filter}
      />
    </div>
  </div>

  <!-- Host list -->
  <div class="flex-1 overflow-y-auto pb-2">
    {#each Object.entries(groups) as [name, list] (name)}
      <div class="px-3 pt-3 pb-1 flex items-center gap-2">
        <span class="h-px flex-1 bg-[var(--color-line)]"></span>
        <span class="text-[10px] text-[var(--color-text-4)] font-mono">{name}</span>
        <span class="h-px flex-1 bg-[var(--color-line)]"></span>
      </div>
      {#each list as h (h.id)}
        {@const Icon = authIcon(h.authMethod)}
        {@const env = envBadge(h.environment)}
        <div
          class="group relative mx-2 my-px flex items-center gap-2 overflow-hidden border border-transparent px-2 py-1.5 transition-all duration-150 {app.selectedHostID === h.id
            ? 'border-[var(--color-accent)]/25 bg-[var(--color-accent)]/6 text-[var(--color-text-1)]'
            : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-2)] hover:translate-x-0.5'}"
        >
          <!-- Env stripe -->
          {#if env.label}
            <span class="absolute inset-y-0 left-0 w-[2px]" style:background={env.color}></span>
          {/if}
          <!-- Active glow -->
          {#if app.selectedHostID === h.id}
            <span class="pointer-events-none absolute inset-0" style="box-shadow: inset 1px 0 0 var(--color-accent), inset 0 0 12px rgba(0,255,136,0.04);"></span>
          {/if}

          <button
            class="flex min-w-0 flex-1 items-center gap-2 text-left"
            onclick={() => (app.selectedHostID = h.id)}
          >
            <!-- Status dot -->
            {#if app.connectedHosts.has(h.id)}
              <span class="h-1.5 w-1.5 shrink-0 bg-[var(--color-success)] pulse-soft shadow-[0_0_4px_var(--color-success)] rounded-full"></span>
            {:else if app.selectedHostID === h.id}
              <span class="h-1.5 w-1.5 shrink-0 bg-[var(--color-accent)] phosphor-flicker shadow-[0_0_4px_var(--color-accent)] rounded-full"></span>
            {:else}
              <span class="h-1 w-1 shrink-0 bg-[var(--color-text-4)] rounded-full"></span>
            {/if}
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-1.5 truncate font-mono text-[11px] leading-tight">
                <span class="truncate">{h.name}</span>
                {#if env.label}
                  <span
                    class="shrink-0 px-1 font-mono text-[8px] font-bold uppercase"
                    style:color={env.color}
                    style:background={env.bg}
                    style:border="1px solid {env.border}"
                  >{env.label}</span>
                {/if}
              </div>
              <div class="truncate font-mono text-[9px] text-[var(--color-text-4)]">
                {h.username}@{h.host}:{h.port}
              </div>
              {#if app.hostMetrics[h.id]}
                {@const m = app.hostMetrics[h.id]}
                <div class="mt-0.5 flex items-center gap-1.5 font-mono text-[8px] text-[var(--color-text-4)]">
                  <span class="flex items-center gap-0.5">
                    <span class="uppercase">C</span>
                    <span class="inline-block h-[3px] w-[20px] rounded-full bg-[var(--color-surface-3)] overflow-hidden">
                      <span class="block h-full rounded-full transition-all duration-500" style:width="{Math.min(m.cpuPercent, 100)}%" style:background={metricColor(m.cpuPercent)}></span>
                    </span>
                  </span>
                  <span class="flex items-center gap-0.5">
                    <span class="uppercase">M</span>
                    <span class="inline-block h-[3px] w-[20px] rounded-full bg-[var(--color-surface-3)] overflow-hidden">
                      <span class="block h-full rounded-full transition-all duration-500" style:width="{Math.min(m.memPercent, 100)}%" style:background={metricColor(m.memPercent)}></span>
                    </span>
                  </span>
                  <span class="flex items-center gap-0.5">
                    <span class="uppercase">D</span>
                    <span class="inline-block h-[3px] w-[20px] rounded-full bg-[var(--color-surface-3)] overflow-hidden">
                      <span class="block h-full rounded-full transition-all duration-500" style:width="{Math.min(m.diskPercent, 100)}%" style:background={metricColor(m.diskPercent)}></span>
                    </span>
                  </span>
                </div>
              {/if}
            </div>
          </button>

          <!-- ⋯ action button — always visible, tappable on any device -->
          <button
            class="flex h-6 w-6 shrink-0 items-center justify-center border border-transparent text-[var(--color-text-4)] transition-all hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)] focus-visible:border-[var(--color-accent)]/50"
            onclick={(e) => openMenu(e, h)}
            title="Actions"
            aria-label="Host actions"
          >
            <MoreVertical size="11" />
          </button>
        </div>
      {/each}
    {/each}

    {#if app.hosts.length === 0}
      <div class="px-4 py-10 text-center">
        <div class="mx-auto mb-3 flex h-12 w-12 items-center justify-center border hairline text-[var(--color-text-4)]" style="box-shadow: 0 0 20px rgba(0,255,136,0.04);">
          <Server size="20" class="glow-pulse" />
        </div>
        <p class="text-xs text-[var(--color-text-3)] font-medium">No hosts yet</p>
        <p class="mt-1 text-xs text-[var(--color-text-4)]">Add your first server to get started</p>
        <button
          class="mt-3 border hairline px-3 py-1.5 text-xs text-[var(--color-accent)]/70 hover:border-[var(--color-accent)]/40 hover:text-[var(--color-accent)] hover:bg-[var(--color-accent)]/5 transition-all"
          onclick={() => (creating = true)}
        >+ Add host</button>
      </div>
    {/if}
  </div>
</div>

<!-- Action menu backdrop -->
{#if menuHostID}
  {@const menuHost = app.hosts.find((h) => h.id === menuHostID)}
  <div
    class="fixed inset-0 z-40"
    role="presentation"
    onclick={closeMenu}
    oncontextmenu={(e) => { e.preventDefault(); closeMenu(); }}
  ></div>
  <div
    class="fade-up fixed z-50 min-w-[140px] overflow-hidden border hairline-strong surface-2 py-0.5 font-mono text-[10px] shadow-xl shadow-black/60"
    style="left: {menuPos.x}px; top: {menuPos.y}px"
  >
    <button
      class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)] transition-colors"
      onclick={() => {
        if (menuHost) editing = menuHost;
        closeMenu();
      }}
    >
      <Pencil size="10" /> EDIT
    </button>
    <button
      class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-danger)] transition-colors"
      onclick={() => {
        if (menuHost) hostToDelete = menuHost;
        closeMenu();
      }}
    >
      <Trash2 size="10" /> DELETE
    </button>
  </div>
{/if}

{#if creating}
  <HostEditor onclose={() => (creating = false)} onsaved={() => (creating = false)} />
{/if}
{#if editing}
  <HostEditor host={editing} onclose={() => (editing = null)} onsaved={() => (editing = null)} />
{/if}
{#if importing}
  <SSHConfigImport
    onclose={() => (importing = false)}
    onimported={async (n) => {
      importing = false;
      await app.refreshHosts();
      if (n > 0) {
        app.toast('ok', 'IMPORT SUCCESSFUL', `Imported ${n} host${n === 1 ? "" : "s"} from ~/.ssh/config`);
      }
    }}
  />
{/if}

{#if hostToDelete}
  <ConfirmDanger
    title="DELETE HOST"
    body="Are you sure you want to delete host '{hostToDelete.name}'? All snippets, custom commands, and credentials associated with this host will be removed from the local vault."
    severity="warn"
    productionHosts={hostToDelete.environment === 'production' ? [hostToDelete.name] : []}
    onCancel={() => (hostToDelete = null)}
    onConfirm={deleteHost}
  />
{/if}
