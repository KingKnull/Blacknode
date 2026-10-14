<script lang="ts">
  import { onMount } from "svelte";
  import { PlatformService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Integration } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { unpack, type Versioned } from "./operationRecords";
  let { onSaved }: { onSaved?: () => void } = $props();
  let integrations = $state<Versioned<Integration>[]>([]);
  let config = $state<Versioned<Integration>>({ id: "", revision: 0, name: "", kind: "prometheus", url: "http://127.0.0.1:9090", hasToken: false });
  let token = $state("");
  let clearToken = $state(false);
  let error = $state("");
  let busy = $state(false);
  async function refresh() { integrations = (await PlatformService.ListRecords("integration")).map((r) => unpack<Integration>(r)); }
  onMount(() => { void refresh().catch((e) => error = String(e)); });
  function defaults() { config.url = ({ prometheus: 'http://127.0.0.1:9090', loki: 'http://127.0.0.1:3100', github: 'https://api.github.com', gitlab: 'https://gitlab.com/api/v4' } as Record<string,string>)[config.kind]; }
  async function save() { busy = true; error = ""; try { const { revision, ...value } = $state.snapshot(config); config = unpack<Integration>(await PlatformService.SaveIntegration(value,token,clearToken,revision)); token = ""; clearToken = false; await refresh(); onSaved?.(); } catch (e) { error = String(e); } finally { busy = false; } }
</script>
<details class="ops-card ops-form"><summary class="cursor-pointer">Manage integrations</summary>
  <div class="ops-form mt-3">
    <div class="ops-actions"><select aria-label="Edit integration" value={config.id} onchange={(e) => { const found = integrations.find((i) => i.id === e.currentTarget.value); config = found ? structuredClone($state.snapshot(found)) : { id: '', revision: 0, name: '', kind: 'prometheus', url: 'http://127.0.0.1:9090', hasToken: false }; token = ''; }}>{#each [{id:'',name:'New integration'},...integrations] as i}<option value={i.id}>{i.name}</option>{/each}</select></div>
    <div class="ops-grid"><label>Name<input bind:value={config.name} /></label><label>Provider<select bind:value={config.kind} onchange={defaults}><option value="prometheus">Prometheus</option><option value="loki">Loki</option><option value="github">GitHub</option><option value="gitlab">GitLab</option></select></label><label>API endpoint<input bind:value={config.url} /></label></div>
    <label>Access token<input type="password" bind:value={token} placeholder={config.hasToken ? 'Token saved; leave empty to keep it' : 'Optional'} autocomplete="new-password" /></label>
    {#if config.hasToken}<label class="!flex-row items-center"><input type="checkbox" bind:checked={clearToken} />Remove saved token</label>{/if}
    <p class="ops-muted">Tokens are encrypted in the vault.</p>
    {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
    <div><button class="primary" onclick={save} disabled={busy || !config.name}>{busy ? 'Saving…' : 'Save integration'}</button></div>
  </div>
</details>
