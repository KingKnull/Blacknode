<script lang="ts">
  import { HostService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Host } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import { Server, X, Loader2, Eye, EyeOff, ShieldCheck } from "@lucide/svelte";

  type Props = {
    host?: Host | null;
    onclose: () => void;
    onsaved: () => void;
  };
  let { host, onclose, onsaved }: Props = $props();

  // svelte-ignore state_referenced_locally
  let name = $state(host?.name ?? "");
  // svelte-ignore state_referenced_locally
  let hostName = $state(host?.host ?? "");
  // svelte-ignore state_referenced_locally
  let port = $state(host?.port ?? 22);
  // svelte-ignore state_referenced_locally
  let username = $state(host?.username ?? "");
  // svelte-ignore state_referenced_locally
  let authMethod = $state(host?.authMethod ?? "password");
  // svelte-ignore state_referenced_locally
  let keyID = $state(host?.keyID ?? "");
  // svelte-ignore state_referenced_locally
  let group = $state(host?.group ?? "");
  // svelte-ignore state_referenced_locally
  let environment = $state(host?.environment ?? "");
  // svelte-ignore state_referenced_locally
  let proxyJump = $state(host?.proxyJump ?? "");
  // svelte-ignore state_referenced_locally
  let notes = $state(host?.notes ?? "");
  // Password: pre-fill from the in-memory cache if editing an existing host.
  // svelte-ignore state_referenced_locally
  let password = $state(host?.id ? (app.hostPasswords[host.id] ?? "") : "");
  let showPassword = $state(false);
  // Sudo password: separate from SSH auth password.
  // svelte-ignore state_referenced_locally
  let sudoPassword = $state(host?.id ? (app.hostSudoPasswords[host.id] ?? "") : "");
  let showSudoPassword = $state(false);
  let sudoSameAsSSH = $state(false);
  let busy = $state(false);
  let err = $state("");

  async function save() {
    err = "";
    if (!name || !hostName || !username) {
      err = "Name, host, and username are required";
      return;
    }
    busy = true;
    try {
      let savedHost: Host;
      if (host?.id) {
        await HostService.Update({
          ...host,
          name,
          host: hostName,
          port,
          username,
          authMethod,
          keyID: authMethod === "key" ? keyID : "",
          group,
          environment,
          proxyJump,
          notes,
        } as Host);
        savedHost = { ...host, name, host: hostName, port, username, authMethod, keyID, group, environment, proxyJump, notes } as Host;
      } else {
        savedHost = (await HostService.Create({
          name,
          host: hostName,
          port,
          username,
          authMethod,
          keyID: authMethod === "key" ? keyID : "",
          group,
          environment,
          proxyJump,
          notes,
          tags: [],
        } as unknown as Host)) as Host;
      }
      // Persist the password in the vault if auth method is password.
      if (authMethod === "password" && savedHost?.id) {
        try {
          await HostService.SetPassword(savedHost.id, password);
          if (password) {
            app.setPassword(savedHost.id, password);
          }
        } catch (pe: any) {
          // Non-fatal: password save failed (e.g. vault locked)
          console.warn("password save failed:", pe);
        }
      }
      // Persist sudo password (works for any auth method).
      if (savedHost?.id) {
        const sudoPw = sudoSameAsSSH ? password : sudoPassword;
        try {
          await HostService.SetSudoPassword(savedHost.id, sudoPw);
          if (sudoPw) {
            app.setSudoPassword(savedHost.id, sudoPw);
          }
        } catch (pe: any) {
          console.warn("sudo password save failed:", pe);
        }
      }
      await app.refreshHosts();
      onsaved();
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = false;
    }
  }
</script>

<div
  class="fixed inset-0 z-50 flex items-center justify-center"
  style="background: rgba(0,0,0,0.82); backdrop-filter: blur(4px);"
  role="presentation"
  onclick={(e) => { if (e.target === e.currentTarget) onclose(); }}
  onkeydown={(e) => e.key === "Escape" && onclose()}
>
  <div
    class="w-[520px] max-h-[85vh] overflow-y-auto overflow-x-hidden border hairline-strong surface-2 shadow-2xl fade-up"
    style="box-shadow: 0 0 0 1px var(--color-line-strong), 0 0 60px rgba(0,255,136,0.04), 0 40px 80px rgba(0,0,0,0.6);"
  >
    <!-- Header -->
    <div class="flex items-center gap-2.5 border-b hairline px-5 py-3">
      <Server size="12" class="text-[var(--color-accent)]" />
      <span class="font-mono text-[10px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-1)]">
        {host ? 'EDIT HOST' : 'NEW HOST'}
      </span>
      <button
        class="ml-auto border border-[var(--color-line)] p-1 text-[var(--color-text-4)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-danger)] transition-all"
        onclick={onclose}
        title="Close"
      >
        <X size="12" />
      </button>
    </div>

    <!-- Form -->
    <div class="space-y-3 p-5">
      <!-- Name -->
      <label class="block">
        <span class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-4)]">NAME</span>
        <input
          class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-[11px] text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
          bind:value={name}
          placeholder="prod-web-01"
        />
      </label>

      <!-- Host + Port -->
      <div class="grid grid-cols-[1fr_88px] gap-2">
        <label class="block">
          <span class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-4)]">HOST</span>
          <input
            class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-[11px] text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
            bind:value={hostName}
            placeholder="10.0.0.5"
          />
        </label>
        <label class="block">
          <span class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-4)]">PORT</span>
          <input
            type="number"
            class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-[11px] text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
            bind:value={port}
          />
        </label>
      </div>

      <!-- User + Auth -->
      <div class="grid grid-cols-2 gap-2">
        <label class="block">
          <span class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-4)]">USER</span>
          <input
            class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-[11px] text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
            bind:value={username}
            placeholder="ubuntu"
          />
        </label>
        <label class="block">
          <span class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-4)]">AUTH</span>
          <select
            class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-[11px] text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
            bind:value={authMethod}
          >
            <option value="password">PASSWORD</option>
            <option value="key">KEY</option>
            <option value="agent">AGENT</option>
          </select>
        </label>
      </div>

      <!-- Key selector -->
      {#if authMethod === 'key'}
        <label class="block">
          <span class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-4)]">SSH KEY</span>
          <select
            class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-[11px] text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
            bind:value={keyID}
          >
            <option value="">— SELECT KEY —</option>
            {#each app.keys as k (k.id)}
              <option value={k.id}>{k.name} ({k.keyType})</option>
            {/each}
          </select>
        </label>
      {/if}

      <!-- Password -->
      {#if authMethod === 'password'}
        <label class="block">
          <span class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-4)]">
            PASSWORD <span class="text-[var(--color-text-4)]/60">(VAULT)</span>
          </span>
          <div class="relative mt-1">
            <input
              type={showPassword ? 'text' : 'password'}
              class="w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 pr-9 font-mono text-[11px] text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
              bind:value={password}
              placeholder="leave blank to keep current"
              autocomplete="new-password"
            />
            <button
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 text-[var(--color-text-4)] hover:text-[var(--color-accent)] transition-colors"
              onclick={() => (showPassword = !showPassword)}
              title={showPassword ? 'Hide' : 'Show'}
            >
              {#if showPassword}<EyeOff size="12" />{:else}<Eye size="12" />{/if}
            </button>
          </div>
          <p class="mt-1 font-mono text-[9px] uppercase tracking-widest text-[var(--color-text-4)]">
            AES-256 ENCRYPTED · SKIPS PROMPT AT CONNECT TIME
          </p>
        </label>
      {/if}

      <!-- Group + Environment -->
      <div class="grid grid-cols-2 gap-2">
        <label class="block">
          <span class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-4)]">GROUP</span>
          <input
            class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-[11px] text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
            bind:value={group}
            placeholder="web · db · cache"
          />
        </label>
        <label class="block">
          <span class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-4)]">ENV</span>
          <select
            class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-[11px] outline-none focus:border-[var(--color-accent)]/50 transition-colors {environment === 'production' ? 'text-[var(--color-danger)] border-[var(--color-danger)]/30' : 'text-[var(--color-text-1)]'}"
            bind:value={environment}
          >
            <option value="">— NONE —</option>
            <option value="dev">DEV</option>
            <option value="staging">STAGING</option>
            <option value="production">PRODUCTION ⚠</option>
          </select>
        </label>
      </div>

      <!-- ProxyJump -->
      <label class="block">
        <span class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-4)]">PROXYJUMP (BASTION)</span>
        <select
          class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-[11px] text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
          bind:value={proxyJump}
        >
          <option value="">— DIRECT CONNECT —</option>
          {#each app.hosts.filter((h) => h.id !== host?.id) as h (h.id)}
            <option value={h.name}>{h.name} ({h.username}@{h.host}:{h.port})</option>
          {/each}
        </select>
        <p class="mt-1 font-mono text-[9px] uppercase tracking-widest text-[var(--color-text-4)]">
          TUNNELS THROUGH SELECTED BASTION · CYCLES DETECTED AT CONNECT
        </p>
      </label>

      <!-- Sudo password (all auth methods) -->
      <div class="border hairline surface-3 p-3">
        <div class="flex items-center gap-2 mb-2">
          <ShieldCheck size="11" class="text-[var(--color-warn)]" />
          <span class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-2)]">SUDO PASSWORD</span>
        </div>
        <label class="flex items-center gap-2 mb-2">
          <input
            type="checkbox"
            class="accent-[var(--color-accent)]"
            bind:checked={sudoSameAsSSH}
          />
          <span class="font-mono text-[9px] uppercase tracking-widest text-[var(--color-text-3)]">
            Same as SSH password
          </span>
        </label>
        {#if !sudoSameAsSSH}
          <div class="relative">
            <input
              type={showSudoPassword ? 'text' : 'password'}
              class="w-full border hairline bg-[var(--color-surface-2)] px-3 py-2 pr-9 font-mono text-[11px] text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
              bind:value={sudoPassword}
              placeholder="sudo / root password"
              autocomplete="new-password"
            />
            <button
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 text-[var(--color-text-4)] hover:text-[var(--color-accent)] transition-colors"
              onclick={() => (showSudoPassword = !showSudoPassword)}
              title={showSudoPassword ? 'Hide' : 'Show'}
            >
              {#if showSudoPassword}<EyeOff size="12" />{:else}<Eye size="12" />{/if}
            </button>
          </div>
        {/if}
        <p class="mt-1 font-mono text-[9px] uppercase tracking-widest text-[var(--color-text-4)]">
          AES-256 ENCRYPTED · AUTO-FILLS WHEN SUDO PROMPT DETECTED
        </p>
      </div>

      <!-- Notes -->
      <label class="block">
        <span class="font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-[var(--color-text-4)]">NOTES</span>
        <textarea
          class="mt-1 h-14 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-[11px] text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors resize-none"
          bind:value={notes}
        ></textarea>
      </label>

      {#if err}
        <p class="font-mono text-[10px] text-[var(--color-danger)] uppercase tracking-widest">
          ERR: {err}
        </p>
      {/if}
    </div>

    <!-- Footer -->
    <div class="flex items-center justify-end gap-2 border-t hairline px-5 py-3">
      <button
        class="border border-[var(--color-line)] px-3 py-1.5 font-mono text-[10px] uppercase tracking-widest text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
        onclick={onclose}>CANCEL</button
      >
      <button
        class="flex items-center gap-1.5 border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 px-3 py-1.5 font-mono text-[10px] uppercase tracking-widest text-[var(--color-accent)] hover:bg-[var(--color-accent)]/18 hover:shadow-[0_0_16px_rgba(0,255,136,0.08)] disabled:opacity-30 disabled:cursor-not-allowed transition-all"
        disabled={busy}
        onclick={save}
      >
        {#if busy}
          <Loader2 size="10" class="animate-spin" />SAVING...
        {:else}
          SAVE HOST
        {/if}
      </button>
    </div>
  </div>
</div>
