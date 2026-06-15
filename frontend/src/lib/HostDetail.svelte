<script lang="ts">
  import { app } from "./state.svelte";
  import { bus } from "./events";
  import { envBadge } from "./envColor";
  import { HostService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Host } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import HostEditor from "./HostEditor.svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";
  import {
    X, Plug, FolderOpen, Pencil, Copy, Trash2,
    Server, KeyRound, Lock, Terminal as TerminalIcon, Network, Tag, FileText, Clock,
  } from "@lucide/svelte";

  let host = $derived(
    app.selectedHostID ? app.hosts.find((h) => h.id === app.selectedHostID) ?? null : null,
  );
  let env = $derived(envBadge(host?.environment));
  let connected = $derived(host ? app.connectedHosts.has(host.id) : false);
  let keyName = $derived(
    host?.keyID ? app.keys.find((k) => k.id === host!.keyID)?.name ?? host.keyID : "",
  );

  let editing = $state(false);
  let confirmingDelete = $state(false);

  function close() {
    app.hostDetailOpen = false;
  }

  function connect() {
    if (!host) return;
    bus.emit("connect-host", { hostID: host.id });
    close();
  }

  function openSFTP() {
    if (!host) return;
    app.selectedHostID = host.id;
    app.view = "files";
    close();
  }

  async function duplicate() {
    if (!host) return;
    const copy = { ...host, name: `${host.name} (copy)`, tags: [...(host.tags ?? [])] } as unknown as Host;
    delete (copy as any).id;
    try {
      await HostService.Create(copy);
      await app.refreshHosts();
      app.toast("ok", "Host duplicated", host.name);
    } catch (e: any) {
      app.toast("error", "Duplicate failed", String(e?.message ?? e));
    }
  }

  async function doDelete() {
    if (!host) return;
    const id = host.id;
    await HostService.Delete(id);
    if (app.selectedHostID === id) app.selectedHostID = null;
    await app.refreshHosts();
    confirmingDelete = false;
    close();
  }

  function fmtRelative(ts: number): string {
    if (!ts) return "Never";
    const secs = Math.floor((Date.now() - ts * 1000) / 1000);
    if (secs < 60) return "Just now";
    if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
    if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
    return `${Math.floor(secs / 86400)}d ago`;
  }

  const authLabel: Record<string, string> = {
    password: "Password",
    key: "SSH key",
    agent: "SSH agent",
  };
</script>

{#if app.hostDetailOpen && host}
  <!-- click-away -->
  <button class="absolute inset-0 z-30 cursor-default" aria-label="Close detail" onclick={close}></button>

  <aside
    class="slide-up absolute left-0 top-0 z-40 flex h-full w-[340px] flex-col border-r hairline-strong surface-1 shadow-2xl shadow-black/40"
  >
    <!-- Header -->
    <div class="flex items-start gap-3 border-b hairline px-4 py-3.5">
      <div
        class="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border hairline bg-[var(--color-surface-2)] text-[var(--color-accent)]"
      >
        <Server size="16" />
      </div>
      <div class="min-w-0 flex-1">
        <div class="flex items-center gap-2">
          <span class="truncate type-title text-[var(--color-text-1)]">{host.name}</span>
          {#if env.label}
            <span class="shrink-0 rounded px-1.5 type-micro font-semibold" style:color={env.color} style:background={env.bg}>{env.label}</span>
          {/if}
        </div>
        <div class="mt-0.5 flex items-center gap-1.5 type-caption text-[var(--color-text-3)]">
          <span class="h-1.5 w-1.5 rounded-full {connected ? 'bg-[var(--color-success)]' : 'bg-[var(--color-text-4)]'}"></span>
          {connected ? "Connected" : "Not connected"}
        </div>
      </div>
      <button
        class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-[var(--color-text-4)] transition-colors hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-2)]"
        onclick={close}
        aria-label="Close"
      >
        <X size="15" />
      </button>
    </div>

    <!-- Body -->
    <div class="flex-1 overflow-y-auto px-4 py-4">
      <!-- Connect -->
      <button
        class="flex w-full items-center justify-center gap-2 rounded-md bg-[var(--color-accent)] py-2.5 type-body font-semibold text-white transition-opacity hover:opacity-90"
        onclick={connect}
      >
        <Plug size="15" /> {connected ? "Open another session" : "Connect"}
      </button>

      <!-- Quick actions -->
      <div class="mt-2 grid grid-cols-2 gap-2">
        <button class="flex items-center justify-center gap-1.5 rounded-md border hairline py-2 type-caption text-[var(--color-text-2)] transition-colors hover:bg-[var(--color-surface-2)]" onclick={openSFTP}>
          <FolderOpen size="14" /> SFTP
        </button>
        <button class="flex items-center justify-center gap-1.5 rounded-md border hairline py-2 type-caption text-[var(--color-text-2)] transition-colors hover:bg-[var(--color-surface-2)]" onclick={() => (editing = true)}>
          <Pencil size="14" /> Edit
        </button>
        <button class="flex items-center justify-center gap-1.5 rounded-md border hairline py-2 type-caption text-[var(--color-text-2)] transition-colors hover:bg-[var(--color-surface-2)]" onclick={duplicate}>
          <Copy size="14" /> Duplicate
        </button>
        <button class="flex items-center justify-center gap-1.5 rounded-md border hairline py-2 type-caption text-[var(--color-danger)] transition-colors hover:bg-[var(--color-danger)]/10" onclick={() => (confirmingDelete = true)}>
          <Trash2 size="14" /> Delete
        </button>
      </div>

      <!-- Connection facts -->
      <dl class="mt-5 space-y-px">
        {#snippet row(Icon: any, label: string, value: string, mono = false)}
          <div class="flex items-center gap-3 rounded-md px-2 py-2 hover:bg-[var(--color-surface-2)]">
            <Icon size="14" class="shrink-0 text-[var(--color-text-4)]" />
            <dt class="w-20 shrink-0 type-caption text-[var(--color-text-4)]">{label}</dt>
            <dd class="min-w-0 flex-1 truncate {mono ? 'font-mono' : ''} type-caption text-[var(--color-text-1)]">{value}</dd>
          </div>
        {/snippet}

        {@render row(Network, "Address", `${host.host}:${host.port}`, true)}
        {@render row(Server, "Username", host.username, true)}
        {@render row(host.authMethod === "key" ? KeyRound : Lock, "Auth", host.authMethod === "key" && keyName ? `SSH key · ${keyName}` : authLabel[host.authMethod] ?? host.authMethod)}
        {#if host.proxyJump}
          {@render row(TerminalIcon, "Jump host", host.proxyJump)}
        {/if}
        {#if host.group}
          {@render row(Server, "Group", host.group)}
        {/if}
        {@render row(Clock, "Last seen", fmtRelative(host.lastConnectedAt))}
      </dl>

      <!-- Tags -->
      {#if host.tags && host.tags.length > 0}
        <div class="mt-4">
          <div class="mb-1.5 flex items-center gap-1.5 type-eyebrow text-[var(--color-text-4)]">
            <Tag size="11" /> Tags
          </div>
          <div class="flex flex-wrap gap-1.5">
            {#each host.tags as tag (tag)}
              <span class="rounded bg-[var(--color-surface-3)] px-2 py-0.5 type-caption text-[var(--color-text-2)]">{tag}</span>
            {/each}
          </div>
        </div>
      {/if}

      <!-- Notes -->
      {#if host.notes}
        <div class="mt-4">
          <div class="mb-1.5 flex items-center gap-1.5 type-eyebrow text-[var(--color-text-4)]">
            <FileText size="11" /> Notes
          </div>
          <p class="type-caption leading-relaxed text-[var(--color-text-2)] whitespace-pre-wrap">{host.notes}</p>
        </div>
      {/if}
    </div>
  </aside>
{/if}

{#if editing && host}
  <HostEditor host={host} onclose={() => (editing = false)} onsaved={() => (editing = false)} />
{/if}

{#if confirmingDelete && host}
  <ConfirmDanger
    title="Delete host"
    body="Delete '{host.name}'? Associated snippets, commands, and credentials are removed from the local vault."
    severity="warn"
    productionHosts={host.environment === "production" ? [host.name] : []}
    onCancel={() => (confirmingDelete = false)}
    onConfirm={doDelete}
  />
{/if}
