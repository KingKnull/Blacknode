<script lang="ts">
  import { app } from "./state.svelte";
  import HostEditor from "./HostEditor.svelte";
  import { Server, KeyRound, TerminalSquare, X, CheckCircle2, ArrowRight } from "@lucide/svelte";

  // First-run guidance card. Renders inside the Terminals view when the
  // user has nothing connected yet. Dismissible — the choice persists
  // across sessions in localStorage so it doesn't badger returning users.
  const STORAGE_KEY = "blacknode.onboarding.dismissed.v1";

  let dismissed = $state<boolean>(localStorage.getItem(STORAGE_KEY) === "1");
  let editorOpen = $state(false);

  // Step completion is derived from app state — we never track these
  // independently. That way, deleting your only host re-shows step 2
  // honestly instead of relying on a stale "done" flag.
  let hasHost = $derived(app.hosts.length > 0);
  let hasKey = $derived(app.keys.length > 0);
  let connected = $derived(!!app.selectedHostID);
  let allDone = $derived(hasHost && connected);

  function dismiss() {
    dismissed = true;
    localStorage.setItem(STORAGE_KEY, "1");
  }
  function reset() {
    dismissed = false;
    localStorage.removeItem(STORAGE_KEY);
  }
  // Expose reset on the window for advanced users / debugging — calling
  // `blacknode_resetOnboarding()` from devtools brings the card back.
  if (typeof window !== "undefined") {
    (window as any).blacknode_resetOnboarding = reset;
  }
</script>

{#if !dismissed && !allDone}
  <div
    class="pointer-events-none absolute inset-0 z-10 flex items-center justify-center p-8"
  >
    <div
      class="pointer-events-auto w-full max-w-md rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/40"
    >
      <div
        class="flex items-center gap-2 border-b hairline px-4 py-2.5"
      >
        <h3 class="text-sm font-semibold text-[var(--color-text-1)]">
          Welcome to blacknode
        </h3>
        <button
          class="ml-auto rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          title="Dismiss"
          onclick={dismiss}
        >
          <X size="13" />
        </button>
      </div>

      <ol class="space-y-1 p-3">
        <li
          class="flex items-center gap-3 rounded-md p-2 {hasHost
            ? 'opacity-50'
            : 'bg-[var(--color-surface-3)]/40'}"
        >
          <div
            class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full {hasHost
              ? 'bg-[var(--color-accent)]/15 text-[var(--color-accent)]'
              : 'border hairline-strong text-[var(--color-text-2)]'}"
          >
            {#if hasHost}
              <CheckCircle2 size="14" />
            {:else}
              <Server size="13" />
            {/if}
          </div>
          <div class="flex-1">
            <p class="text-[12px] font-medium text-[var(--color-text-1)]">
              Add your first host
            </p>
            <p class="text-[11px] text-[var(--color-text-3)]">
              Save connection details for a server you want to manage. Hosts
              live encrypted-at-rest in your local vault.
            </p>
          </div>
          {#if !hasHost}
            <button
              class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-2.5 py-1 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90"
              onclick={() => (editorOpen = true)}
            >
              Add host <ArrowRight size="11" />
            </button>
          {/if}
        </li>

        <li
          class="flex items-center gap-3 rounded-md p-2 {hasKey
            ? 'opacity-50'
            : ''}"
        >
          <div
            class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full {hasKey
              ? 'bg-[var(--color-accent)]/15 text-[var(--color-accent)]'
              : 'border hairline text-[var(--color-text-3)]'}"
          >
            {#if hasKey}
              <CheckCircle2 size="14" />
            {:else}
              <KeyRound size="13" />
            {/if}
          </div>
          <div class="flex-1">
            <p class="text-[12px] font-medium text-[var(--color-text-2)]">
              Import or generate an SSH key <span class="font-mono text-[10px] text-[var(--color-text-4)]">(optional)</span>
            </p>
            <p class="text-[11px] text-[var(--color-text-3)]">
              Use the Keys panel to add an existing private key or generate a
              new ed25519 keypair. Skip if you'll use password or agent auth.
            </p>
          </div>
          {#if !hasKey}
            <button
              class="rounded-md border hairline-strong px-2.5 py-1 text-[11px] text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
              onclick={() => (app.view = "keys")}
            >
              Open Keys
            </button>
          {/if}
        </li>

        <li
          class="flex items-center gap-3 rounded-md p-2 {connected
            ? 'opacity-50'
            : hasHost
              ? 'bg-[var(--color-surface-3)]/40'
              : ''}"
        >
          <div
            class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full {connected
              ? 'bg-[var(--color-accent)]/15 text-[var(--color-accent)]'
              : 'border hairline text-[var(--color-text-3)]'}"
          >
            {#if connected}
              <CheckCircle2 size="14" />
            {:else}
              <TerminalSquare size="13" />
            {/if}
          </div>
          <div class="flex-1">
            <p class="text-[12px] font-medium text-[var(--color-text-1)]">
              Click a host to open a terminal
            </p>
            <p class="text-[11px] text-[var(--color-text-3)]">
              Pick a host in the sidebar — blacknode dials a fresh SSH
              session and binds it to a tab here.
            </p>
          </div>
        </li>
      </ol>

      <div class="border-t hairline px-3 py-2">
        <p class="text-[10px] text-[var(--color-text-4)]">
          Tip: ⌘K / Ctrl+K opens the command palette. ⌘I opens the AI
          assistant. Settings has theme, notifications, and cloud sync.
        </p>
      </div>
    </div>
  </div>

  {#if editorOpen}
    <HostEditor
      onclose={() => (editorOpen = false)}
      onsaved={() => (editorOpen = false)}
    />
  {/if}
{/if}
