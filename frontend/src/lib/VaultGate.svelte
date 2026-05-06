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
    <!-- Grid overlay for depth -->
    <div class="pointer-events-none absolute inset-0" style="
      background-image:
        linear-gradient(var(--color-line) 1px, transparent 1px),
        linear-gradient(90deg, var(--color-line) 1px, transparent 1px);
      background-size: 40px 40px;
      opacity: 0.4;
    "></div>
    <!-- Phosphor glow bloom -->
    <div class="pointer-events-none absolute inset-0">
      <div class="absolute left-1/2 top-1/3 h-[500px] w-[500px] -translate-x-1/2 -translate-y-1/2 rounded-full"
        style="background: radial-gradient(circle, rgba(0,255,136,0.06) 0%, transparent 65%);"></div>
    </div>

    <div class="relative w-[400px]">
      <!-- Logo + wordmark -->
      <div class="mb-8 flex flex-col items-center gap-4">
        <LogoIcon size={52} rounded={0} glow={true} />
        <div class="text-center">
          <div class="font-mono text-[13px] font-bold uppercase tracking-[0.3em] text-[var(--color-accent)]">
            BLACKNODE
          </div>
          <div class="mt-1 font-mono text-[9px] uppercase tracking-[0.3em] text-[var(--color-text-4)]">
            REMOTE OPS PLATFORM
          </div>
        </div>
      </div>

      <!-- Vault card -->
      <div
        class="overflow-hidden border hairline-strong surface-2 shadow-2xl"
        style="box-shadow: 0 0 0 1px var(--color-line-strong), 0 0 40px rgba(0,255,136,0.05), 0 32px 64px rgba(0,0,0,0.5);"
      >
        {#if !app.vault.initialized}
          <!-- SETUP -->
          <div class="border-b hairline px-5 py-3">
            <div class="flex items-center gap-2">
              <Key size="11" class="text-[var(--color-accent)]" />
              <span class="font-mono text-[10px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-1)]">INIT VAULT</span>
            </div>
          </div>
          <div class="space-y-4 p-5">
            <p class="font-mono text-[9px] uppercase tracking-widest leading-relaxed text-[var(--color-text-4)]">
              AES-256-GCM + ARGON2ID · NO RECOVERY · WRITE IT DOWN
            </p>
            <div class="space-y-2">
              <input
                type="password"
                class="w-full border hairline bg-[var(--color-surface-3)] px-3 py-2.5 font-mono text-[11px] text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
                placeholder="PASSPHRASE"
                bind:value={passphrase}
              />
              <input
                type="password"
                class="w-full border hairline bg-[var(--color-surface-3)] px-3 py-2.5 font-mono text-[11px] text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
                placeholder="CONFIRM PASSPHRASE"
                bind:value={confirmPass}
                onkeydown={(e) => e.key === "Enter" && setup()}
              />
            </div>
            {#if err}
              <p class="font-mono text-[9px] uppercase tracking-widest text-[var(--color-danger)]">ERR: {err}</p>
            {/if}
            <button
              onclick={setup}
              disabled={busy}
              class="flex w-full items-center justify-center gap-2 border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 py-2.5 font-mono text-[10px] font-bold uppercase tracking-[0.2em] text-[var(--color-accent)] hover:bg-[var(--color-accent)]/18 disabled:opacity-30 transition-all"
            >
              {#if busy}
                <Loader2 size="12" class="animate-spin" />INITIALIZING...
              {:else}
                CREATE VAULT
              {/if}
            </button>
          </div>
        {:else}
          <!-- UNLOCK -->
          <div class="border-b hairline px-5 py-3">
            <div class="flex items-center gap-2">
              <Key size="11" class="text-[var(--color-accent)]" />
              <span class="font-mono text-[10px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-1)]">UNLOCK VAULT</span>
            </div>
          </div>
          <div class="space-y-4 p-5">
            <p class="font-mono text-[9px] uppercase tracking-widest leading-relaxed text-[var(--color-text-4)]">
              ENTER PASSPHRASE TO DECRYPT KEYS FOR THIS SESSION
            </p>
            <input
              type="password"
              class="w-full border hairline bg-[var(--color-surface-3)] px-3 py-2.5 font-mono text-[11px] text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
              placeholder="PASSPHRASE"
              bind:value={passphrase}
              onkeydown={(e) => e.key === "Enter" && unlock()}
            />
            {#if err}
              <p class="font-mono text-[9px] uppercase tracking-widest text-[var(--color-danger)]">ERR: {err}</p>
            {/if}
            <button
              onclick={unlock}
              disabled={busy || !passphrase}
              class="flex w-full items-center justify-center gap-2 border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 py-2.5 font-mono text-[10px] font-bold uppercase tracking-[0.2em] text-[var(--color-accent)] hover:bg-[var(--color-accent)]/18 disabled:opacity-30 transition-all"
            >
              {#if busy}
                <Loader2 size="12" class="animate-spin" />UNLOCKING...
              {:else}
                UNLOCK
              {/if}
            </button>
          </div>
        {/if}
      </div>

      <!-- Version footer -->
      <div class="mt-4 text-center font-mono text-[8px] uppercase tracking-[0.3em] text-[var(--color-text-4)]/40">
        v0.1-alpha
      </div>
    </div>
  </div>
{:else}
  {@render children()}
{/if}
