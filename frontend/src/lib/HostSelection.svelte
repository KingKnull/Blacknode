<script lang="ts">
  import { app } from "./state.svelte";
  let { selected = $bindable<string[]>([]), scoped = true }: { selected?: string[]; scoped?: boolean } = $props();
</script>
<fieldset class="ops-card"><legend>Hosts</legend><div class="ops-hosts">{#each (scoped ? app.scopedHosts : app.hosts).filter((h) => !h.protocol || h.protocol === 'ssh') as host (host.id)}<label><input type="checkbox" checked={selected.includes(host.id)} onchange={(event) => selected = event.currentTarget.checked ? [...selected,host.id] : selected.filter((id) => id !== host.id)} />{host.name}{#if host.environment}<small>{host.environment}</small>{/if}</label>{/each}</div></fieldset>
