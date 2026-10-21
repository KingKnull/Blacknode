<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { SFTPService, TransferService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import TransferQueue from "./TransferQueue.svelte";
  import type { SFTPEntry, SFTPDeletePreview, SFTPDeleteReceipt, SFTPSearchResult } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
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
    return () => { listRequest++; clearSearch(); offDrop(); offTransfer(); };
  });

  let host = $derived(
    app.selectedHostID
      ? app.hosts.find((h) => h.id === app.selectedHostID)
      : null,
  );

  async function reload() {
    clearSearch();
    const target = host;
    const directory = path;
    const request = ++listRequest;
    if (!target) return;
    void loadReceipts(target.id);
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

  let searchQuery = $state("");
  let searchedQuery = $state("");
  let searchResult = $state<SFTPSearchResult | null>(null);
  let searchBusy = $state(false);
  let searchError = $state("");
  let searchNotice = $state("");
  let searchRequest = 0;
  let activeSearch: ReturnType<typeof SFTPService.Search> | null = null;

  function clearSearch(canceled = false) {
    searchRequest++;
    void activeSearch?.cancel();
    activeSearch = null;
    searchBusy = false;
    searchResult = null;
    searchError = "";
    searchNotice = canceled ? "Search canceled — incomplete; no results retained." : "";
  }

  async function searchFiles() {
    const target = host;
    if (!target || !searchQuery.trim()) return;
    clearSearch();
    const request = searchRequest;
    const directory = path;
    searchedQuery = searchQuery;
    searchBusy = true;
    const pending = SFTPService.Search(target.id, directory, searchedQuery);
    activeSearch = pending;
    try {
      const result = await pending;
      if (request === searchRequest && host?.id === target.id && path === directory) searchResult = result;
    } catch (e) {
      if (request === searchRequest) searchError = String(e);
    } finally {
      if (request === searchRequest) { searchBusy = false; activeSearch = null; }
    }
  }

  async function openSearchResult(entry: SFTPEntry, containingFolder = false) {
    if (entry.symlink) return;
    if (entry.isDir || containingFolder) {
      path = entry.isDir ? entry.path : entry.path.slice(0, entry.path.lastIndexOf("/")) || "/";
      await reload();
    } else {
      editingPath = entry.path;
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

  let deleteReview = $state<{ preview: SFTPDeletePreview; hostName: string; production: boolean } | null>(null);
  let restoreReview = $state<{ receipt: SFTPDeleteReceipt; hostName: string; production: boolean } | null>(null);
  let receipts = $state<SFTPDeleteReceipt[]>([]);
  let reviewBusy = $state(false);
  let mutationBusy = $state(false);
  let reviewRequest = 0;
  let receiptRequest = 0;

  async function loadReceipts(hostID: string) {
    if (host?.id !== hostID) return;
    const request = ++receiptRequest;
    try {
      const result = await SFTPService.DeletedFiles(hostID);
      if (host?.id === hostID && request === receiptRequest) receipts = result ?? [];
    } catch (e) {
      if (host?.id === hostID && request === receiptRequest) err = `Cannot load recovery receipts: ${String(e)}`;
    }
  }

  function rememberReceipt(receipt: SFTPDeleteReceipt) {
    if (host?.id !== receipt.hostID) return;
    receiptRequest++;
    receipts = [...receipts.filter((item) => item.id !== receipt.id), receipt];
  }

  async function previewDelete(entry: SFTPEntry) {
    const target = host;
    if (!target || reviewBusy || mutationBusy) return;
    const request = ++reviewRequest;
    reviewBusy = true;
    err = "";
    try {
      const preview = await SFTPService.PreviewDelete(target.id, entry.path);
      if (request === reviewRequest && host?.id === target.id) {
        deleteReview = { preview, hostName: target.name, production: target.environment === "production" };
      }
    } catch (e) {
      if (request === reviewRequest && host?.id === target.id) err = String(e);
    } finally {
      if (request === reviewRequest) reviewBusy = false;
    }
  }

  async function remove() {
    const review = deleteReview;
    if (!review || mutationBusy) return;
    mutationBusy = true;
    try {
      rememberReceipt(await SFTPService.DeleteReviewed(review.preview.hostID, review.preview.id));
      if (host?.id === review.preview.hostID) await reload();
    } catch (e) {
      if (host?.id === review.preview.hostID) err = `${String(e)} — preview the file again before another deletion attempt.`;
    } finally {
      await loadReceipts(review.preview.hostID);
      if (deleteReview === review) deleteReview = null;
      mutationBusy = false;
    }
  }

  function previewRestore(receipt: SFTPDeleteReceipt) {
    const target = host;
    if (!target || mutationBusy || receipt.hostID !== target.id) return;
    restoreReview = { receipt, hostName: target.name, production: target.environment === "production" };
  }

  async function restore() {
    const review = restoreReview;
    if (!review || mutationBusy) return;
    mutationBusy = true;
    try {
      rememberReceipt(await SFTPService.RestoreDeleted(review.receipt.hostID, review.receipt.id));
      if (restoreReview === review) restoreReview = null;
      if (host?.id === review.receipt.hostID) await reload();
    } finally {
      await loadReceipts(review.receipt.hostID);
      mutationBusy = false;
    }
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
      reviewRequest++;
      receiptRequest++;
      reviewBusy = false;
      deleteReview = null;
      restoreReview = null;
      receipts = [];
      clearSearch();
      searchQuery = "";
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
        oninput={() => clearSearch()}
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

    <section class="border-b hairline px-4 py-3" aria-label="Remote filename search">
      <form class="flex items-center gap-2" onsubmit={(event) => { event.preventDefault(); void searchFiles(); }}>
        <input class="min-w-0 flex-1 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-1.5 type-caption" aria-label="Filename substring" placeholder="Search filenames below this directory…" maxlength="256" bind:value={searchQuery} />
        <button class="rounded border hairline px-2 py-1.5 type-caption disabled:opacity-30" disabled={searchBusy || !searchQuery.trim()}>Search</button>
        {#if searchBusy}<button type="button" class="rounded border hairline px-2 py-1.5 type-caption" onclick={() => clearSearch(true)}>Cancel</button>{/if}
      </form>
      <p class="mt-1 type-micro text-[var(--color-text-3)]">Case-insensitive filename substring over SFTP; no contents or symlinks. Limits: 8 levels, 10,000 entries, 200 results, 512 KiB response, 15 seconds.</p>
      {#if searchBusy}<p class="mt-2 type-caption" role="status">Searching below {path || "(home)"}…</p>{/if}
      {#if searchNotice}<p class="mt-2 type-caption" role="status">{searchNotice}</p>{/if}
      {#if searchError}<p class="mt-2 break-words type-caption text-[var(--color-danger)]" role="alert">Search failed: {searchError}</p>{/if}
      {#if searchResult}
        <p class="mt-2 break-all type-caption" role="status">
          {searchResult.partial ? "Partial search — incomplete" : "Search complete (excluding symlinks and special files)"}{searchResult.limitReached ? "; limit reached" : ""}.
          {searchResult.entries.length} matches for “{searchedQuery}”; {searchResult.scanned} entries scanned below {searchResult.root || "the requested directory"}.
          {#if searchResult.reasons.length > 0}Reasons: {searchResult.reasons.join(", ")}.{/if}
        </p>
        {#if searchResult.entries.length === 0}
          <p class="mt-1 type-caption">{searchResult.partial ? "No matching filenames found before the search stopped." : "No matching filenames."}</p>
        {:else}
          <ul class="mt-2 max-h-64 space-y-2 overflow-y-auto" aria-label="Filename search results">
            {#each searchResult.entries as entry (entry.path)}
              <li class="rounded border hairline p-2 type-caption">
                <button type="button" class="break-all text-left font-mono text-[var(--color-accent)]" onclick={() => openSearchResult(entry)} aria-label={`${entry.isDir ? "Navigate to" : "Open"} ${entry.path}`}>{entry.isDir ? "Folder: " : "File: "}{entry.path}</button>
                <p class="mt-1 type-micro text-[var(--color-text-3)]">{entry.mode} · {entry.isDir ? "directory" : fmtSize(entry.size)} · modified {new Date(entry.modTime * 1000).toLocaleString()}</p>
                {#if !entry.isDir}<button type="button" class="mt-1 rounded border hairline px-2 py-1" onclick={() => openSearchResult(entry, true)}>Open containing folder</button>{/if}
              </li>
            {/each}
          </ul>
        {/if}
      {/if}
    </section>

    {#if reviewBusy}<p class="px-4 py-2 type-caption" role="status">Reading file contents and metadata for deletion review…</p>{/if}
    {#if receipts.length > 0}
      <section class="max-h-64 overflow-y-auto border-b hairline px-4 py-3" aria-label="Recoverable deletions">
        <h2 class="type-caption font-semibold">Recovery receipts</h2>
        <p class="mt-1 type-micro text-[var(--color-text-3)]">Receipts last until the backend restarts. Keep these paths for later manual recovery. Trash is not automatically purged.</p>
        {#each receipts as receipt (receipt.id)}
          <div class="mt-3 space-y-1 border-t hairline pt-2 type-caption">
            <p class="break-all font-mono">Original: {receipt.originalPath}</p>
            <p class="break-all font-mono">Trash: {receipt.trashPath}</p>
            <p>Status: {receipt.status}</p>
            {#if receipt.warning}<p class="break-words text-[var(--color-warn)]" role="alert">{receipt.warning}</p>{/if}
            <button class="rounded border hairline px-2 py-1 disabled:opacity-30" disabled={mutationBusy || receipt.status === 'restored'} onclick={() => previewRestore(receipt)}>Review restore</button>
          </div>
        {/each}
      </section>
    {/if}

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
                class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)] disabled:opacity-30"
                disabled={reviewBusy || mutationBusy || e.isDir || e.symlink || !e.mode.startsWith('-')}
                onclick={() => previewDelete(e)}
                title={e.isDir || e.symlink ? "Only regular files can be moved to trash" : "Review move to trash"}
                aria-label="Review move to trash for {e.name}"
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

{#if deleteReview}
  <ConfirmDanger
    title="REVIEW MOVE TO TRASH"
    body={`Host: ${deleteReview.hostName}. Original: ${deleteReview.preview.originalPath}. Trash: ${deleteReview.preview.trashPath}. File: ${fmtSize(deleteReview.preview.size)}, ${deleteReview.preview.mode}, modified ${new Date(deleteReview.preview.modTime * 1000).toLocaleString()}. Revision: ${deleteReview.preview.revision}. Review expires ${new Date(deleteReview.preview.expiresAt * 1000).toLocaleTimeString()}. Confirm to move this regular file to sibling trash over SFTP. Restore will refuse to overwrite an existing path. This is a two-step move; avoid files being changed by other processes.`}
    severity="block-without-confirm"
    allowEnterConfirm={false}
    productionHosts={deleteReview.production ? [deleteReview.hostName] : []}
    onCancel={() => (deleteReview = null)}
    onConfirm={remove}
  />
{/if}

{#if restoreReview}
  <ConfirmDanger
    title="REVIEW RESTORE"
    body={`Host: ${restoreReview.hostName}. Restore ${restoreReview.receipt.trashPath} to ${restoreReview.receipt.originalPath}? The backend will verify the saved revision and refuse any existing destination, including symlinks. It will only remove the trash name after verifying the restored file.`}
    severity="block-without-confirm"
    allowEnterConfirm={false}
    productionHosts={restoreReview.production ? [restoreReview.hostName] : []}
    onCancel={() => (restoreReview = null)}
    onConfirm={restore}
  />
{/if}
