<script lang="ts">
  import { onDestroy, onMount, tick } from "svelte";
  import { EditorView, basicSetup } from "codemirror";
  import { EditorState, type Extension } from "@codemirror/state";
  import { keymap } from "@codemirror/view";
  import { oneDark } from "@codemirror/theme-one-dark";
  import { sql } from "@codemirror/lang-sql";
  import { DBService } from "../../bindings/github.com/blacknode/blacknode";
  import type {
    DBConnectionInfo,
    QueryResult,
    SavedConnection,
    DBTable,
    DBColumn,
  } from "../../bindings/github.com/blacknode/blacknode/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import {
    Database,
    Play,
    Loader2,
    Plug,
    Unplug,
    AlertTriangle,
    Copy,
    Bookmark,
    BookmarkPlus,
    Trash2,
    Server,
    Table as TableIcon,
    Eye,
    Key,
    ChevronRight,
    ChevronDown,
    RefreshCw,
  } from "@lucide/svelte";

  let kind = $state<"postgres" | "mysql">("postgres");
  let dsn = $state("postgres://postgres:postgres@localhost:5432/postgres");
  let busy = $state(false);
  let connecting = $state(false);
  let err = $state("");

  // Auto-flip kind when the user pastes a known DSN shape so they don't
  // have to remember to change the dropdown.
  $effect(() => {
    const low = dsn.trim().toLowerCase();
    if (low.startsWith("postgres://") || low.startsWith("postgresql://")) {
      kind = "postgres";
    } else if (low.includes("@tcp(")) {
      kind = "mysql";
    }
  });

  // Sample DSN for the placeholder/value depending on kind. Cycles when the
  // user toggles the dropdown, but only if the current DSN matches the
  // *previous* kind's sample exactly (i.e. they haven't customized it).
  const SAMPLE_DSN: Record<"postgres" | "mysql", string> = {
    postgres: "postgres://postgres:postgres@localhost:5432/postgres",
    mysql: "user:password@tcp(localhost:3306)/dbname",
  };
  let prevKind = $state<"postgres" | "mysql">("postgres");
  $effect(() => {
    if (kind !== prevKind && dsn === SAMPLE_DSN[prevKind]) {
      dsn = SAMPLE_DSN[kind];
    }
    prevKind = kind;
  });

  let conn = $state<DBConnectionInfo | null>(null);
  let result = $state<QueryResult | null>(null);

  // Saved-connection state.
  let saved = $state<SavedConnection[]>([]);
  let saveDialogOpen = $state(false);
  let saveName = $state("");
  let savingBusy = $state(false);

  // Schema browser state. tablesByKey caches columns keyed by "schema.name"
  // so re-expanding a table is instant.
  let tables = $state<DBTable[]>([]);
  let loadingTables = $state(false);
  let expanded = $state<Set<string>>(new Set());
  let columnsByKey = $state<Record<string, DBColumn[]>>({});
  let loadingCols = $state<Set<string>>(new Set());
  let tableFilter = $state("");

  let editorEl: HTMLDivElement | undefined = $state();
  let view: EditorView | undefined;

  let host = $derived(
    app.selectedHostID ? app.hosts.find((h) => h.id === app.selectedHostID) : null,
  );

  onMount(() => {
    // If a connection survived a panel remount, pick it back up.
    void DBService.List().then((list) => {
      if (Array.isArray(list) && list.length > 0) {
        conn = list[0] as DBConnectionInfo;
        mountEditor("");
      }
    });
    void refreshSaved();
  });

  async function refreshSaved() {
    try {
      saved = ((await DBService.ListSavedConnections()) ?? []) as SavedConnection[];
    } catch {
      saved = [];
    }
  }

  async function refreshTables() {
    if (!conn) return;
    loadingTables = true;
    try {
      tables = ((await DBService.Tables(conn.connID)) ?? []) as DBTable[];
      // Reset column cache on refresh — schema may have changed.
      columnsByKey = {};
      expanded = new Set();
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      loadingTables = false;
    }
  }

  function tableKey(t: DBTable) {
    return t.schema + "." + t.name;
  }

  async function toggleTable(t: DBTable) {
    if (!conn) return;
    const key = tableKey(t);
    if (expanded.has(key)) {
      const next = new Set(expanded);
      next.delete(key);
      expanded = next;
      return;
    }
    const next = new Set(expanded);
    next.add(key);
    expanded = next;
    if (!columnsByKey[key]) {
      const lc = new Set(loadingCols);
      lc.add(key);
      loadingCols = lc;
      try {
        const cols = (await DBService.Columns(
          conn.connID,
          t.schema,
          t.name,
        )) as DBColumn[];
        columnsByKey[key] = cols ?? [];
      } catch {
        columnsByKey[key] = [];
      } finally {
        const lc = new Set(loadingCols);
        lc.delete(key);
        loadingCols = lc;
      }
    }
  }

  // Build a schema-qualified, dialect-correctly-quoted reference, e.g.
  // "public"."users" on Postgres or `dbname`.`users` on MySQL.
  function qualifyTable(t: DBTable): string {
    if (conn?.kind === "mysql") {
      return "`" + t.schema + "`.`" + t.name + "`";
    }
    return `"${t.schema}"."${t.name}"`;
  }

  function selectFromTable(t: DBTable) {
    if (!view) return;
    const sqlText = `SELECT * FROM ${qualifyTable(t)} LIMIT 100;`;
    view.dispatch({
      changes: {
        from: 0,
        to: view.state.doc.length,
        insert: sqlText,
      },
    });
    view.focus();
  }

  // Filtered tables for the rail's search box.
  let filteredTables = $derived.by(() => {
    if (!tableFilter.trim()) return tables;
    const q = tableFilter.toLowerCase();
    return tables.filter(
      (t) =>
        t.name.toLowerCase().includes(q) ||
        t.schema.toLowerCase().includes(q),
    );
  });

  // Tables grouped by schema for the rail's headers.
  let groupedTables = $derived.by(() => {
    const groups: Record<string, DBTable[]> = {};
    for (const t of filteredTables) {
      (groups[t.schema] ??= []).push(t);
    }
    return groups;
  });

  function fmtRows(n: number): string {
    if (n < 1000) return `${n}`;
    if (n < 1000000) return `${(n / 1000).toFixed(1)}K`;
    if (n < 1000000000) return `${(n / 1000000).toFixed(1)}M`;
    return `${(n / 1000000000).toFixed(1)}B`;
  }

  onDestroy(() => view?.destroy());

  function mountEditor(initial: string) {
    if (!editorEl) return;
    const exts: Extension[] = [
      basicSetup,
      sql(),
      keymap.of([
        {
          key: "Mod-Enter",
          run: () => {
            void runQuery();
            return true;
          },
        },
      ]),
      EditorView.theme({
        "&": { height: "100%", fontSize: "13px" },
        ".cm-scroller": {
          fontFamily:
            '"JetBrains Mono Variable", "Cascadia Mono", Menlo, Consolas, monospace',
        },
      }),
    ];
    if (app.settings.theme !== "light") exts.push(oneDark);
    view = new EditorView({
      state: EditorState.create({ doc: initial, extensions: exts }),
      parent: editorEl,
    });
  }

  async function connect() {
    if (!host) {
      err = "select a host first";
      return;
    }
    if (!dsn.trim()) {
      err = "dsn required";
      return;
    }
    connecting = true;
    err = "";
    try {
      const password = app.hostPasswords[host.id] ?? "";
      conn = (await DBService.Connect(
        host.id,
        password,
        kind,
        dsn,
      )) as DBConnectionInfo;
      await tick();
      mountEditor(
        kind === "postgres"
          ? "SELECT now() AS server_time, current_database() AS db;"
          : "SELECT NOW() AS server_time, DATABASE() AS db;",
      );
      void refreshTables();
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      connecting = false;
    }
  }

  async function disconnect() {
    if (!conn) return;
    try {
      await DBService.Disconnect(conn.connID);
    } finally {
      view?.destroy();
      view = undefined;
      conn = null;
      result = null;
      tables = [];
      columnsByKey = {};
      expanded = new Set();
    }
  }

  async function saveCurrent() {
    if (!host || !dsn.trim() || !saveName.trim()) return;
    savingBusy = true;
    try {
      await DBService.SaveConnection(saveName.trim(), kind, host.id, dsn);
      saveName = "";
      saveDialogOpen = false;
      await refreshSaved();
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      savingBusy = false;
    }
  }

  async function connectSaved(s: SavedConnection) {
    connecting = true;
    err = "";
    try {
      const password = app.hostPasswords[s.hostID] ?? "";
      // Set the active host so the rest of the panel reflects the saved
      // connection's tunnel.
      app.selectedHostID = s.hostID;
      conn = (await DBService.ConnectSaved(s.id, password)) as DBConnectionInfo;
      await tick();
      mountEditor(
        conn.kind === "mysql"
          ? "SELECT NOW() AS server_time, DATABASE() AS db;"
          : "SELECT now() AS server_time, current_database() AS db;",
      );
      void refreshTables();
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      connecting = false;
    }
  }

  async function deleteSaved(s: SavedConnection) {
    if (!confirm(`Delete saved connection "${s.name}"?`)) return;
    await DBService.DeleteSavedConnection(s.id);
    await refreshSaved();
  }

  async function runQuery() {
    if (!conn || !view || busy) return;
    const sqlText = view.state.doc.toString().trim();
    if (!sqlText) return;
    busy = true;
    err = "";
    result = null;
    try {
      result = (await DBService.Query(conn.connID, sqlText)) as QueryResult;
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = false;
    }
  }

  function copy(text: string) {
    navigator.clipboard.writeText(text);
  }

  // Render long cell values with a copy button so users can grab the full
  // string without an expand-on-hover dance. Cells are kept short visually.
  function shorten(s: string, n = 80) {
    return s.length <= n ? s : s.slice(0, n) + "…";
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Database}
    title="Database"
    subtitle={conn
      ? `${conn.user}@${conn.server}/${conn.database} via ${conn.hostName}`
      : host
        ? `Connect to a Postgres on ${host.name} or anywhere it can reach`
        : "Pick a host from the sidebar"}
  >
    {#snippet actions()}
      {#if conn}
        <button
          class="flex items-center gap-1 rounded-md border hairline-strong px-2.5 py-1 text-[11px] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
          onclick={disconnect}
        >
          <Unplug size="11" /> disconnect
        </button>
      {/if}
    {/snippet}
  </PageHeader>

  {#if !host}
    <div class="flex flex-1 items-center justify-center">
      <div class="text-center">
        <Database size="22" class="mx-auto text-[var(--color-text-4)]" />
        <p class="mt-2 text-xs text-[var(--color-text-3)]">
          Pick a host. Connections are tunneled through SSH — point the DSN
          at a Postgres reachable from that host.
        </p>
      </div>
    </div>
  {:else if !conn}
    <!-- Connect form -->
    <div class="m-auto w-full max-w-2xl space-y-4 p-6">
      {#if saved.length > 0}
        <div class="rounded-xl border hairline surface-2 p-5">
          <div class="mb-3 flex items-center gap-2">
            <Bookmark size="14" class="text-[var(--color-accent)]" />
            <h3 class="text-sm font-semibold">Saved connections</h3>
            <span class="text-[10px] text-[var(--color-text-4)]">
              {saved.length}
            </span>
          </div>
          <div class="space-y-1">
            {#each saved as s (s.id)}
              <div class="group flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-[var(--color-surface-3)]">
                <Database size="11" class="text-[var(--color-accent)]" />
                <button
                  class="min-w-0 flex-1 text-left text-sm"
                  onclick={() => connectSaved(s)}
                  disabled={connecting}
                >
                  <div class="truncate font-medium">{s.name}</div>
                  <div class="flex items-center gap-1.5 truncate text-[10px] text-[var(--color-text-3)]">
                    <Server size="9" /> {s.hostName || s.hostID.slice(0, 6)} · {s.kind}
                  </div>
                </button>
                <button
                  class="rounded p-1 text-[var(--color-text-3)] opacity-0 hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)] group-hover:opacity-100"
                  onclick={() => deleteSaved(s)}
                  title="Delete"
                >
                  <Trash2 size="11" />
                </button>
              </div>
            {/each}
          </div>
        </div>
      {/if}

      <div class="rounded-xl border hairline surface-2 p-5 text-sm">
        <div class="mb-3 flex items-center gap-2">
          <Plug size="14" class="text-[var(--color-accent)]" />
          <h3 class="text-sm font-semibold">New connection</h3>
        </div>
        <p class="text-xs text-[var(--color-text-3)]">
          Connection runs through {host.name}'s SSH tunnel. The DSN host can
          be <span class="font-mono">localhost</span> (database on the host
          itself) or any address that host can resolve.
        </p>
        <div class="mt-4 grid grid-cols-[140px_1fr] gap-2">
          <label class="block">
            <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Kind</span
            >
            <select
              class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 text-sm outline-none"
              bind:value={kind}
            >
              <option value="postgres">Postgres</option>
              <option value="mysql">MySQL</option>
            </select>
          </label>
          <label class="block">
            <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >DSN</span
            >
            <input
              class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-xs outline-none"
              placeholder={SAMPLE_DSN[kind]}
              bind:value={dsn}
              onkeydown={(e) => e.key === "Enter" && connect()}
            />
          </label>
        </div>
        {#if err}
          <p class="mt-2 text-xs text-[var(--color-danger)]">{err}</p>
        {/if}
        <div class="mt-3 flex items-center gap-2">
          <button
            class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-3 py-2 text-sm font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
            disabled={connecting || !dsn}
            onclick={connect}
          >
            {#if connecting}
              <Loader2 size="14" class="animate-spin" />
            {:else}
              <Plug size="14" />
            {/if}
            Connect
          </button>
          <button
            class="flex items-center gap-1.5 rounded-md border hairline-strong px-3 py-2 text-sm hover:bg-[var(--color-surface-3)] disabled:opacity-50"
            disabled={!dsn || !host}
            onclick={() => (saveDialogOpen = true)}
            title="Save this DSN as a one-click connection (vault-encrypted)"
          >
            <BookmarkPlus size="13" />
            Save…
          </button>
        </div>
        <p class="mt-3 text-[10px] text-[var(--color-text-4)]">
          Saved DSNs are encrypted with the vault before being written to
          disk. Plaintext never persists. Vault must be unlocked.
        </p>
      </div>
    </div>
  {:else}
    <!-- Schema rail + Query editor + results -->
    <div class="grid h-full grid-cols-[260px_1fr] divide-x divide-[var(--color-line)] overflow-hidden">
      <!-- Schema rail -->
      <div class="flex flex-col overflow-hidden surface-1">
        <div class="flex items-center gap-2 border-b hairline px-3 py-1.5 text-[11px]">
          <TableIcon size="11" class="text-[var(--color-accent)]" />
          <span class="text-[var(--color-text-2)]">Schema</span>
          <span class="font-mono text-[10px] text-[var(--color-text-4)]"
            >{tables.length}</span
          >
          <button
            class="ml-auto rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)] disabled:opacity-50"
            disabled={loadingTables}
            onclick={refreshTables}
            title="Refresh"
          >
            {#if loadingTables}<Loader2 size="10" class="animate-spin" />{:else}<RefreshCw
                size="10"
              />{/if}
          </button>
        </div>
        <div class="border-b hairline px-2 py-1.5">
          <input
            class="w-full rounded-md border hairline bg-[var(--color-surface-3)] px-2 py-1 text-[11px] outline-none placeholder:text-[var(--color-text-4)]"
            placeholder="filter…"
            bind:value={tableFilter}
          />
        </div>
        <div class="flex-1 overflow-y-auto py-1">
          {#each Object.keys(groupedTables).sort() as schema (schema)}
            {#if conn.kind !== "mysql" || tables.length > 0}
              <div
                class="px-3 pt-2 pb-1 text-[9px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-4)]"
              >
                {schema}
              </div>
            {/if}
            {#each groupedTables[schema] as t (tableKey(t))}
              {@const key = tableKey(t)}
              {@const isOpen = expanded.has(key)}
              <div>
                <div
                  class="group flex w-full cursor-pointer items-center gap-1.5 px-2 py-1 text-[11px] hover:bg-[var(--color-surface-3)]"
                  role="button"
                  tabindex="0"
                  onclick={() => toggleTable(t)}
                  onkeydown={(e) => e.key === "Enter" && toggleTable(t)}
                >
                  {#if isOpen}
                    <ChevronDown size="10" class="text-[var(--color-text-4)]" />
                  {:else}
                    <ChevronRight size="10" class="text-[var(--color-text-4)]" />
                  {/if}
                  {#if t.kind === "view"}
                    <Eye size="10" class="text-[var(--color-info)]" />
                  {:else}
                    <TableIcon size="10" class="text-[var(--color-text-3)]" />
                  {/if}
                  <span class="min-w-0 flex-1 truncate">{t.name}</span>
                  {#if t.rowEstimate > 0}
                    <span class="font-mono text-[9px] text-[var(--color-text-4)]"
                      >{fmtRows(t.rowEstimate)}</span
                    >
                  {/if}
                  <button
                    class="rounded px-1 text-[9px] text-[var(--color-text-3)] opacity-0 hover:bg-[var(--color-surface-4)] hover:text-[var(--color-accent)] group-hover:opacity-100"
                    onclick={(e) => {
                      e.stopPropagation();
                      selectFromTable(t);
                    }}
                    title="Insert SELECT * FROM ... LIMIT 100"
                  >
                    100
                  </button>
                </div>
                {#if isOpen}
                  {#if loadingCols.has(key)}
                    <div
                      class="ml-7 px-2 py-1 text-[10px] text-[var(--color-text-4)]"
                    >
                      <Loader2 size="9" class="inline animate-spin" /> loading…
                    </div>
                  {:else if columnsByKey[key]}
                    {#each columnsByKey[key] as col (col.name)}
                      <div
                        class="grid grid-cols-[1fr_auto] items-center gap-1 px-3 py-0.5 pl-7 text-[10px]"
                      >
                        <span class="flex items-center gap-1 truncate">
                          {#if col.isPrimary}
                            <Key size="8" class="text-[var(--color-warn)]" />
                          {/if}
                          <span
                            class={col.isPrimary
                              ? "font-medium text-[var(--color-text-1)]"
                              : "text-[var(--color-text-2)]"}>{col.name}</span
                          >
                          {#if !col.nullable}
                            <span class="text-[var(--color-text-4)]">!</span>
                          {/if}
                        </span>
                        <span
                          class="font-mono text-[9px] text-[var(--color-text-4)]"
                          >{col.dataType}</span
                        >
                      </div>
                    {/each}
                  {/if}
                {/if}
              </div>
            {/each}
          {/each}
          {#if tables.length === 0 && !loadingTables}
            <div class="px-3 py-4 text-center text-[10px] text-[var(--color-text-4)]">
              no tables found
            </div>
          {/if}
        </div>
      </div>

      <!-- Editor + results -->
      <div class="grid grid-rows-[200px_1fr] divide-y divide-[var(--color-line)] overflow-hidden">
        <div class="flex flex-col">
        <div class="flex items-center gap-2 border-b hairline surface-1 px-3 py-1.5 text-[11px]">
          <Database size="11" class="text-[var(--color-accent)]" />
          <span class="font-mono text-[var(--color-text-2)]">{conn.database}</span>
          <span class="text-[var(--color-text-4)]">@</span>
          <span class="font-mono text-[var(--color-text-3)]">{conn.server}</span>
          <span class="ml-auto flex items-center gap-2">
            <kbd
              class="rounded border hairline px-1 py-0.5 font-mono text-[9px] text-[var(--color-text-4)]"
              >⌘↵</kbd
            >
            <button
              class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-3 py-1 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
              disabled={busy}
              onclick={runQuery}
            >
              {#if busy}<Loader2 size="11" class="animate-spin" />{:else}<Play
                  size="11"
                />{/if}
              Run
            </button>
          </span>
        </div>
        <div bind:this={editorEl} class="flex-1 overflow-hidden"></div>
      </div>

      <div class="overflow-auto">
        {#if err}
          <div
            class="m-3 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-3 font-mono text-[11px] whitespace-pre-wrap text-[var(--color-danger)]"
          >
            <AlertTriangle size="12" class="mr-1 inline" /> {err}
          </div>
        {/if}

        {#if result}
          <div class="flex flex-wrap items-center gap-3 border-b hairline surface-1 px-4 py-2 text-[11px]">
            <span class="font-mono text-[var(--color-text-2)]">{result.commandTag || "OK"}</span>
            <span class="text-[var(--color-text-3)]">
              {result.rowCount} row{result.rowCount === 1 ? "" : "s"} · {result.durationMs}ms
            </span>
            {#if result.truncated}
              <span
                class="inline-flex items-center gap-1 rounded border border-[var(--color-warn)]/40 bg-[var(--color-warn)]/10 px-1.5 py-0.5 text-[10px] text-[var(--color-warn)]"
              >
                <AlertTriangle size="10" /> truncated at 1000 rows
              </span>
            {/if}
          </div>

          {#if result.columns.length > 0}
            <table class="w-full text-xs">
              <thead
                class="sticky top-0 z-10 surface-1 text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >
                <tr>
                  <th class="px-3 py-2 text-right font-medium">#</th>
                  {#each result.columns as c (c.name)}
                    <th class="px-3 py-2 text-left font-medium">
                      {c.name}
                      <span class="ml-1 font-mono normal-case text-[9px] text-[var(--color-text-4)]"
                        >{c.type}</span
                      >
                    </th>
                  {/each}
                </tr>
              </thead>
              <tbody>
                {#each result.rows as row, i (i)}
                  <tr class="border-b hairline hover:bg-[var(--color-surface-2)]">
                    <td class="px-3 py-1 text-right font-mono text-[10px] text-[var(--color-text-4)]"
                      >{i + 1}</td
                    >
                    {#each row as cell, j (j)}
                      <td
                        class="max-w-[300px] px-3 py-1 font-mono text-[11px] {cell ===
                        'NULL'
                          ? 'text-[var(--color-text-4)] italic'
                          : 'text-[var(--color-text-1)]'}"
                        title={cell}
                      >
                        <span class="flex items-center gap-1">
                          <span class="truncate">{shorten(cell, 80)}</span>
                          {#if cell.length > 80}
                            <button
                              class="rounded p-0.5 text-[var(--color-text-3)] opacity-0 hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)] group-hover:opacity-100"
                              onclick={() => copy(cell)}
                              title="Copy full value"
                            >
                              <Copy size="9" />
                            </button>
                          {/if}
                        </span>
                      </td>
                    {/each}
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
        {:else if !err && !busy}
          <div class="flex h-full items-center justify-center text-xs text-[var(--color-text-3)]">
            Type a query and hit Run (⌘↵).
          </div>
        {/if}
      </div>
      </div>
    </div>
  {/if}
</div>

{#if saveDialogOpen}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) saveDialogOpen = false;
    }}
  >
    <div
      class="w-[440px] overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/50"
    >
      <div class="flex items-center gap-2 border-b hairline px-5 py-3">
        <BookmarkPlus size="14" class="text-[var(--color-accent)]" />
        <h3 class="text-sm font-semibold">Save connection</h3>
      </div>
      <div class="space-y-3 p-5 text-sm">
        <p class="text-xs text-[var(--color-text-3)]">
          Stores the DSN encrypted with the vault. Recall it from the saved
          list above.
        </p>
        <label class="block">
          <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
            >Name</span
          >
          <input
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            bind:value={saveName}
            placeholder="e.g. prod-orders-db"
            onkeydown={(e) => e.key === "Enter" && saveCurrent()}
          />
        </label>
      </div>
      <div class="flex items-center justify-end gap-2 border-t hairline px-5 py-3">
        <button
          class="rounded-md px-3 py-1.5 text-xs text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={() => (saveDialogOpen = false)}>Cancel</button
        >
        <button
          class="rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
          disabled={savingBusy || !saveName.trim()}
          onclick={saveCurrent}>{savingBusy ? "saving…" : "Save"}</button
        >
      </div>
    </div>
  </div>
{/if}

