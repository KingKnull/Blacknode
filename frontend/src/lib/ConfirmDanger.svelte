<script lang="ts">
  import { AlertTriangle, ShieldAlert, X } from "@lucide/svelte";

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
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
  role="presentation"
  onclick={(e) => {
    if (e.target === e.currentTarget) onCancel();
  }}
>
  <div
    class="w-[520px] overflow-hidden rounded-xl border border-[var(--color-danger)]/40 bg-[var(--color-surface-2)] shadow-2xl shadow-black/60"
  >
    <div
      class="flex items-center gap-2 border-b border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 px-5 py-3"
    >
      {#if severity === "block-without-confirm"}
        <ShieldAlert size="16" class="text-[var(--color-danger)]" />
      {:else}
        <AlertTriangle size="16" class="text-[var(--color-warn)]" />
      {/if}
      <h3 class="text-sm font-semibold text-[var(--color-text-1)]">{title}</h3>
      <button
        class="ml-auto rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={onCancel}
        title="Cancel"
      >
        <X size="14" />
      </button>
    </div>

    <div class="space-y-3 p-5 text-sm">
      <p class="text-[var(--color-text-2)] leading-relaxed">{body}</p>

      {#if productionHosts.length > 0}
        <div
          class="rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-3"
        >
          <div
            class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-danger)]"
          >
            Production hosts in scope
          </div>
          <div class="mt-1 flex flex-wrap gap-1.5">
            {#each productionHosts as h (h)}
              <span
                class="rounded-sm border border-[var(--color-danger)]/40 bg-[var(--color-danger)]/15 px-1.5 py-0.5 font-mono text-[11px] text-[var(--color-danger)]"
                >{h}</span
              >
            {/each}
          </div>
        </div>
      {/if}

      {#if requirePhrase}
        <label class="block">
          <span
            class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >Type
            <span class="font-mono text-[var(--color-danger)]"
              >{requirePhrase}</span
            > to confirm</span
          >
          <input
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-sm outline-none"
            bind:value={typed}
            autofocus
          />
        </label>
      {/if}
    </div>

    <div
      class="flex items-center justify-end gap-2 border-t hairline bg-[var(--color-surface-1)] px-5 py-3"
    >
      <button
        class="rounded-md px-3 py-1.5 text-xs text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)]"
        onclick={onCancel}>Cancel</button
      >
      <button
        class="flex items-center gap-1.5 rounded-md bg-[var(--color-danger)] px-3 py-1.5 text-xs font-medium text-white hover:opacity-90 disabled:opacity-40"
        disabled={!canConfirm}
        onclick={onConfirm}
      >
        <ShieldAlert size="11" />
        I understand — proceed
      </button>
    </div>
  </div>
</div>
