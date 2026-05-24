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
      class="pointer-events-auto w-full max-w-md overflow-hidden border hairline-strong surface-2 shadow-2xl shadow-black/60"
      style="box-shadow: 0 0 0 1px var(--color-line-strong), 0 0 40px rgba(0,255,136,0.05), 0 32px 64px rgba(0,0,0,0.5);"
    >
      <!-- Header -->
      <div class="flex items-center gap-2 border-b hairline px-4 py-2.5">
        <span class="text-sm font-semibold text-[var(--color-accent)]/80">Blacknode</span>
        <span class="text-xs text-[var(--color-text-4)]">Setup guide</span>
        <button
          class="ml-auto border border-[var(--color-line)] p-1 text-[var(--color-text-4)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)] transition-all"
          title="Dismiss"
          onclick={dismiss}
        >
          <X size="11" />
        </button>
      </div>

      <!-- Steps -->
      <ol class="space-y-px p-3">
        <li
          class="flex items-center gap-3 border border-transparent p-2.5 transition-all {hasHost
            ? 'opacity-40'
            : 'border-[var(--color-accent)]/15 bg-[var(--color-accent)]/4'}"
        >
          <div
            class="flex h-6 w-6 shrink-0 items-center justify-center border font-mono text-[9px] font-bold {hasHost
              ? 'border-[var(--color-accent)]/40 text-[var(--color-accent)]'
              : 'border-[var(--color-line-strong)] text-[var(--color-text-3)]'}"
          >
            {#if hasHost}✓{:else}01{/if}
          </div>
          <div class="flex-1">
            <p class="text-sm font-semibold text-[var(--color-text-1)]">Add your first host</p>
            <p class="mt-0.5 text-xs text-[var(--color-text-4)] leading-relaxed">
              Save SSH connection details. Encrypted at rest in your local vault.
            </p>
          </div>
          {#if !hasHost}
            <button
              class="flex items-center gap-1.5 border border-[var(--color-accent)]/40 bg-[var(--color-accent)]/8 px-2.5 py-1.5 text-xs font-semibold text-[var(--color-accent)] hover:bg-[var(--color-accent)]/15 transition-all"
              onclick={() => (editorOpen = true)}
            >
              Add <ArrowRight size="11" />
            </button>
          {/if}
        </li>

        <li
          class="flex items-center gap-3 border border-transparent p-2.5 {hasKey ? 'opacity-40' : ''}"
        >
          <div
            class="flex h-6 w-6 shrink-0 items-center justify-center border font-mono text-[9px] font-bold {hasKey
              ? 'border-[var(--color-accent)]/40 text-[var(--color-accent)]'
              : 'border-[var(--color-line)] text-[var(--color-text-4)]'}"
          >
            {#if hasKey}✓{:else}02{/if}
          </div>
          <div class="flex-1">
            <p class="text-sm font-semibold text-[var(--color-text-2)]">
              SSH key <span class="text-xs text-[var(--color-text-4)] font-normal">(optional)</span>
            </p>
            <p class="mt-0.5 text-xs text-[var(--color-text-4)] leading-relaxed">
              Import or generate an ed25519 keypair. Skip for password or agent auth.
            </p>
          </div>
          {#if !hasKey}
            <button
              class="border border-[var(--color-line)] px-2.5 py-1.5 text-xs text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
              onclick={() => (app.view = 'keys')}
            >
              Keys
            </button>
          {/if}
        </li>

        <li
          class="flex items-center gap-3 border border-transparent p-2.5 {connected
            ? 'opacity-40'
            : hasHost
              ? 'border-[var(--color-accent)]/10 bg-[var(--color-accent)]/3'
              : ''}"
        >
          <div
            class="flex h-6 w-6 shrink-0 items-center justify-center border font-mono text-[9px] font-bold {connected
              ? 'border-[var(--color-accent)]/40 text-[var(--color-accent)]'
              : 'border-[var(--color-line)] text-[var(--color-text-4)]'}"
          >
            {#if connected}✓{:else}03{/if}
          </div>
          <div class="flex-1">
            <p class="text-sm font-semibold text-[var(--color-text-1)]">Click a host to connect</p>
            <p class="mt-0.5 text-xs text-[var(--color-text-4)] leading-relaxed">
              Select a host in the sidebar. An SSH session binds to this tab.
            </p>
          </div>
        </li>
      </ol>

      <!-- Footer tip -->
      <div class="border-t hairline px-3 py-2">
        <p class="font-mono text-[10px] text-[var(--color-text-4)]">
          ⌘K palette · ⌘I AI · ⌘T new tab
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
