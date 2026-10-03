<script lang="ts">
  import Dialog from "./Dialog.svelte";
  import { app } from "./state.svelte";
  import { PortForwardService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { ActiveForward } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { readSavedWorkspaces, WORKSPACES_KEY, parseWorkspace, type SavedWorkspace, type WorkspaceSnapshot } from "./workspaces";
  import { LayoutGrid, X } from "@lucide/svelte";

  let { capture, onopen, forwardIDs, onforwards, disabled = false }: {
    capture: () => WorkspaceSnapshot;
    onopen: (snapshot: WorkspaceSnapshot) => void;
    forwardIDs: string[];
    onforwards: (ids: string[]) => void;
    disabled?: boolean;
  } = $props();
  let open = $state(false);
  let saved = $state<SavedWorkspace[]>(readSavedWorkspaces(localStorage));
  let name = $state("");
  let selected = $state("");
  let forwards = $state<ActiveForward[]>([]);
  let chosen = $state<string[]>([]);
  let error = $state("");
  let loading = $state(false);

  async function show() {
    open = true;
    error = "";
    chosen = [...forwardIDs];
    saved = readSavedWorkspaces(localStorage);
    loading = true;
    try {
      forwards = await PortForwardService.List() ?? [];
      chosen = [...new Set([...chosen, ...forwards.filter((f) => f.active).map((f) => f.id)])];
    } catch (e) { error = `Could not load tunnel presets: ${e}`; }
    finally { loading = false; }
  }

  function persist(rows: SavedWorkspace[]) {
    try {
      localStorage.setItem(WORKSPACES_KEY, JSON.stringify(rows));
      saved = rows;
      error = "";
      return true;
    } catch (e) { error = `Could not save workspaces: ${e}`; return false; }
  }

  function save(update = false) {
    const existing = update ? saved.find((w) => w.id === selected) : undefined;
    const title = existing?.name ?? name.trim();
    if (!title) return;
    if (!existing && saved.some((w) => w.name.toLowerCase() === title.toLowerCase())) {
      error = "That name already exists. Select it and use Update selected."; return;
    }
    if (!existing && saved.length >= 50) { error = "You can save up to 50 workspaces."; return; }
    const snapshot = parseWorkspace({ ...capture(), forwardIDs: chosen });
    if (!snapshot) { error = "This workspace is too large to save (maximum 32 tabs and 256 layout nodes)."; return; }
    const row = { id: existing?.id ?? crypto.randomUUID(), name: title, snapshot };
    const next = existing ? saved.map((w) => w.id === row.id ? row : w) : [...saved, row];
    if (persist(next)) {
      selected = row.id;
      name = "";
      onforwards([...chosen]);
      app.toast("ok", "Workspace saved", row.name);
    }
  }

  function openSelected() {
    const workspace = saved.find((w) => w.id === selected);
    if (!workspace) return;
    onopen(workspace.snapshot);
    open = false;
  }
</script>

<button class="flex shrink-0 items-center gap-1.5 rounded border hairline px-2 py-1 type-caption hover:bg-[var(--color-surface-3)] disabled:opacity-40" {disabled} onclick={show}>
  <LayoutGrid size="13" /> Workspaces
</button>

{#if open}
  <Dialog label="Saved workspaces" onclose={() => open = false} panelClass="w-[min(95vw,560px)] max-h-[85vh] overflow-y-auto rounded-xl border hairline-strong surface-2 p-5 shadow-xl">
    <div class="flex items-center justify-between">
      <h2 class="type-title">Workspaces</h2>
      <button aria-label="Close workspaces" onclick={() => open = false}><X size="16" /></button>
    </div>
    <p class="mt-2 type-caption text-[var(--color-text-3)]">Save tabs, split layouts, host assignments, and tunnel presets. Opening a workspace replaces the current tabs and reconnects its hosts. Running sessions in the current tabs will close.</p>
    {#if error}<p role="alert" class="mt-3 type-caption text-[var(--color-danger)]">{error}</p>{/if}
    <label class="mt-4 block type-caption">Saved workspace
      <select class="mt-1 w-full rounded border hairline surface-3 p-2" bind:value={selected}>
        <option value="">Select a workspace</option>
        {#each saved as workspace (workspace.id)}<option value={workspace.id}>{workspace.name}</option>{/each}
      </select>
    </label>
    <div class="mt-3 flex gap-2 type-caption">
      <button class="rounded bg-[var(--color-accent)] px-3 py-2 text-[var(--color-surface-0)] disabled:opacity-40" disabled={!selected || loading} onclick={openSelected}>Open workspace</button>
      <button class="rounded border hairline px-3 py-2 disabled:opacity-40" disabled={!selected || loading} onclick={() => save(true)}>Update selected</button>
      <button class="ml-auto rounded border hairline px-3 py-2 disabled:opacity-40" disabled={!selected} onclick={() => { if (persist(saved.filter((w) => w.id !== selected))) selected = ""; }}>Delete</button>
    </div>
    <fieldset class="mt-5 border-t hairline pt-3">
      <legend class="type-caption">Tunnel presets to include when saving</legend>
      {#if loading}<p class="type-caption text-[var(--color-text-3)]">Loading tunnels…</p>
      {:else if !forwards.length}<p class="type-caption text-[var(--color-text-3)]">Create a preset in Network → Forwards to include it here.</p>{/if}
      {#each forwards as forward (forward.id)}
        <label class="mt-2 flex items-center gap-2 type-caption">
          <input type="checkbox" checked={chosen.includes(forward.id)} onchange={(e) => chosen = e.currentTarget.checked ? [...chosen, forward.id] : chosen.filter((id) => id !== forward.id)} />
          {forward.name || forward.id}{forward.active ? " (running)" : ""}
        </label>
      {/each}
    </fieldset>
    <form class="mt-5 flex gap-2" onsubmit={(e) => { e.preventDefault(); save(); }}>
      <input aria-label="New workspace name" placeholder="Workspace name" maxlength="80" class="min-w-0 flex-1 rounded border hairline surface-3 px-3 py-2 type-caption" bind:value={name} />
      <button class="rounded border hairline px-3 py-2 type-caption disabled:opacity-40" disabled={!name.trim() || loading}>Save as new</button>
    </form>
  </Dialog>
{/if}
