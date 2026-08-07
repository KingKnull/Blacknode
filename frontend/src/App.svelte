<script lang="ts">
  import { onMount } from "svelte";
  import { app } from "./lib/state.svelte";
  import VaultGate from "./lib/VaultGate.svelte";
  import Workspace from "./lib/Workspace.svelte";

  onMount(() => {
    void app.refreshVault();
  });

  // Apply theme to <html data-theme="..."> whenever the saved theme
  // setting changes. Drives every CSS-variable-based token across the app.
  $effect(() => {
    const theme = app.settings.theme === "light" ? "light" : "dark";
    document.documentElement.dataset.theme = theme;
  });
</script>

<svelte:boundary>
  <VaultGate>
    {#snippet children()}
      <Workspace />
    {/snippet}
  </VaultGate>

  {#snippet failed(error, reset)}
    <!-- Last-resort guard: a render crash here used to leave a blank page. -->
    <div class="flex h-full w-full flex-col items-center justify-center gap-3 bg-[var(--color-surface-0)]" role="alert">
      <p class="type-body font-semibold text-[var(--color-text-1)]">Something went wrong</p>
      <p class="max-w-md break-all text-center font-mono type-caption text-[var(--color-text-3)]">{String(error)}</p>
      <button
        class="rounded-md bg-[var(--color-accent)] px-3 py-1.5 type-caption font-medium text-[var(--color-surface-0)] hover:opacity-90 transition-opacity"
        onclick={reset}
      >Reload interface</button>
    </div>
  {/snippet}
</svelte:boundary>
