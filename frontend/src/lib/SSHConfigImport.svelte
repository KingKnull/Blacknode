<script lang="ts">
  import { onMount } from "svelte";
  import { HostService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { SSHConfigCandidate } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
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
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/80"
  role="presentation"
  onclick={(e) => { if (e.target === e.currentTarget) onclose(); }}
>
  <div
    class="flex max-h-[80vh] w-[640px] flex-col overflow-hidden border hairline-strong surface-2 shadow-2xl"
    style="box-shadow: 0 0 0 1px var(--color-line-strong), 0 0 60px rgba(0,255,136,0.04), 0 40px 80px rgba(0,0,0,0.6);"
  >
    <div class="flex items-center gap-2.5 border-b hairline px-5 py-3">
      <FileText size="12" class="text-[var(--color-accent)]" />
      <span class="font-mono text-[10px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-1)]">IMPORT FROM ~/.ssh/config</span>
      <button
        class="ml-auto border border-[var(--color-line)] p-1 text-[var(--color-text-4)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-danger)] transition-all"
        onclick={onclose}
      >
        <X size="12" />
      </button>
    </div>

    {#if loading}
      <div class="flex h-32 items-center justify-center gap-2 font-mono text-[10px] uppercase tracking-widest text-[var(--color-text-4)]">
        <Loader2 size="12" class="animate-spin text-[var(--color-accent)]" /> READING SSH CONFIG...
      </div>
    {:else if err}
      <div class="m-4 border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/8 p-3 font-mono text-[10px] text-[var(--color-danger)]">
        ERR: {err}
      </div>
    {:else if candidates.length === 0}
      <div class="flex flex-1 items-center justify-center p-6 text-center">
        <div>
          <FileText size="20" class="mx-auto text-[var(--color-text-4)]" />
          <p class="mt-2 font-mono text-[10px] uppercase tracking-widest text-[var(--color-text-4)]">
            NO HOST ENTRIES FOUND
          </p>
          <p class="mt-1 font-mono text-[9px] text-[var(--color-text-4)]">
            ~/.ssh/config not found or only wildcard patterns present
          </p>
        </div>
      </div>
    {:else}
      <div
        class="flex items-center gap-2 border-b hairline surface-1 px-4 py-2 font-mono text-[10px] text-[var(--color-text-4)]"
      >
        <span class="text-[var(--color-accent)]">{selected.size}</span>
        <span class="text-[var(--color-text-4)]">/</span>
        <span>{candidates.length}</span>
        <span class="uppercase tracking-widest">SELECTED</span>
        <button
          class="ml-2 border border-[var(--color-line)] px-1.5 py-0.5 text-[9px] uppercase tracking-widest hover:border-[var(--color-accent)]/40 hover:text-[var(--color-accent)] transition-all"
          onclick={selectAll}>ALL</button
        >
        <button
          class="border border-[var(--color-line)] px-1.5 py-0.5 text-[9px] uppercase tracking-widest hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)] transition-all"
          onclick={selectNone}>NONE</button
        >
        <span class="ml-auto text-[9px] uppercase tracking-widest text-[var(--color-text-4)]/60">
          EXISTING ALIASES AUTO-SKIPPED
        </span>
      </div>

      <div class="flex-1 overflow-y-auto">
        {#each candidates as c (c.alias)}
          {@const isExisting = existingNames.has(c.alias)}
          <label
            class="flex cursor-pointer items-start gap-2 border-b hairline px-4 py-2 font-mono text-[10px] transition-colors hover:bg-[var(--color-surface-2)]"
            class:opacity-40={isExisting}
          >
            <input
              type="checkbox"
              class="mt-px accent-[var(--color-accent)]"
              checked={selected.has(c.alias)}
              disabled={isExisting}
              onchange={() => toggle(c.alias)}
            />
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="text-[var(--color-text-1)]">{c.alias}</span>
                {#if isExisting}
                  <span
                    class="border border-[var(--color-warn)]/40 bg-[var(--color-warn)]/8 px-1.5 py-px text-[8px] uppercase tracking-widest text-[var(--color-warn)]"
                    >EXISTING</span
                  >
                {/if}
                {#if c.identityFile}
                  <span class="ml-auto flex items-center gap-1 text-[9px] text-[var(--color-accent)]/60">
                    <KeyRound size="9" /> KEY
                  </span>
                {:else}
                  <span class="ml-auto flex items-center gap-1 text-[9px] text-[var(--color-text-4)]">
                    <Lock size="9" /> AGENT
                  </span>
                {/if}
              </div>
              <div class="mt-0.5 truncate text-[9px] text-[var(--color-text-4)]">
                {c.user || '?'}@{c.hostname}:{c.port || 22}
              </div>
              {#if c.identityFile}
                <div class="truncate text-[9px] text-[var(--color-text-4)]/60">key: {c.identityFile}</div>
              {/if}
              {#if c.proxyJump}
                <div class="truncate text-[9px] text-[var(--color-warn)]/70">proxyjump: {c.proxyJump} (not yet supported)</div>
              {/if}
            </div>
          </label>
        {/each}
      </div>

      <div
        class="flex items-center justify-between gap-2 border-t hairline surface-1 px-5 py-3"
      >
        <span class="font-mono text-[9px] uppercase tracking-widest text-[var(--color-text-4)]">
          <AlertTriangle size="9" class="mr-1 inline" />
          DEFAULTS: AGENT AUTH (OR KEY IF IDENTITY FILE SET)
        </span>
        <div class="flex items-center gap-2">
          <button
            class="border border-[var(--color-line)] px-3 py-1.5 font-mono text-[10px] uppercase tracking-widest text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
            onclick={onclose}>CANCEL</button
          >
          <button
            class="flex items-center gap-1.5 border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 px-3 py-1.5 font-mono text-[10px] uppercase tracking-widest text-[var(--color-accent)] hover:bg-[var(--color-accent)]/18 disabled:opacity-30 disabled:cursor-not-allowed transition-all"
            disabled={importing || selected.size === 0}
            onclick={doImport}
          >
            {#if importing}
              <Loader2 size="10" class="animate-spin" />
            {:else}
              <Check size="10" />
            {/if}
            IMPORT {selected.size}
          </button>
        </div>
      </div>
    {/if}
  </div>
</div>
