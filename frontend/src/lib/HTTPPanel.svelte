<script lang="ts">
  import { HTTPService } from "../../bindings/github.com/blacknode/blacknode";
  import type { HTTPResponse } from "../../bindings/github.com/blacknode/blacknode/models";
  import type { HTTPRequest as SavedRequest } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import { parseCurl, toCurl, substituteVars } from "./httpCurl";
  import {
    Globe2,
    Send,
    Loader2,
    Copy,
    AlertTriangle,
    Save,
    Trash2,
    FolderOpen,
    Folder,
    ChevronRight,
    ChevronDown,
    Plus,
    Terminal,
    Variable,
  } from "@lucide/svelte";

  // Environment-variable storage. `httpEnvs` is { [envName]: { [k]: v } };
  // active env is the one currently selected. Frontend-local on purpose —
  // saved requests stay portable; envs are a UI-time substitution layer.
  type EnvMap = Record<string, Record<string, string>>;
  const ENVS_KEY = "blacknode.http.envs.v1";
  const ACTIVE_ENV_KEY = "blacknode.http.activeEnv.v1";

  function loadEnvs(): EnvMap {
    try {
      const raw = localStorage.getItem(ENVS_KEY);
      if (!raw) return {};
      const parsed = JSON.parse(raw);
      return typeof parsed === "object" && parsed !== null ? parsed : {};
    } catch {
      return {};
    }
  }
  function persistEnvs(e: EnvMap) {
    localStorage.setItem(ENVS_KEY, JSON.stringify(e));
  }

  // Common methods first; less-common ones still typeable via the input.
  const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];

  let method = $state<string>("GET");
  let url = $state("");
  let headersText = $state("Content-Type: application/json");
  let body = $state("");
  let insecureSkipVerify = $state(false);

  let busy = $state(false);
  let err = $state("");
  let response = $state<HTTPResponse | null>(null);

  // Saved-request state. `loadedID` is the id of whichever saved request is
  // currently loaded into the form — non-null means "Save" updates the
  // existing record; null means "Save" creates a new one (and we collect
  // a name first via the inline save row).
  let saved = $state<SavedRequest[]>([]);
  let loadedID = $state<string | null>(null);
  let saveName = $state("");
  let saveFolder = $state("");
  let savePromptOpen = $state(false);
  let collapsedFolders = $state<Record<string, boolean>>({});

  let envs = $state<EnvMap>(loadEnvs());
  let activeEnv = $state<string>(localStorage.getItem(ACTIVE_ENV_KEY) ?? "");
  let envEditorOpen = $state(false);
  let envVarsText = $state("");
  let curlImportOpen = $state(false);
  let curlImportText = $state("");
  let curlImportErr = $state("");

  // Keep the env editor textarea in sync when the user switches envs.
  $effect(() => {
    const vars = envs[activeEnv] ?? {};
    envVarsText = Object.entries(vars)
      .map(([k, v]) => `${k}=${v}`)
      .join("\n");
  });

  let host = $derived(
    app.selectedHostID ? app.hosts.find((h) => h.id === app.selectedHostID) : null,
  );

  // Group saved requests by folder. Empty folder name renders as "ungrouped"
  // at the top so quick one-offs don't get buried.
  let grouped = $derived.by(() => {
    const groups = new Map<string, SavedRequest[]>();
    for (const r of saved) {
      const key = r.folder ?? "";
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key)!.push(r);
    }
    return [...groups.entries()].sort(([a], [b]) => {
      if (a === "" && b !== "") return -1;
      if (b === "" && a !== "") return 1;
      return a.localeCompare(b);
    });
  });

  // Pretty-printed JSON when the body parses, raw otherwise. Cached so we
  // don't re-parse on every keystroke during scroll.
  let prettyBody = $derived.by(() => {
    if (!response) return "";
    if (response.bodyBase64) return "[binary response — base64 omitted]";
    try {
      const ct = (
        response.headers.find((h) => h.name.toLowerCase() === "content-type")
          ?.value ?? ""
      ).toLowerCase();
      if (ct.includes("json")) {
        return JSON.stringify(JSON.parse(response.body), null, 2);
      }
    } catch {
      // not valid JSON; fall through
    }
    return response.body;
  });

  function parseHeaders(text: string): Record<string, string> {
    const out: Record<string, string> = {};
    for (const raw of text.split("\n")) {
      const line = raw.trim();
      if (!line || line.startsWith("#")) continue;
      const i = line.indexOf(":");
      if (i < 0) continue;
      const k = line.slice(0, i).trim();
      const v = line.slice(i + 1).trim();
      if (k) out[k] = v;
    }
    return out;
  }

  function headersToText(
    h: { [key: string]: string | undefined } | null | undefined,
  ): string {
    if (!h) return "";
    return Object.entries(h)
      .filter(([, v]) => v !== undefined)
      .map(([k, v]) => `${k}: ${v}`)
      .join("\n");
  }

  async function refreshSaved() {
    try {
      saved = (await HTTPService.ListSavedRequests()) ?? [];
    } catch (e: any) {
      err = String(e?.message ?? e);
    }
  }

  $effect(() => {
    refreshSaved();
  });

  async function send() {
    if (!host) {
      err = "select a host";
      return;
    }
    if (!url.trim()) {
      err = "url required";
      return;
    }
    busy = true;
    err = "";
    response = null;
    try {
      const password = app.hostPasswords[host.id] ?? "";
      const vars = (activeEnv && envs[activeEnv]) || {};
      const headers = parseHeaders(headersText);
      const subbedHeaders: Record<string, string> = {};
      for (const [k, v] of Object.entries(headers)) {
        subbedHeaders[substituteVars(k, vars)] = substituteVars(v, vars);
      }
      response = (await HTTPService.Request(host.id, password, {
        method,
        url: substituteVars(url, vars),
        headers: subbedHeaders,
        body: substituteVars(body, vars),
        insecureSkipVerify,
      } as any)) as HTTPResponse;
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = false;
    }
  }

  function applyCurlImport() {
    curlImportErr = "";
    try {
      const parsed = parseCurl(curlImportText);
      method = parsed.method;
      url = parsed.url;
      headersText = Object.entries(parsed.headers)
        .map(([k, v]) => `${k}: ${v}`)
        .join("\n");
      body = parsed.body;
      insecureSkipVerify = parsed.insecure;
      loadedID = null;
      saveName = "";
      saveFolder = "";
      curlImportOpen = false;
      curlImportText = "";
    } catch (e: any) {
      curlImportErr = String(e?.message ?? e);
    }
  }

  function copyAsCurl() {
    const txt = toCurl({
      method,
      url,
      headers: parseHeaders(headersText),
      body,
      insecure: insecureSkipVerify,
    });
    navigator.clipboard.writeText(txt);
  }

  // Parse the env-vars textarea (KEY=VALUE per line) and persist.
  function saveEnvVars() {
    const vars: Record<string, string> = {};
    for (const raw of envVarsText.split("\n")) {
      const line = raw.trim();
      if (!line || line.startsWith("#")) continue;
      const i = line.indexOf("=");
      if (i < 0) continue;
      const k = line.slice(0, i).trim();
      const v = line.slice(i + 1);
      if (k) vars[k] = v;
    }
    envs = { ...envs, [activeEnv]: vars };
    persistEnvs(envs);
  }

  function createEnv() {
    const name = prompt("Environment name (e.g. 'staging')");
    if (!name) return;
    const trimmed = name.trim();
    if (!trimmed) return;
    if (envs[trimmed]) {
      activeEnv = trimmed;
      localStorage.setItem(ACTIVE_ENV_KEY, trimmed);
      return;
    }
    envs = { ...envs, [trimmed]: {} };
    persistEnvs(envs);
    activeEnv = trimmed;
    localStorage.setItem(ACTIVE_ENV_KEY, trimmed);
  }

  function deleteEnv() {
    if (!activeEnv) return;
    if (!confirm(`Delete environment "${activeEnv}"?`)) return;
    const next = { ...envs };
    delete next[activeEnv];
    envs = next;
    persistEnvs(envs);
    activeEnv = "";
    localStorage.setItem(ACTIVE_ENV_KEY, "");
  }

  function selectEnv(name: string) {
    activeEnv = name;
    localStorage.setItem(ACTIVE_ENV_KEY, name);
  }

  function loadSaved(r: SavedRequest) {
    loadedID = r.id;
    method = r.method || "GET";
    url = r.url;
    headersText = headersToText(r.headers);
    body = r.body ?? "";
    insecureSkipVerify = !!r.insecure;
    response = null;
    err = "";
    saveName = r.name;
    saveFolder = r.folder ?? "";
  }

  function newRequest() {
    loadedID = null;
    method = "GET";
    url = "";
    headersText = "Content-Type: application/json";
    body = "";
    insecureSkipVerify = false;
    response = null;
    err = "";
    saveName = "";
    saveFolder = "";
  }

  async function saveRequest() {
    if (loadedID) {
      // Updating an existing record — no name prompt needed.
      try {
        const updated = (await HTTPService.SaveRequest({
          id: loadedID,
          name: saveName || url,
          folder: saveFolder ?? "",
          method,
          url,
          headers: parseHeaders(headersText),
          body,
          hostId: host?.id ?? "",
          insecure: insecureSkipVerify,
          createdAt: 0,
          updatedAt: 0,
        } as SavedRequest)) as SavedRequest;
        loadedID = updated.id;
        await refreshSaved();
      } catch (e: any) {
        err = String(e?.message ?? e);
      }
      return;
    }
    // No id loaded — open the inline name prompt.
    if (!url.trim()) {
      err = "url required to save";
      return;
    }
    saveName = saveName || url;
    savePromptOpen = true;
  }

  async function confirmSave() {
    if (!saveName.trim()) {
      return;
    }
    try {
      const created = (await HTTPService.SaveRequest({
        id: "",
        name: saveName.trim(),
        folder: saveFolder.trim(),
        method,
        url,
        headers: parseHeaders(headersText),
        body,
        hostId: host?.id ?? "",
        insecure: insecureSkipVerify,
        createdAt: 0,
        updatedAt: 0,
      } as SavedRequest)) as SavedRequest;
      loadedID = created.id;
      savePromptOpen = false;
      await refreshSaved();
    } catch (e: any) {
      err = String(e?.message ?? e);
    }
  }

  async function deleteSaved(r: SavedRequest, e: Event) {
    e.stopPropagation();
    if (!confirm(`Delete saved request "${r.name}"?`)) return;
    try {
      await HTTPService.DeleteSavedRequest(r.id);
      if (loadedID === r.id) {
        loadedID = null;
      }
      await refreshSaved();
    } catch (er: any) {
      err = String(er?.message ?? er);
    }
  }

  function statusColor(code: number) {
    if (code >= 200 && code < 300) return "text-[var(--color-accent)]";
    if (code >= 300 && code < 400) return "text-[var(--color-info)]";
    if (code >= 400 && code < 500) return "text-[var(--color-warn)]";
    if (code >= 500) return "text-[var(--color-danger)]";
    return "text-[var(--color-text-3)]";
  }

  function methodColor(m: string) {
    switch (m) {
      case "GET":
        return "text-[var(--color-info)]";
      case "POST":
        return "text-[var(--color-accent)]";
      case "PUT":
      case "PATCH":
        return "text-[var(--color-warn)]";
      case "DELETE":
        return "text-[var(--color-danger)]";
      default:
        return "text-[var(--color-text-3)]";
    }
  }

  function copy(text: string) {
    navigator.clipboard.writeText(text);
  }

  function fmtSize(n: number) {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    return `${(n / 1024 / 1024).toFixed(2)} MB`;
  }

  function toggleFolder(name: string) {
    collapsedFolders = { ...collapsedFolders, [name]: !collapsedFolders[name] };
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Globe2}
    title="HTTP"
    subtitle={host
      ? `Requests run from ${host.name} — reach internal endpoints from a bastion`
      : "Pick a host from the sidebar"}
  />

  <div class="flex flex-1 overflow-hidden">
    <!-- Saved requests rail -->
    <aside class="w-60 shrink-0 border-r hairline surface-1 flex flex-col">
      <div class="flex items-center gap-2 border-b hairline px-3 py-2">
        <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
          >Saved</span
        >
        <button
          class="ml-auto flex items-center gap-1 rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          title="New request"
          onclick={newRequest}
        >
          <Plus size="12" />
        </button>
      </div>

      <div class="flex-1 overflow-y-auto py-1">
        {#if saved.length === 0}
          <p class="px-3 py-2 text-[10px] text-[var(--color-text-4)]">
            No saved requests yet. Compose one and hit Save.
          </p>
        {/if}
        {#each grouped as [folder, items] (folder)}
          {@const collapsed = collapsedFolders[folder]}
          <div class="px-1">
            <button
              class="flex w-full items-center gap-1 rounded px-2 py-1 text-[10px] uppercase tracking-[0.12em] text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)]"
              onclick={() => toggleFolder(folder)}
            >
              {#if collapsed}
                <ChevronRight size="10" />
              {:else}
                <ChevronDown size="10" />
              {/if}
              {#if folder === ""}
                <FolderOpen size="11" />
                <span>ungrouped</span>
              {:else}
                <Folder size="11" />
                <span class="truncate normal-case tracking-normal">{folder}</span>
              {/if}
              <span class="ml-auto text-[var(--color-text-4)]">{items.length}</span>
            </button>
            {#if !collapsed}
              <ul class="mb-1">
                {#each items as r (r.id)}
                  <li>
                    <div
                      role="button"
                      tabindex="0"
                      class="group flex w-full cursor-pointer items-center gap-2 rounded px-2 py-1 text-left hover:bg-[var(--color-surface-3)]"
                      class:bg-[var(--color-surface-3)]={loadedID === r.id}
                      onclick={() => loadSaved(r)}
                      onkeydown={(e) => {
                        if (e.key === "Enter" || e.key === " ") loadSaved(r);
                      }}
                    >
                      <span
                        class="font-mono text-[9px] uppercase {methodColor(r.method)}"
                        style="min-width:34px"
                      >
                        {r.method}
                      </span>
                      <span class="truncate text-[11px] text-[var(--color-text-1)]"
                        >{r.name}</span
                      >
                      <button
                        class="ml-auto hidden rounded p-0.5 text-[var(--color-text-4)] hover:bg-[var(--color-danger)]/20 hover:text-[var(--color-danger)] group-hover:block"
                        title="Delete"
                        onclick={(e) => deleteSaved(r, e)}
                      >
                        <Trash2 size="10" />
                      </button>
                    </div>
                  </li>
                {/each}
              </ul>
            {/if}
          </div>
        {/each}
      </div>
    </aside>

    <!-- Main editor + response -->
    <div class="flex flex-1 flex-col overflow-hidden">
      {#if !host}
        <div class="flex flex-1 items-center justify-center">
          <div class="text-center">
            <Globe2 size="22" class="mx-auto text-[var(--color-text-4)]" />
            <p class="mt-2 text-xs text-[var(--color-text-3)]">
              Pick a host. Requests are tunneled through its SSH session, so any
              URL the host can resolve is reachable — internal services on a VPC,
              health endpoints behind a firewall, mTLS-protected staging APIs
              (with `insecureSkipVerify` if you don't have the cert handy).
            </p>
          </div>
        </div>
      {:else}
        <div class="space-y-2 border-b hairline surface-1 px-4 py-3">
          <div class="flex items-stretch gap-2">
            <select
              class="rounded-md border hairline bg-[var(--color-surface-3)] px-2 py-2 font-mono text-sm outline-none"
              bind:value={method}
            >
              {#each METHODS as m (m)}
                <option value={m}>{m}</option>
              {/each}
            </select>
            <input
              class="flex-1 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-sm outline-none"
              placeholder="https://api.internal.example.com/v1/health"
              bind:value={url}
              onkeydown={(e) => e.key === "Enter" && send()}
            />
            <button
              class="flex items-center gap-1.5 rounded-md border hairline px-3 py-2 text-xs font-medium text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
              title="Import cURL"
              onclick={() => {
                curlImportOpen = !curlImportOpen;
                curlImportErr = "";
              }}
            >
              <Terminal size="13" />
              cURL
            </button>
            <button
              class="flex items-center gap-1.5 rounded-md border hairline px-3 py-2 text-xs font-medium text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)] disabled:opacity-40"
              disabled={!url}
              title="Copy current request as cURL"
              onclick={copyAsCurl}
            >
              <Copy size="13" />
              Copy cURL
            </button>
            <button
              class="flex items-center gap-1.5 rounded-md border hairline px-3 py-2 text-xs font-medium text-[var(--color-text-2)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
              title={loadedID ? "Update saved request" : "Save request"}
              onclick={saveRequest}
            >
              <Save size="13" />
              {loadedID ? "Update" : "Save"}
            </button>
            <button
              class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
              disabled={busy || !url}
              onclick={send}
            >
              {#if busy}<Loader2 size="14" class="animate-spin" />{:else}<Send size="14" />{/if}
              Send
            </button>
          </div>

          {#if curlImportOpen}
            <div
              class="space-y-2 rounded-md border hairline surface-2 p-2"
            >
              <div class="flex items-center gap-2">
                <Terminal size="12" class="text-[var(--color-text-3)]" />
                <span class="text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]"
                  >Paste cURL</span
                >
                <button
                  class="ml-auto rounded-md bg-[var(--color-accent)] px-2 py-1 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-40"
                  disabled={!curlImportText.trim()}
                  onclick={applyCurlImport}>Import</button
                >
                <button
                  class="rounded-md px-2 py-1 text-[11px] text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)]"
                  onclick={() => (curlImportOpen = false)}>Cancel</button
                >
              </div>
              <textarea
                class="h-20 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-xs outline-none"
                placeholder={"curl -X POST 'https://api.example.com/v1/foo' -H 'Authorization: Bearer …' --data '{}'"}
                bind:value={curlImportText}
              ></textarea>
              {#if curlImportErr}
                <p class="text-[11px] text-[var(--color-danger)]">{curlImportErr}</p>
              {/if}
            </div>
          {/if}

          <!-- Environment selector. Variables are merged into url, headers,
               and body via {{name}} substitution at send time. -->
          <div
            class="flex flex-wrap items-center gap-2 rounded-md border hairline surface-2 px-2 py-1.5"
          >
            <Variable size="12" class="text-[var(--color-text-3)]" />
            <span class="text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Env</span
            >
            <select
              class="rounded-md border hairline bg-[var(--color-surface-3)] px-2 py-1 text-[11px] outline-none"
              value={activeEnv}
              onchange={(e) => selectEnv((e.currentTarget as HTMLSelectElement).value)}
            >
              <option value="">— none —</option>
              {#each Object.keys(envs).sort() as name (name)}
                <option value={name}>{name}</option>
              {/each}
            </select>
            <button
              class="rounded px-1.5 py-1 text-[10px] text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
              onclick={createEnv}
              title="New environment"
            >
              <Plus size="11" />
            </button>
            {#if activeEnv}
              <button
                class="rounded px-2 py-1 text-[10px] text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
                onclick={() => (envEditorOpen = !envEditorOpen)}
                >{envEditorOpen ? "Hide vars" : "Edit vars"}</button
              >
              <button
                class="rounded px-2 py-1 text-[10px] text-[var(--color-text-4)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
                onclick={deleteEnv}>Delete</button
              >
            {/if}
            {#if activeEnv && envs[activeEnv]}
              <span class="ml-2 text-[10px] text-[var(--color-text-4)]">
                {Object.keys(envs[activeEnv]).length} vars · use <span
                  class="font-mono">{`{{name}}`}</span
                > in fields
              </span>
            {/if}
          </div>
          {#if envEditorOpen && activeEnv}
            <div class="rounded-md border hairline surface-2 p-2">
              <div class="mb-1 flex items-center gap-2">
                <span class="text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]"
                  >Variables ({activeEnv})</span
                >
                <button
                  class="ml-auto rounded-md bg-[var(--color-accent)] px-2 py-1 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90"
                  onclick={saveEnvVars}>Save</button
                >
              </div>
              <textarea
                class="h-24 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-xs outline-none"
                placeholder={"BASE_URL=https://api.staging.example.com\nTOKEN=abc123"}
                bind:value={envVarsText}
              ></textarea>
              <p class="mt-1 text-[10px] text-[var(--color-text-4)]">
                One <span class="font-mono">KEY=value</span> per line. Lines starting
                with # are ignored.
              </p>
            </div>
          {/if}

          {#if savePromptOpen}
            <div
              class="flex flex-wrap items-center gap-2 rounded-md border hairline surface-2 p-2"
            >
              <span class="text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]"
                >Name</span
              >
              <input
                class="flex-1 rounded-md border hairline bg-[var(--color-surface-3)] px-2 py-1 text-xs outline-none"
                placeholder="health check"
                bind:value={saveName}
                onkeydown={(e) => e.key === "Enter" && confirmSave()}
              />
              <span class="text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]"
                >Folder</span
              >
              <input
                class="w-28 rounded-md border hairline bg-[var(--color-surface-3)] px-2 py-1 text-xs outline-none"
                placeholder="(optional)"
                bind:value={saveFolder}
                onkeydown={(e) => e.key === "Enter" && confirmSave()}
              />
              <button
                class="rounded-md bg-[var(--color-accent)] px-2 py-1 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90"
                onclick={confirmSave}>Save</button
              >
              <button
                class="rounded-md px-2 py-1 text-[11px] text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)]"
                onclick={() => (savePromptOpen = false)}>Cancel</button
              >
            </div>
          {/if}

          <div class="grid grid-cols-2 gap-2">
            <label class="block">
              <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
                >Headers — one per line, <span class="font-mono">Header: value</span></span
              >
              <textarea
                class="mt-1 h-20 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-xs outline-none"
                bind:value={headersText}
              ></textarea>
            </label>
            <label class="block">
              <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
                >Body</span
              >
              <textarea
                class="mt-1 h-20 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-xs outline-none"
                placeholder="{`{"key": "value"}`}"
                bind:value={body}
              ></textarea>
            </label>
          </div>

          <label
            class="flex items-center gap-2 text-[10px] text-[var(--color-text-3)]"
          >
            <input
              type="checkbox"
              class="accent-[var(--color-accent)]"
              bind:checked={insecureSkipVerify}
            />
            Skip TLS verification (debug only — accepts expired / self-signed certs)
          </label>
        </div>

        <div class="flex-1 overflow-y-auto">
          {#if err}
            <div
              class="m-4 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-3 font-mono text-[11px] whitespace-pre-wrap text-[var(--color-danger)]"
            >
              {err}
            </div>
          {/if}

          {#if response}
            <div class="m-4 space-y-3">
              <div class="flex flex-wrap items-center gap-3 rounded-lg border hairline surface-2 p-3">
                <div class="flex items-center gap-2">
                  <span class="font-mono text-xl font-semibold {statusColor(response.status)}"
                    >{response.status}</span
                  >
                  <span class="text-xs text-[var(--color-text-2)]">{response.statusText}</span>
                </div>
                <span
                  class="rounded border hairline px-2 py-0.5 font-mono text-[10px] text-[var(--color-text-3)]"
                  >{response.proto}</span
                >
                <span class="text-[11px] text-[var(--color-text-3)]">
                  {response.durationMs} ms · {fmtSize(response.sizeBytes)}
                </span>
                {#if response.truncated}
                  <span
                    class="ml-2 inline-flex items-center gap-1 rounded border border-[var(--color-warn)]/40 bg-[var(--color-warn)]/10 px-1.5 py-0.5 text-[10px] text-[var(--color-warn)]"
                  >
                    <AlertTriangle size="10" /> body truncated at 1 MB
                  </span>
                {/if}
                <button
                  class="ml-auto flex items-center gap-1 rounded px-2 py-1 text-[10px] text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
                  onclick={() => response && copy(response.body)}
                >
                  <Copy size="11" /> copy body
                </button>
              </div>

              {#if response.headers.length > 0}
                <details class="rounded-md border hairline surface-2">
                  <summary class="cursor-pointer px-4 py-2 text-[11px] text-[var(--color-text-3)]"
                    >response headers ({response.headers.length})</summary
                  >
                  <div class="border-t hairline">
                    {#each response.headers as h (h.name + h.value)}
                      <div class="grid grid-cols-[180px_1fr] gap-2 border-b hairline px-4 py-1 text-[11px]">
                        <span class="truncate font-mono text-[var(--color-text-3)]"
                          >{h.name}</span
                        >
                        <span class="break-all font-mono text-[var(--color-text-1)]"
                          >{h.value}</span
                        >
                      </div>
                    {/each}
                  </div>
                </details>
              {/if}

              <div class="rounded-md border hairline surface-2">
                <div class="border-b hairline px-4 py-2 text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]">
                  body
                </div>
                <pre
                  class="overflow-x-auto bg-[var(--color-code-bg)] px-4 py-3 font-mono text-[12px] text-[var(--color-text-1)]">{prettyBody}</pre>
              </div>
            </div>
          {:else if !err && !busy}
            <div class="flex h-full items-center justify-center">
              <div class="max-w-md text-center">
                <Globe2 size="22" class="mx-auto text-[var(--color-text-4)]" />
                <p class="mt-2 text-xs text-[var(--color-text-3)]">
                  Compose a request and hit Send. Bodies under 1 MB are returned
                  in full; longer responses get truncated.
                </p>
                <p class="mt-1 text-[10px] text-[var(--color-text-4)]">
                  Redirects are <em>not</em> followed automatically — you'll see
                  the 301/302 directly.
                </p>
              </div>
            </div>
          {/if}
        </div>
      {/if}
    </div>
  </div>
</div>
