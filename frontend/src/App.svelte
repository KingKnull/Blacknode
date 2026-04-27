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

<VaultGate>
  {#snippet children()}
    <Workspace />
  {/snippet}
</VaultGate>
