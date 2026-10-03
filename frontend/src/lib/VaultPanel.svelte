<script lang="ts">
  import { app } from "./state.svelte";
  import {
    HostService,
  } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import { Shield, Key, Lock, Trash2, Edit3, Check, X } from "@lucide/svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";

  // This panel is deliberately write-only. It can tell you a credential exists
  // and let you replace or delete it, but it cannot show you the plaintext —
  // there's no longer any way for the frontend to ask for one. Secrets are
  // unsealed in the Go connect path (sshconn.Dialer.ResolveSecret) and never
  // cross the bridge. A reveal button would mean re-exposing every saved
  // password to the renderer for the sake of a convenience feature.
  let editingSecret = $state<{ hostID: string; type: "ssh" | "sudo"; value: string } | null>(null);

  let hostsWithSecrets = $derived.by(() =>
    app.hosts
      .map((h) => ({
        host: h,
        hasSSH: app.hasSavedPassword(h.id),
        hasSudo: app.hasSavedSudoPassword(h.id),
      }))
      .filter((h) => h.hasSSH || h.hasSudo),
  );

  // Replacing a secret starts from empty — we don't have the old value to
  // prefill, by design.
  function startEdit(hostID: string, type: "ssh" | "sudo") {
    editingSecret = { hostID, type, value: "" };
  }

  async function saveEdit() {
    if (!editingSecret || !editingSecret.value) return;
    try {
      if (editingSecret.type === "ssh") {
        await HostService.SetPassword(editingSecret.hostID, editingSecret.value);
      } else {
        await HostService.SetSudoPassword(editingSecret.hostID, editingSecret.value);
      }
      await app.refreshSecretStatus();
      app.toast("ok", "SECRET UPDATED", "Password saved to vault.");
    } catch (e: any) {
      app.toast("error", "SAVE FAILED", String(e?.message ?? e));
    }
    editingSecret = null;
  }

  let pendingDelete = $state<{ hostID: string; type: "ssh" | "sudo" } | null>(null);

  async function deleteSecret() {
    if (!pendingDelete) return;
    const { hostID, type } = pendingDelete;
    pendingDelete = null;
    const label = type === "ssh" ? "SSH password" : "Sudo password";
    try {
      if (type === "ssh") {
        await HostService.ClearPassword(hostID);
      } else {
        await HostService.ClearSudoPassword(hostID);
      }
      await app.refreshSecretStatus();
      app.toast("ok", "SECRET DELETED", `${label} removed from vault.`);
    } catch (e: any) {
      app.toast("error", "DELETE FAILED", String(e?.message ?? e));
    }
  }
</script>

<div class="flex h-full flex-col overflow-hidden">
  <!-- Header -->
  <div class="flex items-center gap-2.5 border-b hairline px-4 py-3">
    <Shield size="14" class="text-[var(--color-accent)]" />
    <span class="font-mono type-eyebrow text-[var(--color-text-1)]">
      Vault Secrets
    </span>
    <span class="ml-auto rounded border hairline px-1.5 py-0.5 font-mono type-nano text-[var(--color-text-4)]">
      {hostsWithSecrets.length} HOST{hostsWithSecrets.length === 1 ? '' : 'S'}
    </span>
  </div>

  <!-- Info bar -->
  <div class="border-b hairline bg-[var(--color-surface-1)] px-4 py-2">
    <div class="flex items-center gap-2">
      <Lock size="10" class="text-[var(--color-accent)]/50" />
      <span class="font-mono type-nano text-[var(--color-text-3)]">
        Encrypted with your vault master key. Saved passwords are never shown —
        they're decrypted only when connecting.
      </span>
    </div>
  </div>

  <!-- Secret list -->
  <div class="flex-1 overflow-y-auto">
    {#if hostsWithSecrets.length === 0}
      <div class="flex flex-col items-center justify-center gap-3 py-16 text-center">
        <Shield size="28" class="text-[var(--color-text-4)]" />
        <div class="font-mono type-caption text-[var(--color-text-3)]">
          No saved secrets yet
        </div>
        <div class="max-w-[240px] font-mono type-nano text-[var(--color-text-4)]">
          When you save SSH or sudo passwords, they will appear here. Use the terminal to save passwords on first connect.
        </div>
      </div>
    {:else}
      {#each hostsWithSecrets as entry (entry.host.id)}
        <div class="border-b hairline">
          <!-- Host header -->
          <div class="flex items-center gap-2 bg-[var(--color-surface-1)] px-4 py-2">
            <span class="h-1.5 w-1.5 rounded-full {app.connectedHosts.has(entry.host.id) ? 'bg-[var(--color-success)] shadow-[0_0_4px_var(--color-success)]' : 'bg-[var(--color-text-4)]'}"></span>
            <span class="font-mono type-caption font-medium text-[var(--color-text-1)]">{entry.host.name}</span>
            <span class="font-mono type-nano text-[var(--color-text-4)]">{entry.host.username}@{entry.host.host}</span>
          </div>

          <!-- SSH Password -->
          {#if entry.hasSSH}
            <div class="flex items-center gap-2 px-4 py-2.5 hover:bg-[var(--color-surface-2)]/50 transition-colors">
              <Key size="11" class="shrink-0 text-[var(--color-accent)]/60" />
              <span class="shrink-0 font-mono type-eyebrow text-[var(--color-text-3)] w-16">SSH</span>

              {#if editingSecret?.hostID === entry.host.id && editingSecret?.type === "ssh"}
                <input
                  type="password"
                  placeholder="New SSH password"
                  class="flex-1 border hairline bg-[var(--color-surface-3)] px-2 py-1 font-mono type-micro text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50"
                  bind:value={editingSecret.value}
                  onkeydown={(e) => e.key === "Enter" && saveEdit()}
                />
                <button class="p-1 text-[var(--color-accent)] hover:text-[var(--color-text-1)] disabled:opacity-30" onclick={saveEdit} disabled={!editingSecret.value} title="Save">
                  <Check size="12" />
                </button>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-text-1)]" onclick={() => editingSecret = null} title="Cancel">
                  <X size="12" />
                </button>
              {:else}
                <span class="flex-1 font-mono type-micro text-[var(--color-text-3)]">
                  Saved · hidden
                </span>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-text-2)] transition-colors" onclick={() => startEdit(entry.host.id, "ssh")} title="Replace">
                  <Edit3 size="12" />
                </button>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-danger)] transition-colors" onclick={() => (pendingDelete = { hostID: entry.host.id, type: "ssh" })} title="Delete" aria-label="Delete SSH password for {entry.host.name}">
                  <Trash2 size="12" />
                </button>
              {/if}
            </div>
          {/if}

          <!-- Sudo Password -->
          {#if entry.hasSudo}
            <div class="flex items-center gap-2 px-4 py-2.5 hover:bg-[var(--color-surface-2)]/50 transition-colors">
              <Shield size="11" class="shrink-0 text-[var(--color-warn)]/60" />
              <span class="shrink-0 font-mono type-eyebrow text-[var(--color-text-3)] w-16">SUDO</span>

              {#if editingSecret?.hostID === entry.host.id && editingSecret?.type === "sudo"}
                <input
                  type="password"
                  placeholder="New sudo password"
                  class="flex-1 border hairline bg-[var(--color-surface-3)] px-2 py-1 font-mono type-micro text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50"
                  bind:value={editingSecret.value}
                  onkeydown={(e) => e.key === "Enter" && saveEdit()}
                />
                <button class="p-1 text-[var(--color-accent)] hover:text-[var(--color-text-1)] disabled:opacity-30" onclick={saveEdit} disabled={!editingSecret.value} title="Save">
                  <Check size="12" />
                </button>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-text-1)]" onclick={() => editingSecret = null} title="Cancel">
                  <X size="12" />
                </button>
              {:else}
                <span class="flex-1 font-mono type-micro text-[var(--color-text-3)]">
                  Saved · hidden
                </span>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-text-2)] transition-colors" onclick={() => startEdit(entry.host.id, "sudo")} title="Replace">
                  <Edit3 size="12" />
                </button>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-danger)] transition-colors" onclick={() => (pendingDelete = { hostID: entry.host.id, type: "sudo" })} title="Delete" aria-label="Delete sudo password for {entry.host.name}">
                  <Trash2 size="12" />
                </button>
              {/if}
            </div>
          {/if}
        </div>
      {/each}
    {/if}
  </div>
</div>

{#if pendingDelete}
  <ConfirmDanger
    title="DELETE SECRET"
    body="Remove the {pendingDelete.type === 'ssh' ? 'SSH password' : 'sudo password'} for this host from the vault? Connections that rely on it will prompt again."
    severity="warn"
    productionHosts={[]}
    onCancel={() => (pendingDelete = null)}
    onConfirm={deleteSecret}
  />
{/if}
