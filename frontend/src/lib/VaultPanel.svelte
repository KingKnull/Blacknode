<script lang="ts">
  import { app } from "./state.svelte";
  import {
    HostService,
  } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import { Shield, Key, Lock, Trash2, Eye, EyeOff, Edit3, Check, X } from "@lucide/svelte";

  // Reactive derivation of secrets grouped by host
  let showPasswords = $state<Record<string, boolean>>({});
  let editingSecret = $state<{ hostID: string; type: "ssh" | "sudo"; value: string } | null>(null);

  let hostsWithSecrets = $derived.by(() => {
    return app.hosts.map((h) => ({
      host: h,
      sshPassword: app.hostPasswords[h.id] ?? null,
      sudoPassword: app.hostSudoPasswords[h.id] ?? null,
    })).filter((h) => h.sshPassword || h.sudoPassword);
  });

  function toggleVisibility(id: string) {
    showPasswords[id] = !showPasswords[id];
  }

  function startEdit(hostID: string, type: "ssh" | "sudo", currentValue: string) {
    editingSecret = { hostID, type, value: currentValue };
  }

  async function saveEdit() {
    if (!editingSecret) return;
    try {
      if (editingSecret.type === "ssh") {
        await HostService.SetPassword(editingSecret.hostID, editingSecret.value);
        app.setPassword(editingSecret.hostID, editingSecret.value);
      } else {
        await HostService.SetSudoPassword(editingSecret.hostID, editingSecret.value);
        app.setSudoPassword(editingSecret.hostID, editingSecret.value);
      }
      app.toast("ok", "SECRET UPDATED", "Password saved to vault.");
    } catch (e: any) {
      app.toast("error", "SAVE FAILED", String(e?.message ?? e));
    }
    editingSecret = null;
  }

  async function deleteSecret(hostID: string, type: "ssh" | "sudo") {
    const label = type === "ssh" ? "SSH password" : "Sudo password";
    if (!confirm(`Delete ${label} for this host?`)) return;
    try {
      if (type === "ssh") {
        await HostService.SetPassword(hostID, "");
        delete app.hostPasswords[hostID];
      } else {
        await HostService.SetSudoPassword(hostID, "");
        delete app.hostSudoPasswords[hostID];
      }
      app.toast("ok", "SECRET DELETED", `${label} removed from vault.`);
    } catch (e: any) {
      app.toast("error", "DELETE FAILED", String(e?.message ?? e));
    }
  }

  function maskPassword(pw: string): string {
    return "•".repeat(Math.min(pw.length, 20));
  }
</script>

<div class="flex h-full flex-col overflow-hidden">
  <!-- Header -->
  <div class="flex items-center gap-2.5 border-b hairline px-4 py-3">
    <Shield size="14" class="text-[var(--color-accent)]" />
    <span class="font-mono text-[11px] font-bold uppercase tracking-widest text-[var(--color-text-1)]">
      Vault Secrets
    </span>
    <span class="ml-auto rounded border hairline px-1.5 py-0.5 font-mono text-[9px] text-[var(--color-text-4)]">
      {hostsWithSecrets.length} HOST{hostsWithSecrets.length === 1 ? '' : 'S'}
    </span>
  </div>

  <!-- Info bar -->
  <div class="border-b hairline bg-[var(--color-surface-1)] px-4 py-2">
    <div class="flex items-center gap-2">
      <Lock size="10" class="text-[var(--color-accent)]/50" />
      <span class="font-mono text-[9px] text-[var(--color-text-3)]">
        All secrets are encrypted with your vault master key.
      </span>
    </div>
  </div>

  <!-- Secret list -->
  <div class="flex-1 overflow-y-auto">
    {#if hostsWithSecrets.length === 0}
      <div class="flex flex-col items-center justify-center gap-3 py-16 text-center">
        <Shield size="28" class="text-[var(--color-text-4)]" />
        <div class="font-mono text-[11px] text-[var(--color-text-3)]">
          No saved secrets yet
        </div>
        <div class="max-w-[240px] font-mono text-[9px] text-[var(--color-text-4)]">
          When you save SSH or sudo passwords, they will appear here. Use the terminal to save passwords on first connect.
        </div>
      </div>
    {:else}
      {#each hostsWithSecrets as entry (entry.host.id)}
        <div class="border-b hairline">
          <!-- Host header -->
          <div class="flex items-center gap-2 bg-[var(--color-surface-1)] px-4 py-2">
            <span class="h-1.5 w-1.5 rounded-full {app.connectedHosts.has(entry.host.id) ? 'bg-[var(--color-success)] shadow-[0_0_4px_var(--color-success)]' : 'bg-[var(--color-text-4)]'}"></span>
            <span class="font-mono text-[11px] font-medium text-[var(--color-text-1)]">{entry.host.name}</span>
            <span class="font-mono text-[9px] text-[var(--color-text-4)]">{entry.host.username}@{entry.host.host}</span>
          </div>

          <!-- SSH Password -->
          {#if entry.sshPassword}
            <div class="flex items-center gap-2 px-4 py-2.5 hover:bg-[var(--color-surface-2)]/50 transition-colors">
              <Key size="11" class="shrink-0 text-[var(--color-accent)]/60" />
              <span class="shrink-0 font-mono text-[9px] font-bold uppercase tracking-widest text-[var(--color-text-3)] w-16">SSH</span>

              {#if editingSecret?.hostID === entry.host.id && editingSecret?.type === "ssh"}
                <input
                  type="password"
                  class="flex-1 border hairline bg-[var(--color-surface-3)] px-2 py-1 font-mono text-[10px] text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50"
                  bind:value={editingSecret.value}
                  onkeydown={(e) => e.key === "Enter" && saveEdit()}
                />
                <button class="p-1 text-[var(--color-accent)] hover:text-[var(--color-text-1)]" onclick={saveEdit} title="Save">
                  <Check size="12" />
                </button>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-text-1)]" onclick={() => editingSecret = null} title="Cancel">
                  <X size="12" />
                </button>
              {:else}
                <span class="flex-1 font-mono text-[10px] text-[var(--color-text-2)]">
                  {showPasswords[`${entry.host.id}:ssh`] ? entry.sshPassword : maskPassword(entry.sshPassword)}
                </span>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-text-2)] transition-colors" onclick={() => toggleVisibility(`${entry.host.id}:ssh`)} title="Toggle visibility">
                  {#if showPasswords[`${entry.host.id}:ssh`]}
                    <EyeOff size="12" />
                  {:else}
                    <Eye size="12" />
                  {/if}
                </button>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-text-2)] transition-colors" onclick={() => startEdit(entry.host.id, "ssh", entry.sshPassword ?? "")} title="Edit">
                  <Edit3 size="12" />
                </button>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-danger)] transition-colors" onclick={() => deleteSecret(entry.host.id, "ssh")} title="Delete">
                  <Trash2 size="12" />
                </button>
              {/if}
            </div>
          {/if}

          <!-- Sudo Password -->
          {#if entry.sudoPassword}
            <div class="flex items-center gap-2 px-4 py-2.5 hover:bg-[var(--color-surface-2)]/50 transition-colors">
              <Shield size="11" class="shrink-0 text-[var(--color-warn)]/60" />
              <span class="shrink-0 font-mono text-[9px] font-bold uppercase tracking-widest text-[var(--color-text-3)] w-16">SUDO</span>

              {#if editingSecret?.hostID === entry.host.id && editingSecret?.type === "sudo"}
                <input
                  type="password"
                  class="flex-1 border hairline bg-[var(--color-surface-3)] px-2 py-1 font-mono text-[10px] text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50"
                  bind:value={editingSecret.value}
                  onkeydown={(e) => e.key === "Enter" && saveEdit()}
                />
                <button class="p-1 text-[var(--color-accent)] hover:text-[var(--color-text-1)]" onclick={saveEdit} title="Save">
                  <Check size="12" />
                </button>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-text-1)]" onclick={() => editingSecret = null} title="Cancel">
                  <X size="12" />
                </button>
              {:else}
                <span class="flex-1 font-mono text-[10px] text-[var(--color-text-2)]">
                  {showPasswords[`${entry.host.id}:sudo`] ? entry.sudoPassword : maskPassword(entry.sudoPassword)}
                </span>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-text-2)] transition-colors" onclick={() => toggleVisibility(`${entry.host.id}:sudo`)} title="Toggle visibility">
                  {#if showPasswords[`${entry.host.id}:sudo`]}
                    <EyeOff size="12" />
                  {:else}
                    <Eye size="12" />
                  {/if}
                </button>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-text-2)] transition-colors" onclick={() => startEdit(entry.host.id, "sudo", entry.sudoPassword ?? "")} title="Edit">
                  <Edit3 size="12" />
                </button>
                <button class="p-1 text-[var(--color-text-4)] hover:text-[var(--color-danger)] transition-colors" onclick={() => deleteSecret(entry.host.id, "sudo")} title="Delete">
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
