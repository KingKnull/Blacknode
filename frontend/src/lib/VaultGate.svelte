<script lang="ts">
  import type { Snippet } from "svelte";
  import { VaultService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import { app } from "./state.svelte";
  import { Key, Loader2 } from "@lucide/svelte";
  import LogoIcon from "./logo/LogoIcon.svelte";

  type Props = { children: Snippet };
  let { children }: Props = $props();

  let passphrase = $state("");
  let confirmPass = $state("");
  let busy = $state(false);
  let err = $state("");
  let rememberMe = $state(true);

  async function setup() {
    err = "";
    if (passphrase !== confirmPass) { err = "Passphrases do not match"; return; }
    if (passphrase.length < 8) { err = "Use at least 8 characters"; return; }
    busy = true;
    try {
      await VaultService.Setup(passphrase);
      await app.refreshAll();
      passphrase = ""; confirmPass = "";
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally { busy = false; }
  }

  async function unlock() {
    err = "";
    busy = true;
    try {
      if (rememberMe) {
        await VaultService.UnlockAndRemember(passphrase, 60);
      } else {
        await VaultService.Unlock(passphrase);
      }
      await app.refreshAll();
      passphrase = "";
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally { busy = false; }
  }
</script>

{#if !app.vault.initialized || !app.vault.unlocked}
  <div class="relative flex h-full w-full items-center justify-center overflow-hidden bg-[var(--color-surface-0)]">
    <!-- Grid overlay -->
    <div class="pointer-events-none absolute inset-0" style="
      background-image: linear-gradient(var(--color-line) 1px, transparent 1px), linear-gradient(90deg, var(--color-line) 1px, transparent 1px);
      background-size: 40px 40px; opacity: 0.35; animation: pulse-soft 8s ease-in-out infinite;
    "></div>
    <!-- Glow bloom -->
    <div class="pointer-events-none absolute inset-0">
      <div class="absolute left-1/2 top-1/3 h-[600px] w-[600px] -translate-x-1/2 -translate-y-1/2 rounded-full"
        style="background: radial-gradient(circle, rgba(0,255,136,0.08) 0%, rgba(0,255,136,0.02) 40%, transparent 65%);"></div>
    </div>

    <div class="relative w-[400px]">
      <!-- Logo -->
      <div class="mb-8 flex flex-col items-center gap-4">
        <LogoIcon size={52} rounded={0} glow={true} />
        <div class="text-center">
          <div class="font-mono text-sm font-bold tracking-[0.25em] text-[var(--color-accent)]">BLACKNODE</div>
          <div class="mt-1 text-xs text-[var(--color-text-4)]">Remote ops platform</div>
        </div>
      </div>

      <!-- Card -->
      <div class="overflow-hidden border hairline-strong surface-2 shadow-2xl"
        style="backdrop-filter: blur(16px) saturate(1.3); box-shadow: 0 0 0 1px var(--color-line-strong), 0 0 60px rgba(0,255,136,0.06), 0 40px 80px rgba(0,0,0,0.6);">

        {#if !app.vault.initialized}
          <!-- SETUP -->
          <div class="border-b hairline px-5 py-3 flex items-center gap-2">
            <Key size="11" class="text-[var(--color-accent)]" />
            <span class="font-mono text-[10px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-1)]">Init vault</span>
          </div>
          <div class="space-y-4 p-5">
            <p class="text-xs text-[var(--color-text-3)] leading-relaxed">
              Encrypted with AES-256-GCM + Argon2id. No recovery possible — store your passphrase somewhere safe.
            </p>
            <div class="space-y-2">
              <input type="password"
                class="w-full border hairline bg-[var(--color-surface-3)] px-3 py-2.5 font-mono text-sm text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
                placeholder="Passphrase" bind:value={passphrase} />
              <input type="password"
                class="w-full border hairline bg-[var(--color-surface-3)] px-3 py-2.5 font-mono text-sm text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
                placeholder="Confirm passphrase" bind:value={confirmPass}
                onkeydown={(e) => e.key === "Enter" && setup()} />
            </div>
            {#if err}<p class="text-xs text-[var(--color-danger)]">{err}</p>{/if}
            <button onclick={setup} disabled={busy}
              class="flex w-full items-center justify-center gap-2 border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 py-2.5 text-sm font-semibold text-[var(--color-accent)] hover:bg-[var(--color-accent)]/18 hover:shadow-[0_0_20px_rgba(0,255,136,0.1)] disabled:opacity-30 transition-all">
              {#if busy}<Loader2 size="14" class="animate-spin" /> Initializing...{:else}Create vault{/if}
            </button>
          </div>

        {:else}
          <!-- UNLOCK -->
          <div class="border-b hairline px-5 py-3 flex items-center gap-2">
            <Key size="11" class="text-[var(--color-accent)]" />
            <span class="font-mono text-[10px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-1)]">Unlock vault</span>
          </div>
          <div class="space-y-4 p-5">
            <p class="text-xs text-[var(--color-text-3)] leading-relaxed">
              Enter your passphrase to decrypt keys for this session.
            </p>
            <input type="password"
              class="w-full border hairline bg-[var(--color-surface-3)] px-3 py-2.5 font-mono text-sm text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
              placeholder="Passphrase" bind:value={passphrase}
              onkeydown={(e) => e.key === "Enter" && unlock()} />
            <div class="flex items-center gap-2 px-1">
              <input id="rememberMe" type="checkbox" bind:checked={rememberMe}
                class="h-3 w-3 border hairline bg-[var(--color-surface-3)] text-[var(--color-accent)] focus:ring-0" />
              <label for="rememberMe" class="select-none text-xs text-[var(--color-text-4)] hover:text-[var(--color-text-2)] cursor-pointer transition-colors">
                Remember for 60 days
              </label>
            </div>
            {#if err}<p class="text-xs text-[var(--color-danger)]">{err}</p>{/if}
            <button onclick={unlock} disabled={busy || !passphrase}
              class="flex w-full items-center justify-center gap-2 border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 py-2.5 text-sm font-semibold text-[var(--color-accent)] hover:bg-[var(--color-accent)]/18 hover:shadow-[0_0_20px_rgba(0,255,136,0.1)] disabled:opacity-30 transition-all">
              {#if busy}<Loader2 size="14" class="animate-spin" /> Unlocking...{:else}Unlock{/if}
            </button>
          </div>
        {/if}
      </div>

      <div class="mt-4 text-center font-mono text-[9px] text-[var(--color-text-4)]/40">v0.1-alpha</div>
    </div>
  </div>
{:else}
  {@render children()}
{/if}
