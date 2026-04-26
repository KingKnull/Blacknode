<script lang="ts">
  import { onMount } from "svelte";
  import { app } from "./lib/state.svelte";
  import VaultGate from "./lib/VaultGate.svelte";
  import Workspace from "./lib/Workspace.svelte";

  onMount(() => {
    void app.refreshVault();
  });
</script>

<VaultGate>
  {#snippet children()}
    <Workspace />
  {/snippet}
</VaultGate>
