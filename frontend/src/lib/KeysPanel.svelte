<script lang="ts">
  import { KeyService } from "../../bindings/github.com/blacknode/blacknode";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import {
    KeyRound,
    Plus,
    Upload,
    Copy,
    Trash2,
    Loader2,
    X,
  } from "@lucide/svelte";

  let creating = $state(false);
  let importing = $state(false);

  let newName = $state("");
  let newType = $state("ed25519");
  let importText = $state("");
  let importPass = $state("");
  let busy = $state(false);
  let err = $state("");

  function reset() {
    newName = "";
    importText = "";
    importPass = "";
    err = "";
  }

  async function generate() {
    err = "";
    if (!newName) {
      err = "Name required";
      return;
    }
    busy = true;
    try {
      await KeyService.Generate(newName, newType);
      await app.refreshKeys();
      reset();
      creating = false;
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = false;
    }
  }

  async function importKey() {
    err = "";
    if (!newName || !importText) {
      err = "Name and PEM contents required";
      return;
    }
    busy = true;
    try {
      await KeyService.Import(newName, importText, importPass);
      await app.refreshKeys();
      reset();
      importing = false;
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = false;
    }
  }

  async function del(id: string, name: string) {
    if (
      !confirm(
        `Delete key "${name}"? Hosts referencing it will fail to connect.`,
      )
    )
      return;
    await KeyService.Delete(id);
    await app.refreshKeys();
  }

  function copyPub(text: string) {
    navigator.clipboard.writeText(text);
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={KeyRound}
    title="Keys"
    subtitle="Encrypted at rest with AES-256-GCM"
  >
    {#snippet actions()}
      <button
        class="flex items-center gap-1 rounded-md border hairline-strong px-2.5 py-1 text-[11px] text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)]"
        onclick={() => {
          importing = true;
          creating = false;
          reset();
        }}
      >
        <Upload size="11" /> import
      </button>
      <button
        class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-2.5 py-1 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90"
        onclick={() => {
          creating = true;
          importing = false;
          reset();
        }}
      >
        <Plus size="11" /> generate
      </button>
    {/snippet}
  </PageHeader>

  {#if creating}
    <div class="border-b hairline surface-1 p-4">
      <div class="flex items-center gap-2">
        <h3 class="text-xs font-medium text-[var(--color-text-1)]">
          Generate keypair
        </h3>
        <button
          class="ml-auto rounded p-0.5 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)]"
          onclick={() => (creating = false)}><X size="12" /></button
        >
      </div>
      <div class="mt-2 flex items-center gap-2">
        <input
          class="flex-1 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-1.5 text-xs outline-none"
          bind:value={newName}
          placeholder="key name"
        />
        <select
          class="rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-1.5 text-xs outline-none"
          bind:value={newType}
        >
          <option value="ed25519">ed25519</option>
          <option value="rsa">rsa-4096</option>
        </select>
        <button
          class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
          disabled={busy}
          onclick={generate}
        >
          {#if busy}<Loader2 size="11" class="animate-spin" />{:else}generate{/if}
        </button>
      </div>
      {#if err}
        <p class="mt-2 text-xs text-[var(--color-danger)]">{err}</p>
      {/if}
    </div>
  {/if}

  {#if importing}
    <div class="space-y-2 border-b hairline surface-1 p-4">
      <div class="flex items-center gap-2">
        <h3 class="text-xs font-medium text-[var(--color-text-1)]">
          Import existing key
        </h3>
        <button
          class="ml-auto rounded p-0.5 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)]"
          onclick={() => (importing = false)}><X size="12" /></button
        >
      </div>
      <div class="flex gap-2">
        <input
          class="flex-1 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-1.5 text-xs outline-none"
          bind:value={newName}
          placeholder="name"
        />
        <input
          type="password"
          class="w-48 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-1.5 text-xs outline-none"
          bind:value={importPass}
          placeholder="passphrase (optional)"
        />
      </div>
      <textarea
        class="h-32 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-xs outline-none"
        bind:value={importText}
        placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
      ></textarea>
      <div class="flex gap-2">
        <button
          class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
          disabled={busy}
          onclick={importKey}
        >
          {#if busy}<Loader2 size="11" class="animate-spin" />{:else}import{/if}
        </button>
      </div>
      {#if err}
        <p class="text-xs text-[var(--color-danger)]">{err}</p>
      {/if}
    </div>
  {/if}

  <div class="flex-1 overflow-y-auto p-4">
    <div class="space-y-2">
      {#each app.keys as k (k.id)}
        <div
          class="rounded-lg border hairline surface-2 p-3 transition-colors hover:border-[var(--color-line-strong)]"
        >
          <div class="flex items-center gap-2">
            <KeyRound size="13" class="text-[var(--color-accent)]" />
            <span class="text-sm font-medium text-[var(--color-text-1)]"
              >{k.name}</span
            >
            <span
              class="rounded-md border hairline px-1.5 py-0.5 text-[10px] font-mono text-[var(--color-text-2)]"
              >{k.keyType}</span
            >
            <span
              class="font-mono text-[10px] text-[var(--color-text-3)]"
              title={k.fingerprint}
            >
              {k.fingerprint}
            </span>
            <button
              class="ml-auto flex items-center gap-1 rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
              onclick={() => copyPub(k.publicKey)}
              title="Copy public key"
            >
              <Copy size="11" />
            </button>
            <button
              class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
              onclick={() => del(k.id, k.name)}
              title="Delete"
            >
              <Trash2 size="11" />
            </button>
          </div>
          <pre
            class="mt-2 overflow-x-auto rounded bg-[var(--color-code-bg)] px-3 py-2 font-mono text-[10px] text-[var(--color-text-3)]">{k.publicKey.trim()}</pre>
        </div>
      {/each}
    </div>
    {#if app.keys.length === 0 && !creating && !importing}
      <div class="flex h-full items-center justify-center">
        <div class="text-center">
          <KeyRound size="22" class="mx-auto text-[var(--color-text-4)]" />
          <p class="mt-2 text-xs text-[var(--color-text-3)]">
            No keys yet. Generate one or import an existing PEM.
          </p>
        </div>
      </div>
    {/if}
  </div>
</div>
