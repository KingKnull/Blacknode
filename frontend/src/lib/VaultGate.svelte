<script lang="ts">
  import type { Snippet } from "svelte";
  import { VaultService } from "../../bindings/github.com/blacknode/blacknode";
  import { app } from "./state.svelte";
  import { Key, Loader2 } from "@lucide/svelte";
  import LogoIcon from "./logo/LogoIcon.svelte";

  type Props = { children: Snippet };
  let { children }: Props = $props();

  let passphrase = $state("");
  let confirmPass = $state("");
  let busy = $state(false);
  let err = $state("");

  async function setup() {
    err = "";
    if (passphrase !== confirmPass) {
      err = "Passphrases do not match";
      return;
    }
    if (passphrase.length < 8) {
      err = "Use at least 8 characters";
      return;
    }
    busy = true;
    try {
      await VaultService.Setup(passphrase);
      await app.refreshAll();
      passphrase = "";
      confirmPass = "";
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = false;
    }
  }

  async function unlock() {
    err = "";
    busy = true;
    try {
      await VaultService.Unlock(passphrase);
      await app.refreshAll();
      passphrase = "";
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = false;
    }
  }
</script>

{#if !app.vault.initialized || !app.vault.unlocked}
  <div class="relative flex h-screen items-center justify-center overflow-hidden bg-[var(--color-surface-0)]">
    <!-- Ambient gradient backdrop -->
    <div class="pointer-events-none absolute inset-0">
      <div class="absolute -top-40 left-1/2 h-[480px] w-[480px] -translate-x-1/2 rounded-full bg-[var(--color-accent)]/10 blur-3xl"></div>
      <div class="absolute bottom-0 right-0 h-[300px] w-[300px] rounded-full bg-[var(--color-info)]/8 blur-3xl"></div>
    </div>

    <div class="relative w-[420px]">
      <div class="mb-6 flex flex-col items-center gap-3">
        <LogoIcon size={56} rounded={14} glow={true} />
        <div class="text-center">
          <div
            class="text-base font-semibold tracking-[0.04em] text-[var(--color-text-1)]"
            style="font-feature-settings: 'cv11', 'ss01';"
          >
            blacknode
          </div>
          <div
            class="mt-1 text-[10px] uppercase tracking-[0.22em] text-[var(--color-text-4)]"
          >
            remote ops platform
          </div>
        </div>
      </div>

      <div
        class="space-y-4 rounded-xl border hairline-strong surface-2 p-6 shadow-2xl shadow-black/40 backdrop-blur"
      >
        {#if !app.vault.initialized}
          <div class="flex items-center gap-2">
            <Key size="14" class="text-[var(--color-accent)]" />
            <h2 class="text-sm font-semibold">Set up your vault</h2>
          </div>
          <p class="text-xs text-[var(--color-text-3)]">
            The vault encrypts SSH keys with AES-256-GCM, derived from your passphrase
            via Argon2id. There's no recovery — write it down somewhere safe.
          </p>
          <div class="space-y-2">
            <input
              type="password"
              class="w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 text-sm outline-none placeholder:text-[var(--color-text-4)]"
              placeholder="Passphrase"
              bind:value={passphrase}
            />
            <input
              type="password"
              class="w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 text-sm outline-none placeholder:text-[var(--color-text-4)]"
              placeholder="Confirm passphrase"
              bind:value={confirmPass}
              onkeydown={(e) => e.key === "Enter" && setup()}
            />
          </div>
          {#if err}
            <p class="text-xs text-[var(--color-danger)]">{err}</p>
          {/if}
          <button
            onclick={setup}
            disabled={busy}
            class="flex w-full items-center justify-center gap-2 rounded-md bg-[var(--color-accent)] py-2 text-sm font-medium text-[var(--color-surface-0)] transition-opacity hover:opacity-90 disabled:opacity-50"
          >
            {#if busy}
              <Loader2 size="14" class="animate-spin" />Creating…
            {:else}
              Create vault
            {/if}
          </button>
        {:else}
          <div class="flex items-center gap-2">
            <Key size="14" class="text-[var(--color-accent)]" />
            <h2 class="text-sm font-semibold">Unlock vault</h2>
          </div>
          <p class="text-xs text-[var(--color-text-3)]">
            Enter your passphrase to decrypt stored keys for this session.
          </p>
          <input
            type="password"
            class="w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 text-sm outline-none placeholder:text-[var(--color-text-4)]"
            placeholder="Passphrase"
            bind:value={passphrase}
            onkeydown={(e) => e.key === "Enter" && unlock()}
          />
          {#if err}
            <p class="text-xs text-[var(--color-danger)]">{err}</p>
          {/if}
          <button
            onclick={unlock}
            disabled={busy || !passphrase}
            class="flex w-full items-center justify-center gap-2 rounded-md bg-[var(--color-accent)] py-2 text-sm font-medium text-[var(--color-surface-0)] transition-opacity hover:opacity-90 disabled:opacity-50"
          >
            {#if busy}
              <Loader2 size="14" class="animate-spin" />Unlocking…
            {:else}
              Unlock
            {/if}
          </button>
        {/if}
      </div>
    </div>
  </div>
{:else}
  {@render children()}
{/if}
