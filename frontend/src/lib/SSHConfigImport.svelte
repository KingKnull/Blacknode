<script lang="ts">
  import { onMount } from "svelte";
  import { HostService } from "../../bindings/github.com/blacknode/blacknode";
  import type { SSHConfigCandidate } from "../../bindings/github.com/blacknode/blacknode/models";
  import { app } from "./state.svelte";
  import {
    FileText,
    X,
    Loader2,
    Check,
    KeyRound,
    Lock,
    AlertTriangle,
    Server,
  } from "@lucide/svelte";

  type Props = {
    onclose: () => void;
    onimported: (count: number) => void;
  };
  let { onclose, onimported }: Props = $props();

  let candidates = $state<SSHConfigCandidate[]>([]);
  let selected = $state<Set<string>>(new Set());
  let loading = $state(true);
  let importing = $state(false);
  let err = $state("");

  // Names already in the registry — pre-checked candidates that would
  // collide are filtered out so we don't accidentally create duplicates.
  let existingNames = $derived(new Set(app.hosts.map((h) => h.name)));

  onMount(async () => {
    try {
      const list = ((await HostService.ScanSSHConfig()) ??
        []) as SSHConfigCandidate[];
      candidates = list;
      // Pre-select everything that won't collide with an existing host.
      const next = new Set<string>();
      for (const c of list) {
        if (!existingNames.has(c.alias)) next.add(c.alias);
      }
      selected = next;
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      loading = false;
    }
  });

  function toggle(alias: string) {
    const next = new Set(selected);
    if (next.has(alias)) next.delete(alias);
    else next.add(alias);
    selected = next;
  }

  function selectAll() {
    selected = new Set(
      candidates
        .filter((c) => !existingNames.has(c.alias))
        .map((c) => c.alias),
    );
  }

  function selectNone() {
    selected = new Set();
  }

  async function doImport() {
    if (selected.size === 0) return;
    importing = true;
    err = "";
    try {
      const picks = candidates.filter((c) => selected.has(c.alias));
      const n = (await HostService.ImportSSHConfigEntries(picks)) as number;
      onimported(n);
    } catch (e: any) {
      err = String(e?.message ?? e);
      importing = false;
    }
  }
</script>

<div
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
  role="presentation"
  onclick={(e) => {
    if (e.target === e.currentTarget) onclose();
  }}
>
  <div
    class="flex max-h-[80vh] w-[640px] flex-col overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/50"
  >
    <div class="flex items-center gap-2 border-b hairline px-5 py-3">
      <FileText size="14" class="text-[var(--color-accent)]" />
      <h3 class="text-sm font-semibold">Import from ~/.ssh/config</h3>
      <button
        class="ml-auto rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={onclose}
      >
        <X size="14" />
      </button>
    </div>

    {#if loading}
      <div class="flex h-32 items-center justify-center text-xs text-[var(--color-text-3)]">
        <Loader2 size="14" class="animate-spin" /> &nbsp;reading SSH config…
      </div>
    {:else if err}
      <div class="m-4 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-3 text-xs text-[var(--color-danger)]">
        {err}
      </div>
    {:else if candidates.length === 0}
      <div class="flex flex-1 items-center justify-center p-6 text-center">
        <div>
          <FileText size="22" class="mx-auto text-[var(--color-text-4)]" />
          <p class="mt-2 text-xs text-[var(--color-text-3)]">
            No host entries found in ~/.ssh/config (or the file doesn't
            exist). Wildcard patterns are skipped — only concrete aliases
            qualify for import.
          </p>
        </div>
      </div>
    {:else}
      <div
        class="flex items-center gap-2 border-b hairline surface-1 px-4 py-2 text-[11px] text-[var(--color-text-3)]"
      >
        <span class="font-mono">
          {selected.size} <span class="text-[var(--color-text-4)]">/</span>
          {candidates.length}
        </span>
        <span>selected</span>
        <button
          class="ml-2 rounded px-1.5 py-0.5 hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={selectAll}>all</button
        >
        <button
          class="rounded px-1.5 py-0.5 hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={selectNone}>none</button
        >
        <span class="ml-auto text-[10px] text-[var(--color-text-4)]">
          existing aliases are auto-skipped
        </span>
      </div>

      <div class="flex-1 overflow-y-auto">
        {#each candidates as c (c.alias)}
          {@const isExisting = existingNames.has(c.alias)}
          <label
            class="flex cursor-pointer items-start gap-2 border-b hairline px-4 py-2 text-xs transition-colors hover:bg-[var(--color-surface-2)]"
            class:opacity-50={isExisting}
          >
            <input
              type="checkbox"
              class="mt-1 accent-[var(--color-accent)]"
              checked={selected.has(c.alias)}
              disabled={isExisting}
              onchange={() => toggle(c.alias)}
            />
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <Server size="11" class="text-[var(--color-text-3)]" />
                <span class="font-medium text-[var(--color-text-1)]"
                  >{c.alias}</span
                >
                {#if isExisting}
                  <span
                    class="rounded border border-[var(--color-warn)]/40 bg-[var(--color-warn)]/10 px-1.5 py-0.5 text-[9px] uppercase tracking-wider text-[var(--color-warn)]"
                    >already imported</span
                  >
                {/if}
                {#if c.identityFile}
                  <span
                    class="ml-auto inline-flex items-center gap-1 text-[10px] text-[var(--color-text-3)]"
                  >
                    <KeyRound size="10" /> key
                  </span>
                {:else}
                  <span
                    class="ml-auto inline-flex items-center gap-1 text-[10px] text-[var(--color-text-3)]"
                  >
                    <Lock size="10" /> agent
                  </span>
                {/if}
              </div>
              <div class="mt-0.5 truncate font-mono text-[10px] text-[var(--color-text-3)]">
                {c.user || "?"}@{c.hostname}:{c.port || 22}
              </div>
              {#if c.identityFile}
                <div class="truncate font-mono text-[10px] text-[var(--color-text-4)]">
                  identity: {c.identityFile}
                </div>
              {/if}
              {#if c.proxyJump}
                <div class="truncate font-mono text-[10px] text-[var(--color-warn)]">
                  ProxyJump: {c.proxyJump} (not yet supported by Connect)
                </div>
              {/if}
            </div>
          </label>
        {/each}
      </div>

      <div
        class="flex items-center justify-between gap-2 border-t hairline surface-1 px-5 py-3"
      >
        <span class="text-[10px] text-[var(--color-text-4)]">
          <AlertTriangle size="10" class="mr-1 inline" />
          Imported hosts default to <span class="font-mono">agent</span> auth
          (or <span class="font-mono">key</span> if IdentityFile is set; you
          must link a vault key after import).
        </span>
        <div class="flex items-center gap-2">
          <button
            class="rounded-md px-3 py-1.5 text-xs text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
            onclick={onclose}>Cancel</button
          >
          <button
            class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
            disabled={importing || selected.size === 0}
            onclick={doImport}
          >
            {#if importing}
              <Loader2 size="11" class="animate-spin" />
            {:else}
              <Check size="11" />
            {/if}
            Import {selected.size}
          </button>
        </div>
      </div>
    {/if}
  </div>
</div>
