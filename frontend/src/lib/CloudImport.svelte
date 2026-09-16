<script lang="ts">
  // Discover-then-review cloud import. Nothing is written until the user
  // confirms a selection, and credentials are used for the discovery call
  // only — the backend never stores them.
  import { CloudImportService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { DiscoveredHost, CloudCredentials, ImportResult } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { app } from "./state.svelte";
  import { platformBadge } from "./platform";
  import { Cloud, Loader2, X } from "@lucide/svelte";
  import Dialog from "./Dialog.svelte";

  type Props = { onclose: () => void };
  let { onclose }: Props = $props();

  type Provider = "aws" | "digitalocean" | "azure";
  let provider = $state<Provider>("aws");

  // AWS
  let accessKeyID = $state("");
  let secretAccessKey = $state("");
  let sessionToken = $state("");
  let region = $state("us-east-1");
  // DigitalOcean
  let token = $state("");
  // Azure
  let tenantID = $state("");
  let clientID = $state("");
  let clientSecret = $state("");
  let subscriptionID = $state("");

  let discovering = $state(false);
  let importing = $state(false);
  let err = $state("");
  let found = $state<DiscoveredHost[]>([]);
  let selected = $state<Set<string>>(new Set());
  let result = $state<ImportResult | null>(null);

  // Applied to every imported host.
  let group = $state("");
  let usernameOverride = $state("");
  let authMethod = $state("key");
  let keyID = $state("");

  function credentials(): CloudCredentials {
    return {
      provider,
      accessKeyID,
      secretAccessKey,
      sessionToken,
      region,
      token,
      tenantID,
      clientID,
      clientSecret,
      subscriptionID,
    } as CloudCredentials;
  }

  async function discover() {
    err = "";
    result = null;
    discovering = true;
    try {
      found = ((await CloudImportService.Discover(credentials())) ?? []) as DiscoveredHost[];
      // Pre-select everything that is not already saved, so the common case is
      // one click and duplicates are opt-in.
      selected = new Set(found.filter((h) => !h.alreadyExists).map((h) => h.host));
      if (found.length === 0) err = "No running instances found for those credentials.";
    } catch (e: any) {
      err = String(e?.message ?? e);
      found = [];
    } finally {
      discovering = false;
    }
  }

  function toggle(host: string) {
    const next = new Set(selected);
    if (next.has(host)) next.delete(host);
    else next.add(host);
    selected = next;
  }

  async function runImport() {
    err = "";
    importing = true;
    try {
      result = (await CloudImportService.Import({
        hosts: found.filter((h) => selected.has(h.host)),
        group,
        username: usernameOverride,
        authMethod,
        keyID,
      } as any)) as ImportResult;
      await app.refreshHosts();
      if (result.imported > 0) {
        app.toast("ok", "HOSTS IMPORTED", `${result.imported} added${result.skipped ? `, ${result.skipped} already present` : ""}.`);
      }
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      importing = false;
    }
  }

  let canDiscover = $derived(
    provider === "aws"
      ? accessKeyID.trim() !== "" && secretAccessKey.trim() !== ""
      : provider === "digitalocean"
      ? token.trim() !== ""
      : tenantID.trim() !== "" && clientID.trim() !== "" && clientSecret.trim() !== "" && subscriptionID.trim() !== "",
  );

  const INPUT =
    "mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 type-caption text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors";
</script>

<Dialog portal label="Import from cloud" onclose={onclose} panelClass="w-[min(95vw,780px)] max-h-[88vh] overflow-auto border hairline-strong surface-2 p-5">
  <div class="flex items-center gap-2">
    <Cloud size="14" class="text-[var(--color-accent)]" />
    <h2 class="type-body font-medium text-[var(--color-text-1)]">Import hosts from cloud</h2>
    <button class="ml-auto text-[var(--color-text-4)] hover:text-[var(--color-text-2)]" onclick={onclose} aria-label="Close"><X size="14" /></button>
  </div>
  <p class="mt-2 type-caption text-[var(--color-text-3)]">
    Credentials are used for this lookup only and are never stored. Use a read-only key: discovery
    needs nothing more than listing instances.
  </p>

  <!-- Provider -->
  <div class="mt-4 flex gap-1">
    {#each [["aws", "AWS EC2"], ["digitalocean", "DigitalOcean"], ["azure", "Azure"]] as [id, label] (id)}
      <button
        class="border px-3 py-1.5 type-caption transition-colors {provider === id
          ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent-soft)] text-[var(--color-accent)]'
          : 'hairline text-[var(--color-text-3)] hover:text-[var(--color-text-1)]'}"
        onclick={() => { provider = id as Provider; found = []; result = null; err = ""; }}
      >{label}</button>
    {/each}
  </div>

  <!-- Credentials -->
  <div class="mt-3 grid grid-cols-2 gap-2">
    {#if provider === "aws"}
      <label class="block"><span class="type-caption text-[var(--color-text-4)]">Access key ID</span>
        <input class={INPUT} bind:value={accessKeyID} placeholder="AKIA..." /></label>
      <label class="block"><span class="type-caption text-[var(--color-text-4)]">Secret access key</span>
        <input type="password" class={INPUT} bind:value={secretAccessKey} /></label>
      <label class="block"><span class="type-caption text-[var(--color-text-4)]">Region</span>
        <input class={INPUT} bind:value={region} placeholder="us-east-1" /></label>
      <label class="block"><span class="type-caption text-[var(--color-text-4)]">Session token (optional)</span>
        <input type="password" class={INPUT} bind:value={sessionToken} placeholder="for temporary credentials" /></label>
    {:else if provider === "digitalocean"}
      <label class="col-span-2 block"><span class="type-caption text-[var(--color-text-4)]">API token</span>
        <input type="password" class={INPUT} bind:value={token} placeholder="dop_v1_..." /></label>
    {:else}
      <label class="block"><span class="type-caption text-[var(--color-text-4)]">Tenant ID</span>
        <input class={INPUT} bind:value={tenantID} /></label>
      <label class="block"><span class="type-caption text-[var(--color-text-4)]">Subscription ID</span>
        <input class={INPUT} bind:value={subscriptionID} /></label>
      <label class="block"><span class="type-caption text-[var(--color-text-4)]">Client ID</span>
        <input class={INPUT} bind:value={clientID} /></label>
      <label class="block"><span class="type-caption text-[var(--color-text-4)]">Client secret</span>
        <input type="password" class={INPUT} bind:value={clientSecret} /></label>
    {/if}
  </div>

  <button
    class="mt-3 flex items-center gap-1.5 bg-[var(--color-accent)] px-3 py-2 type-caption font-medium text-[var(--color-surface-0)] disabled:opacity-40"
    disabled={discovering || !canDiscover}
    onclick={discover}
  >
    {#if discovering}<Loader2 size="11" class="animate-spin" /> Discovering...{:else}Discover instances{/if}
  </button>

  {#if err}<p role="alert" class="mt-3 type-caption text-[var(--color-danger)]">{err}</p>{/if}

  {#if found.length > 0}
    <!-- Applied to all -->
    <div class="mt-5 border-t hairline pt-4">
      <span class="type-eyebrow text-[var(--color-text-4)]">Applied to every imported host</span>
      <div class="mt-2 grid grid-cols-4 gap-2">
        <label class="block"><span class="type-caption text-[var(--color-text-4)]">Group</span>
          <input class={INPUT} bind:value={group} placeholder="e.g. aws-prod" /></label>
        <label class="block"><span class="type-caption text-[var(--color-text-4)]">Username</span>
          <input class={INPUT} bind:value={usernameOverride} placeholder="provider default" /></label>
        <label class="block"><span class="type-caption text-[var(--color-text-4)]">Auth</span>
          <select class={INPUT} bind:value={authMethod}>
            <option value="key">key</option>
            <option value="agent">agent</option>
            <option value="password">password</option>
          </select></label>
        <label class="block"><span class="type-caption text-[var(--color-text-4)]">Key</span>
          <select class={INPUT} bind:value={keyID}>
            <option value="">— none —</option>
            {#each app.keys as k (k.id)}<option value={k.id}>{k.name}</option>{/each}
          </select></label>
      </div>
    </div>

    <!-- Review -->
    <div class="mt-4 flex items-center gap-2">
      <span class="type-eyebrow text-[var(--color-text-4)]">{found.length} found · {selected.size} selected</span>
      <button class="ml-auto type-caption text-[var(--color-text-4)] hover:text-[var(--color-text-2)]"
        onclick={() => (selected = new Set(found.filter((h) => !h.alreadyExists).map((h) => h.host)))}>Select new</button>
      <button class="type-caption text-[var(--color-text-4)] hover:text-[var(--color-text-2)]"
        onclick={() => (selected = new Set())}>Clear</button>
    </div>
    <div class="mt-2 max-h-64 overflow-y-auto border hairline">
      {#each found as candidate (candidate.host)}
        {@const badge = platformBadge(candidate.platform)}
        <label class="flex items-center gap-2 border-b hairline px-2 py-1.5 last:border-b-0 {candidate.alreadyExists ? 'opacity-50' : ''}">
          <input type="checkbox" class="h-3 w-3 shrink-0" checked={selected.has(candidate.host)} onchange={() => toggle(candidate.host)} />
          {#if badge}
            <span class="shrink-0 rounded border px-1 type-nano font-semibold" style:color={badge.color} style:border-color="{badge.color}59" title={badge.label}>{badge.monogram}</span>
          {/if}
          <span class="w-1/4 truncate type-caption text-[var(--color-text-1)]">{candidate.name}</span>
          <span class="min-w-0 flex-1 truncate font-mono type-caption text-[var(--color-text-3)]">{candidate.username || "?"}@{candidate.host}</span>
          <span class="shrink-0 type-micro text-[var(--color-text-4)]">{candidate.region}</span>
          <span class="w-28 shrink-0 truncate type-micro text-[var(--color-text-4)]">{candidate.detail}</span>
          {#if candidate.alreadyExists}
            <span class="shrink-0 type-micro text-[var(--color-warn)]">already saved</span>
          {/if}
        </label>
      {/each}
    </div>

    <div class="mt-4 flex items-center justify-end gap-2">
      <button class="border hairline px-3 py-2 type-caption" onclick={onclose}>Cancel</button>
      <button
        class="bg-[var(--color-accent)] px-3 py-2 type-caption font-medium text-[var(--color-surface-0)] disabled:opacity-40"
        disabled={importing || selected.size === 0}
        onclick={runImport}
      >
        {#if importing}<Loader2 size="11" class="animate-spin" />{/if}Import {selected.size} host{selected.size === 1 ? "" : "s"}
      </button>
    </div>
  {/if}

  {#if result}
    <div class="mt-4 border hairline surface-3 p-3">
      <p class="type-caption text-[var(--color-text-2)]">
        Imported {result.imported} · skipped {result.skipped} already present
      </p>
      {#if result.errors && result.errors.length > 0}
        <p class="mt-2 type-caption text-[var(--color-danger)]">Failed:</p>
        <ul class="mt-1 list-inside list-disc type-caption text-[var(--color-danger)]">
          {#each result.errors as message (message)}<li>{message}</li>{/each}
        </ul>
      {/if}
    </div>
  {/if}
</Dialog>
