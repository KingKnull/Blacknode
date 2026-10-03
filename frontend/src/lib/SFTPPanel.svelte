<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { SFTPService, TransferService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import TransferQueue from "./TransferQueue.svelte";
  import type { SFTPEntry } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
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
  import ConfirmDanger from "./ConfirmDanger.svelte";
  import EmptyState from "./EmptyState.svelte";
  import Skeleton from "./Skeleton.svelte";

  let path = $state("");
  let entries = $state<SFTPEntry[]>([]);
  let busy = $state(false);
  let err = $state("");
  let editingPath: string | null = $state(null);
  let isDraggingOver = $state(false);
  let overwrite = $state(false);
  let listRequest = 0;
  onMount(() => {
    const offDrop = Events.On("files:dropped", (event: any) => { isDraggingOver = false; if (Array.isArray(event.data)) void uploadPaths(event.data); });
    const offTransfer = Events.On("transfers:changed", (event: any) => { if (event.data?.status === "completed" && event.data.request?.hostID === host?.id) void reload(); });
    return () => { listRequest++; offDrop(); offTransfer(); };
  });

  let host = $derived(
    app.selectedHostID
      ? app.hosts.find((h) => h.id === app.selectedHostID)
      : null,
  );

  async function reload() {
    const target = host;
    const directory = path;
    const request = ++listRequest;
    if (!target) return;
    err = "";
    busy = true;
    try {
      const result = await SFTPService.List(target.id, directory);
      if (request === listRequest) entries = result ?? [];
    } catch (e: any) {
      if (request !== listRequest) return;
      let msg = String(e?.message ?? e);
      if (msg.trimStart().startsWith("{")) {
        try {
          const parsed = JSON.parse(msg);
          if (parsed.message) msg = parsed.message;
        } catch {}
      }
      err = msg;
    } finally {
      if (request === listRequest) busy = false;
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
    } else {
      editingPath = joinPath(path, e.name);
    }
  }

  async function up() {
    if (!path) return;
    const i = path.lastIndexOf("/");
    path = i <= 0 ? "/" : path.slice(0, i);
    if (path === "") path = "/";
    await reload();
  }

  function uploadTarget() {
    return host ? { hostID: host.id, directory: path || ".", overwrite } : null;
  }

  async function uploadPaths(paths: string[], target = uploadTarget()) {
    if (!target) return;
    for (const localPath of paths) {
      const name = localPath.replace(/\\/g, "/").split("/").pop()!;
      try { await TransferService.Enqueue({ hostID: target.hostID, direction: "upload", localPath, remotePath: joinPath(target.directory,name), overwrite: target.overwrite, directory: false }); }
      catch (e) { err = String(e); }
    }
  }
  async function pickUploads(directory = false) {
    const target = uploadTarget();
    if (!target) return;
    try { await uploadPaths(await TransferService.PickUploads(directory) ?? [], target); }
    catch (e) { err = String(e); }
  }

  async function download(e: SFTPEntry) {
    if (!host) return;
    const target = host;
    const remotePath = joinPath(path,e.name);
    try {
      const localPath = await TransferService.PickDownload(e.name,e.isDir);
      if (localPath) await TransferService.Enqueue({ hostID: target.id, direction: "download", localPath, remotePath, overwrite: true, directory: e.isDir });
    } catch (e) { err = String(e); }
  }

  let entryToDelete = $state<(SFTPEntry & { hostID: string; remotePath: string }) | null>(null);
  async function remove() {
    if (!entryToDelete || !host) return;
    // Recursive only for directories. Passing it unconditionally would be
    // harmless for files but would turn a mis-click on a directory into an
    // unbounded delete; passing it never would make non-empty directories
    // undeletable from the UI.
    await SFTPService.Remove(entryToDelete.hostID, entryToDelete.remotePath, entryToDelete.isDir);
    await reload();
    entryToDelete = null;
  }

  function onDrop(ev: DragEvent) {
    ev.preventDefault();
    isDraggingOver = false;
    // The native Wails drop event supplies paths without reading file contents
    // into JavaScript. Uploads use the same queue as the file picker.
  }

  function onDragEnter(ev: DragEvent) {
    ev.preventDefault();
    isDraggingOver = true;
  }

  function onDragLeave(ev: DragEvent) {
    ev.preventDefault();
    // Only set false if we're leaving the actual container, not moving to a child
    if (ev.currentTarget === ev.target) {
      isDraggingOver = false;
    }
  }

  function fmtSize(n: number) {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
    return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
  }

  $effect(() => {
    const hostID = host?.id;
    untrack(() => {
      listRequest++;
      path = "";
      entries = [];
      err = "";
      busy = false;
      editingPath = null;
      entryToDelete = null;
      if (hostID) void reload();
    });
  });
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Folder}
    title="Files"
    subtitle={host ? `SFTP — ${host.name}` : "Select a host from the sidebar"}
  />

  {#if !host}
    <EmptyState
      icon={FolderOpen}
      title="No host selected"
      description="Pick a host on the left to browse its filesystem."
    />
  {:else}
    <div class="flex flex-wrap items-center gap-2 border-b hairline surface-1 px-4 py-2">
      <button
        class="rounded p-1.5 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)] disabled:opacity-30"
        onclick={up}
        disabled={!path || path === '/'}
        title="Up one directory"
        aria-label="Up one directory"
      >
        <ChevronUp size="14" />
      </button>
      <input
        class="min-w-0 flex-1 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-1.5 font-mono type-caption outline-none"
        aria-label="Remote directory"
        bind:value={path}
        onkeydown={(e) => e.key === "Enter" && reload()}
        placeholder="(home)"
      />
      <button
        class="flex items-center gap-1 rounded p-1.5 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={reload}
        title="Reload"
        aria-label="Reload directory"
      >
        <RefreshCw size="13" />
      </button>
      <button onclick={() => pickUploads()}
        class="flex cursor-pointer items-center gap-1 rounded-md border hairline-strong px-2.5 py-1.5 type-caption text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)]"
      >
        <Upload size="12" /> upload
      </button>
      <button class="rounded border hairline px-2 py-1.5 type-caption" onclick={() => pickUploads(true)}>Folder</button>
      <label class="flex items-center gap-1 type-caption"><input type="checkbox" bind:checked={overwrite} />Replace files</label>
    </div>

    <div
      class="relative flex-1 overflow-y-auto"
      data-file-drop-target
      role="region"
      aria-label="File browser — drop files here to upload"
      ondragover={(e) => { e.preventDefault(); isDraggingOver = true; }}
      ondragenter={onDragEnter}
      ondragleave={onDragLeave}
      ondrop={onDrop}
    >
      {#if isDraggingOver}
        <div class="pointer-events-none absolute inset-0 z-40 flex items-center justify-center bg-[var(--color-surface-1)]/80 fade-up" style="backdrop-filter: blur(8px);">
          <div class="flex flex-col items-center gap-3 rounded-2xl border-2 border-dashed border-[var(--color-accent)]/50 bg-[var(--color-surface-2)] p-10 shadow-2xl">
            <Upload size="40" class="text-[var(--color-accent)] pulse-soft" />
            <span class="font-mono type-eyebrow text-[var(--color-text-1)]">Drop files to upload</span>
            <span class="font-mono type-micro text-[var(--color-text-3)]">to {path || "(home)"}</span>
          </div>
        </div>
      {/if}
      {#if err}
        <div class="m-4 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-3 type-caption text-[var(--color-danger)]" role="alert">
          {err}
        </div>
      {/if}
      {#if busy && entries.length === 0}
        <Skeleton rows={8} rowClass="h-6" />
      {/if}
      <div class="divide-y divide-[var(--color-line)]">
        {#each entries as e (e.name)}
          <div
            class="grid grid-cols-[1fr_100px_120px_80px] items-center gap-2 px-4 py-1.5 type-caption transition-colors hover:bg-[var(--color-surface-2)]"
          >
            <button
              class="flex min-w-0 items-center gap-2 truncate text-left"
              onclick={() => open(e)}
            >
              {#if e.isDir}
                <Folder size="13" class="shrink-0 text-[var(--color-info)]" />
              {:else}
                <FileIcon size="13" class="shrink-0 text-[var(--color-text-3)]" />
              {/if}
              <span class="truncate">{e.name}</span>
            </button>
            <span class="text-right font-mono type-micro text-[var(--color-text-3)]"
              >{e.isDir ? "" : fmtSize(e.size)}</span
            >
            <span class="font-mono type-micro text-[var(--color-text-4)]"
              >{e.mode}</span
            >
            <div class="flex justify-end gap-0.5">
              {#if !e.isDir}
                <button
                  class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-accent)]"
                  onclick={() => (editingPath = joinPath(path, e.name))}
                  title="Edit"
                  aria-label="Edit {e.name}"
                >
                  <FileCode size="11" />
                </button>
              {/if}
                <button
                  class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
                  onclick={() => download(e)}
                  title="Download"
                  aria-label="Download {e.name}"
                >
                  <Download size="11" />
                </button>
              <button
                class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
                onclick={() => (entryToDelete = { ...e, hostID: host!.id, remotePath: joinPath(path, e.name) })}
                title="Delete"
                aria-label="Delete {e.name}"
              >
                <Trash2 size="11" />
              </button>
            </div>
          </div>
        {/each}
      </div>
      {#if entries.length === 0 && !busy && !err}
        <EmptyState
          icon={FolderOpen}
          title="Empty directory"
          description="Nothing here yet. Drop files anywhere in this pane to upload."
          compact
        />
      {/if}
    </div>
  {/if}
  <TransferQueue />
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

{#if entryToDelete}
  <ConfirmDanger
    title={entryToDelete.isDir ? "DELETE FOLDER" : "DELETE FILE"}
    body={entryToDelete.isDir
      ? `Delete the folder '${entryToDelete.name}' and everything inside it? This removes all contents recursively and cannot be undone.`
      : `Are you sure you want to delete '${entryToDelete.name}'? This action cannot be undone and will permanently remove the item from the remote server.`}
    severity={entryToDelete.isDir ? "block-without-confirm" : "warn"}
    productionHosts={host?.environment === 'production' ? [host.name] : []}
    onCancel={() => (entryToDelete = null)}
    onConfirm={remove}
  />
{/if}
