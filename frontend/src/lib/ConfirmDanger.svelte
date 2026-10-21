<script lang="ts">
  import { AlertTriangle, ShieldAlert, X } from "@lucide/svelte";
  import Dialog from "./Dialog.svelte";

  type Props = {
    title: string;
    body: string;
    severity: "warn" | "block-without-confirm";
    productionHosts: string[]; // names of prod hosts in scope
    readOnlyHosts?: string[]; // names of read-only hosts in scope
    requirePhrase?: string; // user must type this exactly to confirm
    /**
     * Whether Enter confirms. Default true, which suits the click-triggered
     * call sites: the user pressed a button, the dialog appeared, and Enter is
     * a natural "yes". Pass false when the dialog is raised *by* a keystroke —
     * see the broadcast danger prompt in Workspace.svelte, where the Enter that
     * submitted the command bubbles to window and would confirm the dialog it
     * just opened.
     */
    allowEnterConfirm?: boolean;
    portal?: boolean;
    onCancel: () => void;
    onConfirm: () => void | Promise<void>;
  };
  let {
    title,
    body,
    severity,
    productionHosts,
    readOnlyHosts = [],
    requirePhrase,
    allowEnterConfirm = true,
    portal = false,
    onCancel,
    onConfirm,
  }: Props = $props();

  let typed = $state("");
  let submitting = $state(false);
  let error = $state("");
  const titleID = $props.id();
  let canConfirm = $derived(
    !submitting && (!requirePhrase || typed.trim() === requirePhrase.trim()),
  );

  function cancel() {
    if (!submitting) onCancel();
  }

  async function confirm() {
    if (!canConfirm) return;
    submitting = true;
    error = "";
    try { await onConfirm(); }
    catch (e) { error = e instanceof Error ? e.message : String(e); }
    finally { submitting = false; }
  }

  // Enter-to-confirm (Escape + focus trap handled by Dialog). Disabled for the
  // most destructive tier, which requires an explicit click, and for callers
  // that opt out because a keystroke opened the dialog.
  function onKey(e: KeyboardEvent) {
    // Focused buttons retain their native action. Enter on Cancel must never
    // also run Proceed, and Enter on Proceed must not run it twice.
    if (!allowEnterConfirm || e.defaultPrevented || e.repeat) return;
    if ((e.target as HTMLElement)?.closest('button, a, [role="button"]')) return;
    if (e.key === "Enter" && canConfirm && severity !== "block-without-confirm") {
      e.preventDefault();
      void confirm();
    }
  }
</script>

<Dialog
  {portal}
  onkeydown={onKey}
  onclose={cancel}
  labelledby={titleID}
  panelClass="w-[520px] overflow-y-auto border border-[var(--color-danger)]/40 bg-[var(--color-surface-2)] shadow-2xl"
  panelStyle="box-shadow: 0 0 0 1px rgba(255,60,60,0.2), 0 0 40px rgba(255,60,60,0.04), 0 40px 80px rgba(0,0,0,0.6);"
>
  {#snippet children()}
    <!-- Danger header -->
    <div
      class="flex items-center gap-2 border-b border-[var(--color-danger)]/25 bg-[var(--color-danger)]/8 px-5 py-3"
    >
      {#if severity === 'block-without-confirm'}
        <ShieldAlert size="12" class="text-[var(--color-danger)]" />
      {:else}
        <AlertTriangle size="12" class="text-[var(--color-warn)]" />
      {/if}
      <span id={titleID} class="font-mono type-eyebrow text-[var(--color-danger)]">{title}</span>
      <button
        class="ml-auto border border-[var(--color-line)] p-1 text-[var(--color-text-4)] hover:border-[var(--color-danger)]/40 hover:text-[var(--color-danger)] transition-all"
        onclick={cancel}
        disabled={submitting}
        title="Cancel"
      >
        <X size="11" />
      </button>
    </div>

    <div class="space-y-3 p-5">
      <p class="type-body leading-relaxed text-[var(--color-text-2)]">{body}</p>
      {#if error}<p class="type-caption break-words text-[var(--color-danger)]" role="alert">{error}</p>{/if}

      {#if productionHosts.length > 0}
        <div class="border border-[var(--color-danger)]/25 bg-[var(--color-danger)]/8 p-3">
          <div class="type-caption font-semibold text-[var(--color-danger)] mb-2">Production hosts in scope</div>
          <div class="flex flex-wrap gap-1">
            <!-- Different hosts can share a display name. Keep every entry. -->
            {#each productionHosts as h}
              <span class="border border-[var(--color-danger)]/40 bg-[var(--color-danger)]/12 px-1.5 py-0.5 font-mono type-caption text-[var(--color-danger)]">{h}</span>
            {/each}
          </div>
        </div>
      {/if}

      {#if readOnlyHosts.length > 0}
        <div class="border border-[var(--color-accent)]/25 bg-[var(--color-accent)]/8 p-3">
          <div class="type-caption font-semibold text-[var(--color-accent)] mb-2">Read-only hosts in scope</div>
          <div class="flex flex-wrap gap-1">
            {#each readOnlyHosts as h}
              <span class="border border-[var(--color-accent)]/35 bg-[var(--color-accent)]/10 px-1.5 py-0.5 font-mono type-caption text-[var(--color-accent)]">{h}</span>
            {/each}
          </div>
          <p class="mt-2 type-micro text-[var(--color-text-3)]">These hosts allow inspection commands only. Mutating or destructive commands will be blocked by host policy.</p>
        </div>
      {/if}

      {#if requirePhrase}
        <label class="block">
          <span class="type-caption text-[var(--color-text-4)]">
            Type <span class="font-mono text-[var(--color-danger)]">{requirePhrase}</span> to confirm
          </span>
          <input
            class="mt-1.5 w-full border border-[var(--color-danger)]/30 bg-[var(--color-surface-3)] px-3 py-2 font-mono type-body text-[var(--color-text-1)] outline-none focus:border-[var(--color-danger)]/60 transition-colors"
            bind:value={typed}
            data-autofocus
          />
        </label>
      {/if}
    </div>

    <div
      class="flex items-center justify-end gap-2 border-t border-[var(--color-danger)]/15 bg-[var(--color-surface-1)] px-5 py-3"
    >
      <button class="border border-[var(--color-line)] px-3 py-1.5 type-body text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
        disabled={submitting} onclick={cancel}>Cancel</button>
      <button
        class="flex items-center gap-1.5 border border-[var(--color-danger)]/50 bg-[var(--color-danger)]/12 px-3 py-1.5 type-body font-semibold text-[var(--color-danger)] hover:bg-[var(--color-danger)]/20 disabled:opacity-30 disabled:cursor-not-allowed transition-all"
        disabled={!canConfirm}
        onclick={confirm}
      >
        <ShieldAlert size="12" />
        {submitting ? "Working…" : "Proceed"}
      </button>
    </div>
  {/snippet}
</Dialog>
