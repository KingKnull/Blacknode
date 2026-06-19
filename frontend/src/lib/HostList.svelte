<script lang="ts">
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { HostService, MoshService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Host } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import { bus } from "./events";
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
    Clock,
    Star,
    Wifi,
  } from "@lucide/svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";

  let editing: Host | null = $state(null);
  let creating = $state(false);
  let importing = $state(false);
  let filter = $state("");

  // Mosh availability — checked once, gates the context-menu entry.
  let moshAvailable = $state(false);
  onMount(async () => {
    try { moshAvailable = await MoshService.Available(); } catch { moshAvailable = false; }
  });

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
        (h.group ?? "").toLowerCase().includes(f) ||
        (h.tags ?? []).some((t) => t.toLowerCase().includes(f))
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

  // Favorites — pinned hosts, always shown at the very top (respecting the
  // active filter so search still narrows them).
  let favoriteHosts = $derived(
    visible.filter((h) => h.favorite).sort((a, b) => a.name.localeCompare(b.name)),
  );

  // Recently connected — derived purely from the existing lastConnectedAt
  // timestamp. Only shown when not actively filtering, so it stays a
  // shortcut rather than clutter.
  let recentHosts = $derived(
    filter
      ? []
      : app.hosts
          .filter((h) => h.lastConnectedAt > 0)
          .sort((a, b) => b.lastConnectedAt - a.lastConnectedAt)
          .slice(0, 5),
  );

  async function toggleFavorite(h: Host) {
    try {
      await HostService.SetFavorite(h.id, !h.favorite);
      await app.refreshHosts();
    } catch (e: any) {
      app.toast("error", "Couldn't update favorite", String(e?.message ?? e));
    }
  }

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
    const offMetrics = Events.On("metrics:update", (e: any) => {
      const m = e?.data;
      if (!m || !m.hostID) return;
      app.hostMetrics[m.hostID] = {
        cpuPercent: m.cpuPercent ?? 0,
        memPercent: m.memPercent ?? 0,
        diskPercent: m.diskPercent ?? 0,
      };
    });
    // "+ New → New host" from the section tab bar opens the editor here.
    const offNewHost = bus.on("new-host", () => (creating = true));
    return () => {
      offMetrics();
      offNewHost();
    };
  });

  function metricColor(pct: number): string {
    if (pct > 85) return 'var(--color-danger)';
    if (pct > 60) return 'var(--color-warn)';
    return 'var(--color-accent)';
  }
</script>

<div class="flex h-full w-full flex-col">
  <!-- Header -->
  <div class="flex items-center gap-2 border-b hairline px-3 py-2.5">
    <span class="type-title text-[var(--color-text-1)]">Hosts</span>
    <span class="rounded-full bg-[var(--color-surface-3)] px-1.5 type-micro tabular text-[var(--color-text-4)]">{app.hosts.length}</span>
    <button
      class="ml-auto flex h-6 w-6 items-center justify-center rounded-md text-[var(--color-text-4)] transition-colors hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-2)]"
      onclick={() => (importing = true)}
      title="Import from ~/.ssh/config"
    >
      <FileText size="14" />
    </button>
    <button
      class="flex h-6 w-6 items-center justify-center rounded-md bg-[var(--color-accent)] text-white transition-opacity hover:opacity-90"
      onclick={() => (creating = true)}
      title="New host"
    >
      <Plus size="14" />
    </button>
  </div>

  <!-- Search -->
  <div class="px-2 py-2">
    <div class="relative flex items-center rounded-md border hairline bg-[var(--color-surface-2)] transition-colors focus-within:border-[var(--color-accent)]/60">
      <Search size="13" class="absolute left-2.5 text-[var(--color-text-4)]" />
      <input
        class="w-full bg-transparent py-1.5 pl-8 pr-2 type-caption text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)]"
        placeholder="Search hosts, tags…"
        bind:value={filter}
      />
    </div>
  </div>

  <!-- Reusable host row — shared by Recent and grouped lists. -->
  {#snippet hostRow(h: Host)}
    {@const Icon = authIcon(h.authMethod)}
    {@const env = envBadge(h.environment)}
    <div
      class="group relative mx-2 my-px flex items-center gap-2 overflow-hidden border px-2 py-2 transition-colors duration-150 {app.connectedHosts.has(h.id)
        ? 'border-[var(--color-accent)]/30 bg-[var(--color-accent-soft)] text-[var(--color-text-1)]'
        : app.selectedHostID === h.id
        ? 'border-[var(--color-accent)]/40 bg-[var(--color-accent-soft)] text-[var(--color-text-1)]'
        : 'border-transparent text-[var(--color-text-2)] hover:bg-[var(--color-surface-2)]'}"
      style="border-radius: var(--radius-md);"
    >
      <!-- Env stripe -->
      {#if env.label}
        <span class="absolute inset-y-1 left-0 w-[3px] rounded-full" style:background={env.color}></span>
      {/if}

      <button
        class="flex min-w-0 flex-1 items-start gap-2.5 text-left"
        onclick={() => { app.selectedHostID = h.id; app.hostDetailOpen = true; }}
      >
        <!-- Status dot -->
        <span class="mt-1.5 shrink-0">
          {#if app.connectedHosts.has(h.id)}
            <span class="block h-2 w-2 rounded-full bg-[var(--color-success)] pulse-soft"></span>
          {:else if app.selectedHostID === h.id}
            <span class="block h-2 w-2 rounded-full bg-[var(--color-accent)]"></span>
          {:else}
            <span class="block h-2 w-2 rounded-full border border-[var(--color-line-strong)]"></span>
          {/if}
        </span>
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-1.5">
            <span class="truncate type-body font-medium leading-tight text-[var(--color-text-1)]">{h.name}</span>
            {#if env.label}
              <span
                class="shrink-0 rounded px-1 type-micro font-semibold"
                style:color={env.color}
                style:background={env.bg}
              >{env.label}</span>
            {/if}
            <Icon size="10" class="shrink-0 text-[var(--color-text-4)]" />
          </div>
          <div class="clamp-1 font-mono type-caption text-[var(--color-text-3)]">
            {h.username}@{h.host}<span class="text-[var(--color-text-4)]">:{h.port}</span>
          </div>
          {#if h.tags && h.tags.length > 0}
            <div class="mt-1 flex flex-wrap gap-1">
              {#each h.tags.slice(0, 4) as tag (tag)}
                <span class="rounded bg-[var(--color-surface-3)] px-1.5 py-px type-micro text-[var(--color-text-3)]">{tag}</span>
              {/each}
            </div>
          {/if}
          {#if app.hostMetrics[h.id]}
            {@const m = app.hostMetrics[h.id]}
            <div class="mt-1 flex items-center gap-2 type-micro text-[var(--color-text-4)]">
              {#each [['C', m.cpuPercent], ['M', m.memPercent], ['D', m.diskPercent]] as [label, pct] (label)}
                <span class="flex items-center gap-1">
                  <span class="font-semibold">{label}</span>
                  <span class="inline-block h-[3px] w-[22px] overflow-hidden rounded-full bg-[var(--color-surface-3)]">
                    <span class="block h-full rounded-full transition-all duration-500" style:width="{Math.min(pct as number, 100)}%" style:background={metricColor(pct as number)}></span>
                  </span>
                </span>
              {/each}
            </div>
          {/if}
        </div>
      </button>

      <!-- ⋯ action button — always visible, tappable on any device -->
      <button
        class="flex h-6 w-6 shrink-0 items-center justify-center rounded-md border border-transparent text-[var(--color-text-4)] transition-colors hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-2)] focus-visible:border-[var(--color-accent)]/50"
        onclick={(e) => openMenu(e, h)}
        title="Actions"
        aria-label="Host actions"
      >
        <MoreVertical size="13" />
      </button>
    </div>
  {/snippet}

  {#snippet sectionHeader(label: string, count: number, Icon?: any)}
    <div class="flex items-center gap-2 px-3 pt-3 pb-1">
      {#if Icon}<Icon size="11" class="text-[var(--color-text-4)]" />{/if}
      <span class="type-eyebrow text-[var(--color-text-4)]">{label}</span>
      <span class="rounded-full bg-[var(--color-surface-3)] px-1.5 type-micro tabular text-[var(--color-text-4)]">{count}</span>
      <span class="h-px flex-1 bg-[var(--color-line)]"></span>
    </div>
  {/snippet}

  <!-- Host list -->
  <div class="flex-1 overflow-y-auto pb-2">
    {#if favoriteHosts.length > 0}
      {@render sectionHeader("Favorites", favoriteHosts.length, Star)}
      {#each favoriteHosts as h (h.id)}
        {@render hostRow(h)}
      {/each}
    {/if}

    {#if recentHosts.length > 0}
      {@render sectionHeader("Recent", recentHosts.length, Clock)}
      {#each recentHosts as h (h.id)}
        {@render hostRow(h)}
      {/each}
    {/if}

    {#each Object.entries(groups) as [name, list] (name)}
      {@render sectionHeader(name, list.length)}
      {#each list as h (h.id)}
        {@render hostRow(h)}
      {/each}
    {/each}

    {#if app.hosts.length === 0}
      <div class="px-4 py-10 text-center">
        <div class="mx-auto mb-3 flex h-12 w-12 items-center justify-center rounded-lg border hairline bg-[var(--color-surface-2)] text-[var(--color-text-4)]">
          <Server size="20" />
        </div>
        <p class="type-body font-medium text-[var(--color-text-2)]">No hosts yet</p>
        <p class="mt-1 type-caption text-[var(--color-text-4)]">Add your first server to get started</p>
        <button
          class="mt-4 rounded-md bg-[var(--color-accent)] px-3 py-1.5 type-caption font-medium text-white transition-opacity hover:opacity-90"
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
    class="fade-up fixed z-50 min-w-[150px] overflow-hidden border hairline-strong surface-2 py-1 type-caption shadow-xl shadow-black/40"
    style="left: {menuPos.x}px; top: {menuPos.y}px; border-radius: var(--radius-md);"
  >
    <button
      class="flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-[var(--color-text-2)] transition-colors hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
      onclick={() => {
        if (menuHost) bus.emit("connect-host", { hostID: menuHost.id });
        closeMenu();
      }}
    >
      <Server size="13" /> Connect
    </button>
    {#if moshAvailable}
      <button
        class="flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-[var(--color-success)] transition-colors hover:bg-[var(--color-success)]/10"
        onclick={() => {
          if (menuHost) bus.emit("connect-host-mosh", { hostID: menuHost.id });
          closeMenu();
        }}
      >
        <Wifi size="13" /> Connect via Mosh
      </button>
    {/if}
    <button
      class="flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-[var(--color-text-2)] transition-colors hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
      onclick={() => {
        if (menuHost) void toggleFavorite(menuHost);
        closeMenu();
      }}
    >
      <Star size="13" class={menuHost?.favorite ? "fill-[var(--color-warn)] text-[var(--color-warn)]" : ""} />
      {menuHost?.favorite ? "Unfavorite" : "Favorite"}
    </button>
    <button
      class="flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-[var(--color-text-2)] transition-colors hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
      onclick={() => {
        if (menuHost) editing = menuHost;
        closeMenu();
      }}
    >
      <Pencil size="13" /> Edit
    </button>
    <button
      class="flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-[var(--color-text-2)] transition-colors hover:bg-[var(--color-danger)]/10 hover:text-[var(--color-danger)]"
      onclick={() => {
        if (menuHost) hostToDelete = menuHost;
        closeMenu();
      }}
    >
      <Trash2 size="13" /> Delete
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
