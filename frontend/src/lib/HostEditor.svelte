<script lang="ts">
  import { HostService } from "../../bindings/github.com/blacknode/blacknode";
  import type { Host } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import { Server, X, Loader2 } from "@lucide/svelte";

  type Props = {
    host?: Host | null;
    onclose: () => void;
    onsaved: () => void;
  };
  let { host, onclose, onsaved }: Props = $props();

  // svelte-ignore state_referenced_locally
  let name = $state(host?.name ?? "");
  // svelte-ignore state_referenced_locally
  let hostName = $state(host?.host ?? "");
  // svelte-ignore state_referenced_locally
  let port = $state(host?.port ?? 22);
  // svelte-ignore state_referenced_locally
  let username = $state(host?.username ?? "");
  // svelte-ignore state_referenced_locally
  let authMethod = $state(host?.authMethod ?? "password");
  // svelte-ignore state_referenced_locally
  let keyID = $state(host?.keyID ?? "");
  // svelte-ignore state_referenced_locally
  let group = $state(host?.group ?? "");
  // svelte-ignore state_referenced_locally
  let environment = $state(host?.environment ?? "");
  // svelte-ignore state_referenced_locally
  let notes = $state(host?.notes ?? "");
  let busy = $state(false);
  let err = $state("");

  async function save() {
    err = "";
    if (!name || !hostName || !username) {
      err = "Name, host, and username are required";
      return;
    }
    busy = true;
    try {
      if (host?.id) {
        await HostService.Update({
          ...host,
          name,
          host: hostName,
          port,
          username,
          authMethod,
          keyID: authMethod === "key" ? keyID : "",
          group,
          environment,
          notes,
        } as Host);
      } else {
        await HostService.Create({
          name,
          host: hostName,
          port,
          username,
          authMethod,
          keyID: authMethod === "key" ? keyID : "",
          group,
          environment,
          notes,
          tags: [],
        } as unknown as Host);
      }
      await app.refreshHosts();
      onsaved();
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = false;
    }
  }
</script>

<div
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
  role="presentation"
  onclick={(e) => {
    if (e.target === e.currentTarget) onclose();
  }}
  onkeydown={(e) => e.key === "Escape" && onclose()}
>
  <div
    class="w-[520px] overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/50"
  >
    <div class="flex items-center gap-2 border-b hairline px-5 py-3">
      <Server size="14" class="text-[var(--color-accent)]" />
      <h3 class="text-sm font-semibold">
        {host ? "Edit host" : "New host"}
      </h3>
      <button
        class="ml-auto rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={onclose}
        title="Close"
      >
        <X size="14" />
      </button>
    </div>

    <div class="space-y-3 p-5 text-sm">
      <label class="block">
        <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
          >Name</span
        >
        <input
          class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
          bind:value={name}
          placeholder="prod-web-1"
        />
      </label>
      <div class="grid grid-cols-[1fr_88px] gap-2">
        <label class="block">
          <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >Host</span
          >
          <input
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            bind:value={hostName}
            placeholder="10.0.0.5"
          />
        </label>
        <label class="block">
          <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >Port</span
          >
          <input
            type="number"
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            bind:value={port}
          />
        </label>
      </div>
      <div class="grid grid-cols-2 gap-2">
        <label class="block">
          <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >User</span
          >
          <input
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            bind:value={username}
          />
        </label>
        <label class="block">
          <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >Auth method</span
          >
          <select
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            bind:value={authMethod}
          >
            <option value="password">password</option>
            <option value="key">key</option>
            <option value="agent">agent</option>
          </select>
        </label>
      </div>
      {#if authMethod === "key"}
        <label class="block">
          <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >Key</span
          >
          <select
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            bind:value={keyID}
          >
            <option value="">— select —</option>
            {#each app.keys as k (k.id)}
              <option value={k.id}>{k.name} ({k.keyType})</option>
            {/each}
          </select>
        </label>
      {/if}
      <div class="grid grid-cols-2 gap-2">
        <label class="block">
          <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >Group</span
          >
          <input
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            bind:value={group}
            placeholder="web · db · cache"
          />
        </label>
        <label class="block">
          <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >Environment</span
          >
          <select
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            class:!border-red-500={environment === "production"}
            bind:value={environment}
          >
            <option value="">— none —</option>
            <option value="dev">dev</option>
            <option value="staging">staging</option>
            <option value="production">production ⚠</option>
          </select>
        </label>
      </div>
      <label class="block">
        <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
          >Notes</span
        >
        <textarea
          class="mt-1 h-16 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
          bind:value={notes}
        ></textarea>
      </label>

      {#if err}
        <p class="text-xs text-[var(--color-danger)]">{err}</p>
      {/if}
    </div>

    <div class="flex items-center justify-end gap-2 border-t hairline px-5 py-3">
      <button
        class="rounded-md px-3 py-1.5 text-xs text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={onclose}>Cancel</button
      >
      <button
        class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
        disabled={busy}
        onclick={save}
      >
        {#if busy}
          <Loader2 size="12" class="animate-spin" />Saving…
        {:else}
          Save host
        {/if}
      </button>
    </div>
  </div>
</div>
