<script lang="ts">
  import { SFTPService } from "../../bindings/github.com/blacknode/blacknode";
  import type { SFTPEntry } from "../../bindings/github.com/blacknode/blacknode/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import RemoteEditor from "./RemoteEditor.svelte";
  import {
    Folder,
    File as FileIcon,
    ChevronUp,
    Upload,
    RefreshCw,
    Download,
    Trash2,
    FolderOpen,
    FileCode,
  } from "@lucide/svelte";

  let path = $state("");
  let entries = $state<SFTPEntry[]>([]);
  let busy = $state(false);
  let err = $state("");
  let editingPath: string | null = $state(null);

  let host = $derived(
    app.selectedHostID
      ? app.hosts.find((h) => h.id === app.selectedHostID)
      : null,
  );

  async function reload() {
    if (!host) return;
    err = "";
    busy = true;
    try {
      const password = app.hostPasswords[host.id] ?? "";
      entries = ((await SFTPService.List(host.id, password, path)) ??
        []) as SFTPEntry[];
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = false;
    }
  }

  function joinPath(base: string, name: string) {
    if (!base) return name;
    if (base.endsWith("/")) return base + name;
    return `${base}/${name}`;
  }

  async function open(e: SFTPEntry) {
    if (e.isDir) {
      path = joinPath(path, e.name);
      await reload();
    }
  }

  async function up() {
    if (!path) return;
    const i = path.lastIndexOf("/");
    path = i <= 0 ? "/" : path.slice(0, i);
    if (path === "") path = "/";
    await reload();
  }

  async function uploadFile(file: File) {
    if (!host) return;
    const buf = await file.arrayBuffer();
    let bin = "";
    const bytes = new Uint8Array(buf);
    for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    const b64 = btoa(bin);
    const password = app.hostPasswords[host.id] ?? "";
    await SFTPService.Upload(host.id, password, path || ".", file.name, b64);
    await reload();
  }

  async function download(e: SFTPEntry) {
    if (!host) return;
    const password = app.hostPasswords[host.id] ?? "";
    const b64 = await SFTPService.Download(
      host.id,
      password,
      joinPath(path, e.name),
    );
    const bin = atob(b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    const blob = new Blob([bytes]);
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = e.name;
    a.click();
    URL.revokeObjectURL(url);
  }

  async function remove(e: SFTPEntry) {
    if (!host) return;
    if (!confirm(`Delete ${e.name}?`)) return;
    const password = app.hostPasswords[host.id] ?? "";
    await SFTPService.Remove(host.id, password, joinPath(path, e.name));
    await reload();
  }

  function onDrop(ev: DragEvent) {
    ev.preventDefault();
    const files = ev.dataTransfer?.files;
    if (!files) return;
    for (const f of files) void uploadFile(f);
  }

  function fmtSize(n: number) {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
    return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
  }

  $effect(() => {
    if (host) {
      path = "";
      void reload();
    }
  });
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Folder}
    title="Files"
    subtitle={host ? `SFTP — ${host.name}` : "Select a host from the sidebar"}
  />

  {#if !host}
    <div class="flex flex-1 items-center justify-center">
      <div class="text-center">
        <FolderOpen size="22" class="mx-auto text-[var(--color-text-4)]" />
        <p class="mt-2 text-xs text-[var(--color-text-3)]">
          Pick a host on the left to browse its filesystem.
        </p>
      </div>
    </div>
  {:else}
    <div class="flex items-center gap-2 border-b hairline surface-1 px-4 py-2">
      <button
        class="rounded p-1.5 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)] disabled:opacity-30"
        onclick={up}
        disabled={!path}
        title="Up one directory"
      >
        <ChevronUp size="14" />
      </button>
      <input
        class="flex-1 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-1.5 font-mono text-xs outline-none"
        bind:value={path}
        onkeydown={(e) => e.key === "Enter" && reload()}
        placeholder="(home)"
      />
      <button
        class="flex items-center gap-1 rounded p-1.5 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={reload}
        title="Reload"
      >
        <RefreshCw size="13" />
      </button>
      <label
        class="flex cursor-pointer items-center gap-1 rounded-md border hairline-strong px-2.5 py-1.5 text-xs text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)]"
      >
        <Upload size="12" /> upload
        <input
          type="file"
          class="hidden"
          onchange={(e) => {
            const f = (e.currentTarget as HTMLInputElement).files?.[0];
            if (f) void uploadFile(f);
          }}
        />
      </label>
    </div>

    <div
      class="flex-1 overflow-y-auto"
      role="region"
      ondragover={(e) => e.preventDefault()}
      ondrop={onDrop}
    >
      {#if err}
        <div class="m-4 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-3 text-xs text-[var(--color-danger)]">
          {err}
        </div>
      {/if}
      {#if busy && entries.length === 0}
        <div class="p-4 text-center text-xs text-[var(--color-text-3)]">Loading…</div>
      {/if}
      <div class="divide-y divide-[var(--color-line)]">
        {#each entries as e (e.name)}
          <div
            class="grid grid-cols-[1fr_100px_120px_80px] items-center gap-2 px-4 py-1.5 text-xs transition-colors hover:bg-[var(--color-surface-2)]"
          >
            <button
              class="flex min-w-0 items-center gap-2 truncate text-left"
              onclick={() => open(e)}
              ondblclick={() => open(e)}
            >
              {#if e.isDir}
                <Folder size="13" class="shrink-0 text-[var(--color-info)]" />
              {:else}
                <FileIcon size="13" class="shrink-0 text-[var(--color-text-3)]" />
              {/if}
              <span class="truncate">{e.name}</span>
            </button>
            <span class="text-right font-mono text-[10px] text-[var(--color-text-3)]"
              >{e.isDir ? "" : fmtSize(e.size)}</span
            >
            <span class="font-mono text-[10px] text-[var(--color-text-4)]"
              >{e.mode}</span
            >
            <div class="flex justify-end gap-0.5">
              {#if !e.isDir}
                <button
                  class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-accent)]"
                  onclick={() => (editingPath = joinPath(path, e.name))}
                  title="Edit"
                >
                  <FileCode size="11" />
                </button>
                <button
                  class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
                  onclick={() => download(e)}
                  title="Download"
                >
                  <Download size="11" />
                </button>
              {/if}
              <button
                class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
                onclick={() => remove(e)}
                title="Delete"
              >
                <Trash2 size="11" />
              </button>
            </div>
          </div>
        {/each}
      </div>
      {#if entries.length === 0 && !busy && !err}
        <div class="p-6 text-center text-xs text-[var(--color-text-3)]">
          empty
        </div>
      {/if}
    </div>
  {/if}
</div>

{#if editingPath && host}
  <RemoteEditor
    hostID={host.id}
    remotePath={editingPath}
    onClose={() => {
      editingPath = null;
      void reload();
    }}
  />
{/if}
