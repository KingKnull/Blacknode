<script lang="ts">
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
</script>

<div class="flex h-full w-full flex-col">
  <!-- Header -->
  <div class="flex items-center gap-2 border-b hairline px-3 py-2">
    <span class="font-mono text-[10px] font-bold tracking-[0.2em] text-[var(--color-accent)]/70 uppercase">// HOSTS</span>
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
    <div
      class="relative flex items-center border hairline surface-2 focus-within:border-[var(--color-accent)]/50 transition-colors"
    >
      <Search size="10" class="absolute left-2 text-[var(--color-text-4)]" />
      <input
        class="w-full bg-transparent py-1 pl-6 pr-2 font-mono text-[10px] outline-none placeholder:text-[var(--color-text-4)] text-[var(--color-text-2)]"
        placeholder="FILTER HOSTS..."
        bind:value={filter}
      />
    </div>
  </div>

  <!-- Host list -->
  <div class="flex-1 overflow-y-auto pb-2">
    {#each Object.entries(groups) as [name, list] (name)}
      <div
        class="px-3 pt-3 pb-1 font-mono text-[8px] font-bold tracking-[0.2em] text-[var(--color-text-4)] uppercase"
      >
        [{name}]
      </div>
      {#each list as h (h.id)}
        {@const Icon = authIcon(h.authMethod)}
        {@const env = envBadge(h.environment)}
        <div
          class="group relative mx-2 my-px flex items-center gap-2 overflow-hidden border border-transparent px-2 py-1.5 transition-all {app.selectedHostID ===
          h.id
            ? 'border-[var(--color-accent)]/25 bg-[var(--color-accent)]/6 text-[var(--color-text-1)]'
            : 'text-[var(--color-text-3)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-2)]'}"
        >
          <!-- Env stripe -->
          {#if env.label}
            <span
              class="absolute inset-y-0 left-0 w-[2px]"
              style:background={env.color}
            ></span>
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
            {#if app.selectedHostID === h.id}
              <span class="h-1.5 w-1.5 shrink-0 bg-[var(--color-accent)] phosphor-flicker shadow-[0_0_4px_var(--color-accent)]"></span>
            {:else}
              <span class="h-1 w-1 shrink-0 bg-[var(--color-text-4)]"></span>
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
                  >
                    {env.label}
                  </span>
                {/if}
              </div>
              <div class="truncate font-mono text-[9px] text-[var(--color-text-4)]">
                {h.username}@{h.host}:{h.port}
              </div>
            </div>
          </button>

          <!-- Action buttons (hover + menu) -->
          <div class="flex items-center">
            <button
              class="flex h-6 w-6 items-center justify-center text-[var(--color-text-4)] hover:text-[var(--color-text-2)] group-hover:hidden"
              title="Actions"
            >
              <MoreVertical size="12" />
            </button>
            <div class="hidden gap-px group-hover:flex">
              <button
                class="border border-[var(--color-line)] p-1 text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
                onclick={() => (editing = h)}
                title="Edit"
              >
                <Pencil size="9" />
              </button>
              <button
                class="border border-[var(--color-line)] p-1 text-[var(--color-text-3)] hover:border-[var(--color-danger)]/40 hover:text-[var(--color-danger)] transition-all"
                onclick={() => (hostToDelete = h)}
                title="Delete"
              >
                <Trash2 size="9" />
              </button>
            </div>
          </div>
        </div>
      {/each}
    {/each}
    {#if app.hosts.length === 0}
      <div class="px-4 py-10 text-center">
        <div class="mx-auto mb-3 flex h-10 w-10 items-center justify-center border hairline text-[var(--color-text-4)]">
          <Server size="18" />
        </div>
        <p class="font-mono text-[10px] text-[var(--color-text-4)] uppercase tracking-widest">
          NO HOSTS
        </p>
        <button
          class="mt-3 border hairline px-3 py-1.5 font-mono text-[10px] text-[var(--color-accent)]/70 hover:border-[var(--color-accent)]/40 hover:text-[var(--color-accent)] transition-all uppercase tracking-widest"
          onclick={() => (creating = true)}>+ ADD HOST</button
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
