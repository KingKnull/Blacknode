<script lang="ts">
  import { app } from "./state.svelte";
  import HostEditor from "./HostEditor.svelte";
  import { Server, KeyRound, TerminalSquare, X, CheckCircle2, ArrowRight, Sparkles } from "@lucide/svelte";

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

  // Progress across the 3 steps (key step is optional but still counts
  // toward the visual fill so the bar doesn't look "stuck" at 66%).
  let completedCount = $derived([hasHost, hasKey, connected].filter(Boolean).length);
  let progressPct = $derived(Math.round((completedCount / 3) * 100));

  // Brief "all done" celebration state — shown once when allDone flips true,
  // then auto-dismisses after a few seconds.
  let showSuccess = $state(false);
  let successShownOnce = false;
  $effect(() => {
    if (allDone && !successShownOnce && !dismissed) {
      successShownOnce = true;
      showSuccess = true;
      setTimeout(() => {
        showSuccess = false;
        dismiss();
      }, 2400);
    }
  });

  function dismiss() {
    dismissed = true;
    localStorage.setItem(STORAGE_KEY, "1");
  }
  function reset() {
    dismissed = false;
    successShownOnce = false;
    localStorage.removeItem(STORAGE_KEY);
  }
  // Expose reset on the window for advanced users / debugging — calling
  // `blacknode_resetOnboarding()` from devtools brings the card back.
  if (typeof window !== "undefined") {
    (window as any).blacknode_resetOnboarding = reset;
  }

  // A few extra shortcuts beyond the original one-liner footer.
  const SHORTCUTS: [string, string][] = [
    ["⌘K", "Command palette"],
    ["⌘I", "AI assistant"],
    ["⌘T", "New tab"],
    ["⌘W", "Close tab"],
    ["Ctrl+.", "Terminal side panel"],
    ["↓", "Autocomplete (in terminal)"],
    ["?", "Full shortcut list"],
  ];
</script>

{#if showSuccess}
  <div class="pointer-events-none absolute inset-0 z-20 flex items-center justify-center p-8">
    <div
      class="scale-in flex items-center gap-3 border border-[var(--color-accent)]/40 bg-[var(--color-accent-soft)] px-5 py-4 shadow-2xl"
      style="box-shadow: 0 0 40px rgba(59,130,246,0.15), 0 24px 48px rgba(0,0,0,0.4);"
    >
      <!-- Subtle celebratory sparkle burst — a few small dots fading outward,
           kept minimal so it reads as a confirmation, not a distraction. -->
      <div class="relative flex h-9 w-9 shrink-0 items-center justify-center">
        <span class="absolute h-9 w-9 rounded-full bg-[var(--color-accent)]/15 confetti-ring"></span>
        <CheckCircle2 size="22" class="relative text-[var(--color-accent)]" />
      </div>
      <div>
        <p class="type-body font-semibold text-[var(--color-text-1)]">You're all set</p>
        <p class="type-caption text-[var(--color-text-3)]">Host added and connected. Happy shipping.</p>
      </div>
    </div>
  </div>
{/if}

{#if !dismissed && !allDone}
  <div
    class="pointer-events-none absolute inset-0 z-10 flex items-center justify-center p-8"
  >
    <div
      class="pointer-events-auto w-full max-w-md overflow-hidden border hairline-strong surface-2 shadow-2xl shadow-black/60"
      style="box-shadow: 0 0 0 1px var(--color-line-strong), 0 0 40px rgba(59, 130, 246,0.05), 0 32px 64px rgba(0,0,0,0.5);"
    >
      <!-- Header -->
      <div class="flex items-center gap-2 border-b hairline px-4 py-2.5">
        <span class="type-body font-semibold text-[var(--color-accent)]/80">Blacknode</span>
        <span class="type-caption text-[var(--color-text-4)]">Setup guide</span>
        <span class="ml-auto font-mono type-micro text-[var(--color-text-4)] tabular">{completedCount}/3</span>
        <button
          class="border border-[var(--color-line)] p-1 text-[var(--color-text-4)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-2)] transition-all"
          title="Dismiss"
          onclick={dismiss}
        >
          <X size="11" />
        </button>
      </div>

      <!-- Progress bar -->
      <div class="h-[3px] w-full bg-[var(--color-surface-3)] overflow-hidden">
        <div
          class="h-full bg-[var(--color-accent)] transition-[width] duration-500 ease-out"
          style="width: {progressPct}%; box-shadow: 0 0 8px var(--color-accent);"
        ></div>
      </div>

      <!-- Steps -->
      <ol class="space-y-px p-3">
        <li
          class="flex items-center gap-3 border border-transparent p-2.5 transition-all {hasHost
            ? 'opacity-40'
            : 'border-[var(--color-accent)]/15 bg-[var(--color-accent)]/4'}"
        >
          <div
            class="flex h-6 w-6 shrink-0 items-center justify-center border font-mono type-nano font-bold {hasHost ? 'border-[var(--color-accent)]/40 text-[var(--color-accent)]' : 'border-[var(--color-line-strong)] text-[var(--color-text-3)]'}"
          >
            {#if hasHost}✓{:else}01{/if}
          </div>
          <div class="flex-1">
            <p class="type-body font-semibold text-[var(--color-text-1)]">Add your first host</p>
            <p class="mt-0.5 type-caption text-[var(--color-text-4)] leading-relaxed">
              Save SSH connection details. Encrypted at rest in your local vault.
            </p>
          </div>
          {#if !hasHost}
            <button
              class="flex items-center gap-1.5 border border-[var(--color-accent)]/40 bg-[var(--color-accent)]/8 px-2.5 py-1.5 type-caption font-semibold text-[var(--color-accent)] hover:bg-[var(--color-accent)]/15 transition-all"
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
            class="flex h-6 w-6 shrink-0 items-center justify-center border font-mono type-nano font-bold {hasKey ? 'border-[var(--color-accent)]/40 text-[var(--color-accent)]' : 'border-[var(--color-line)] text-[var(--color-text-4)]'}"
          >
            {#if hasKey}✓{:else}02{/if}
          </div>
          <div class="flex-1">
            <p class="type-body font-semibold text-[var(--color-text-2)]">
              SSH key <span class="type-caption text-[var(--color-text-4)] font-normal">(optional)</span>
            </p>
            <p class="mt-0.5 type-caption text-[var(--color-text-4)] leading-relaxed">
              Import or generate an ed25519 keypair. Skip for password or agent auth.
            </p>
          </div>
          {#if !hasKey}
            <button
              class="border border-[var(--color-line)] px-2.5 py-1.5 type-caption text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
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
            class="flex h-6 w-6 shrink-0 items-center justify-center border font-mono type-nano font-bold {connected ? 'border-[var(--color-accent)]/40 text-[var(--color-accent)]' : 'border-[var(--color-line)] text-[var(--color-text-4)]'}"
          >
            {#if connected}✓{:else}03{/if}
          </div>
          <div class="flex-1">
            <p class="type-body font-semibold text-[var(--color-text-1)]">Click a host to connect</p>
            <p class="mt-0.5 type-caption text-[var(--color-text-4)] leading-relaxed">
              Select a host in the sidebar. An SSH session binds to this tab.
            </p>
          </div>
        </li>
      </ol>

      <!-- Footer: shortcuts cheat-sheet -->
      <div class="border-t hairline px-3 py-2.5">
        <div class="mb-1.5 flex items-center gap-1.5 type-eyebrow text-[var(--color-text-4)]">
          <Sparkles size="10" /> Quick reference
        </div>
        <div class="grid grid-cols-2 gap-x-3 gap-y-1">
          {#each SHORTCUTS as [key, label] (key)}
            <div class="flex items-center gap-1.5 font-mono type-micro text-[var(--color-text-4)]">
              <kbd class="rounded border hairline px-1 text-[var(--color-text-3)]">{key}</kbd>
              <span class="truncate">{label}</span>
            </div>
          {/each}
        </div>
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

<style>
  .confetti-ring {
    animation: confetti-ring-pulse 1.2s ease-out 2;
  }
  @keyframes confetti-ring-pulse {
    0% { transform: scale(0.6); opacity: 0.6; }
    100% { transform: scale(1.8); opacity: 0; }
  }
  @media (prefers-reduced-motion: reduce) {
    .confetti-ring { animation: none; }
  }
</style>
