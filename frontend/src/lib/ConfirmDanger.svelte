<script lang="ts">
  import { AlertTriangle, ShieldAlert, X } from "@lucide/svelte";
  import { focus } from "./actions";

  type Props = {
    title: string;
    body: string;
    severity: "warn" | "block-without-confirm";
    productionHosts: string[]; // names of prod hosts in scope
    requirePhrase?: string; // user must type this exactly to confirm
    onCancel: () => void;
    onConfirm: () => void;
  };
  let {
    title,
    body,
    severity,
    productionHosts,
    requirePhrase,
    onCancel,
    onConfirm,
  }: Props = $props();

  let typed = $state("");
  let canConfirm = $derived(
    !requirePhrase || typed.trim() === requirePhrase.trim(),
  );

  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape") onCancel();
    if (e.key === "Enter" && canConfirm && severity !== "block-without-confirm")
      onConfirm();
  }
</script>

<svelte:window onkeydown={onKey} />

<div
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/80"
  role="presentation"
  onclick={(e) => { if (e.target === e.currentTarget) onCancel(); }}
>
  <div
    class="w-[520px] overflow-hidden border border-[var(--color-danger)]/40 bg-[var(--color-surface-2)] shadow-2xl"
    style="box-shadow: 0 0 0 1px rgba(255,60,60,0.2), 0 0 40px rgba(255,60,60,0.04), 0 40px 80px rgba(0,0,0,0.6);"
  >
    <!-- Danger header -->
    <div
      class="flex items-center gap-2 border-b border-[var(--color-danger)]/25 bg-[var(--color-danger)]/8 px-5 py-3"
    >
      {#if severity === 'block-without-confirm'}
        <ShieldAlert size="12" class="text-[var(--color-danger)]" />
      {:else}
        <AlertTriangle size="12" class="text-[var(--color-warn)]" />
      {/if}
      <span class="font-mono text-[10px] font-bold uppercase tracking-[0.2em] text-[var(--color-danger)]">{title}</span>
      <button
        class="ml-auto border border-[var(--color-line)] p-1 text-[var(--color-text-4)] hover:border-[var(--color-danger)]/40 hover:text-[var(--color-danger)] transition-all"
        onclick={onCancel}
        title="Cancel"
      >
        <X size="11" />
      </button>
    </div>

    <div class="space-y-3 p-5">
      <p class="font-mono text-[10px] leading-relaxed text-[var(--color-text-3)]">{body}</p>

      {#if productionHosts.length > 0}
        <div
          class="border border-[var(--color-danger)]/25 bg-[var(--color-danger)]/8 p-3"
        >
          <div
            class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-danger)]"
          >
            PRODUCTION HOSTS IN SCOPE
          </div>
          <div class="mt-2 flex flex-wrap gap-1">
            {#each productionHosts as h (h)}
              <span
                class="border border-[var(--color-danger)]/40 bg-[var(--color-danger)]/12 px-1.5 py-0.5 font-mono text-[10px] text-[var(--color-danger)]"
                >{h}</span
              >
            {/each}
          </div>
        </div>
      {/if}

      {#if requirePhrase}
        <label class="block">
          <span
            class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-4)]"
            >TYPE <span class="text-[var(--color-danger)]">{requirePhrase}</span> TO CONFIRM</span
          >
          <input
            class="mt-1.5 w-full border border-[var(--color-danger)]/30 bg-[var(--color-surface-3)] px-3 py-2 font-mono text-[11px] text-[var(--color-text-1)] outline-none focus:border-[var(--color-danger)]/60 transition-colors"
            bind:value={typed}
            use:focus
          />
        </label>
      {/if}
    </div>

    <div
      class="flex items-center justify-end gap-2 border-t border-[var(--color-danger)]/15 bg-[var(--color-surface-1)] px-5 py-3"
    >
      <button
        class="border border-[var(--color-line)] px-3 py-1.5 font-mono text-[10px] uppercase tracking-widest text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
        onclick={onCancel}>CANCEL</button
      >
      <button
        class="flex items-center gap-1.5 border border-[var(--color-danger)]/50 bg-[var(--color-danger)]/12 px-3 py-1.5 font-mono text-[10px] font-bold uppercase tracking-widest text-[var(--color-danger)] hover:bg-[var(--color-danger)]/20 disabled:opacity-30 disabled:cursor-not-allowed transition-all"
        disabled={!canConfirm}
        onclick={onConfirm}
      >
        <ShieldAlert size="10" />
        PROCEED
      </button>
    </div>
  </div>
</div>
