<script lang="ts">
  import { HostService } from "../../bindings/github.com/blacknode/blacknode";
  import type { Host } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import HostEditor from "./HostEditor.svelte";
  import { envBadge } from "./envColor";
  import {
    Search,
    Plus,
    Server,
    Pencil,
    Trash2,
    KeyRound,
    Lock,
  } from "@lucide/svelte";

  let editing: Host | null = $state(null);
  let creating = $state(false);
  let filter = $state("");

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

  async function deleteHost(h: Host) {
    if (!confirm(`Delete host "${h.name}"?`)) return;
    await HostService.Delete(h.id);
    if (app.selectedHostID === h.id) app.selectedHostID = null;
    await app.refreshHosts();
  }

  const authIcon = (m: string) => {
    if (m === "key") return KeyRound;
    if (m === "agent") return Lock;
    return Lock;
  };
</script>

<div class="flex h-full w-full flex-col">
  <div class="flex items-center gap-2 border-b hairline px-3 py-2.5">
    <span
      class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
      >Hosts</span
    >
    <button
      class="ml-auto flex h-6 w-6 items-center justify-center rounded-md text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-accent)]"
      onclick={() => (creating = true)}
      title="New host"
    >
      <Plus size="12" />
    </button>
  </div>

  <div class="px-3 py-2">
    <div
      class="relative flex items-center rounded-md border hairline surface-2 focus-within:border-[var(--color-accent)]/40"
    >
      <Search size="12" class="absolute left-2.5 text-[var(--color-text-4)]" />
      <input
        class="w-full bg-transparent py-1.5 pl-7 pr-2 text-xs outline-none placeholder:text-[var(--color-text-4)]"
        placeholder="Search hosts…"
        bind:value={filter}
      />
    </div>
  </div>

  <div class="flex-1 overflow-y-auto pb-2">
    {#each Object.entries(groups) as [name, list] (name)}
      <div
        class="px-3 pt-3 pb-1 text-[9px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-4)]"
      >
        {name}
      </div>
      {#each list as h (h.id)}
        {@const Icon = authIcon(h.authMethod)}
        {@const env = envBadge(h.environment)}
        <div
          class="group relative mx-2 my-0.5 flex items-center gap-2 overflow-hidden rounded-md border border-transparent px-2 py-1.5 transition-colors {app.selectedHostID ===
          h.id
            ? 'border-[var(--color-accent)]/30 bg-[var(--color-accent-soft)] text-[var(--color-text-1)]'
            : 'text-[var(--color-text-2)] hover:bg-[var(--color-surface-2)]'}"
        >
          {#if env.label}
            <span
              class="absolute inset-y-0 left-0 w-0.5"
              style:background={env.color}
            ></span>
          {/if}
          <Server
            size="13"
            class={app.selectedHostID === h.id
              ? "text-[var(--color-accent)]"
              : "text-[var(--color-text-3)]"}
          />
          <button
            class="flex-1 truncate text-left"
            onclick={() => (app.selectedHostID = h.id)}
          >
            <div class="flex items-center gap-1.5 truncate text-xs">
              <span class="truncate">{h.name}</span>
              {#if env.label}
                <span
                  class="shrink-0 rounded-sm px-1 text-[8px] font-mono font-semibold"
                  style:color={env.color}
                  style:background={env.bg}
                  style:border="1px solid {env.border}"
                >
                  {env.label}
                </span>
              {/if}
              <Icon size="9" class="shrink-0 text-[var(--color-text-4)]" />
            </div>
            <div class="truncate text-[10px] text-[var(--color-text-3)]">
              {h.username}@{h.host}:{h.port}
            </div>
          </button>
          <div class="hidden gap-0.5 group-hover:flex">
            <button
              class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
              onclick={() => (editing = h)}
              title="Edit"
            >
              <Pencil size="10" />
            </button>
            <button
              class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
              onclick={() => deleteHost(h)}
              title="Delete"
            >
              <Trash2 size="10" />
            </button>
          </div>
        </div>
      {/each}
    {/each}
    {#if app.hosts.length === 0}
      <div class="px-4 py-8 text-center">
        <Server size="20" class="mx-auto text-[var(--color-text-4)]" />
        <p class="mt-2 text-[11px] text-[var(--color-text-3)]">
          No saved hosts yet
        </p>
        <button
          class="mt-3 rounded-md border hairline-strong px-2.5 py-1 text-[11px] text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)]"
          onclick={() => (creating = true)}>+ Add your first host</button
        >
      </div>
    {/if}
  </div>
</div>

{#if creating}
  <HostEditor
    onclose={() => (creating = false)}
    onsaved={() => (creating = false)}
  />
{/if}
{#if editing}
  <HostEditor
    host={editing}
    onclose={() => (editing = null)}
    onsaved={() => (editing = null)}
  />
{/if}
