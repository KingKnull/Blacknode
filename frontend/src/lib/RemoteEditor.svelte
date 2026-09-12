<script lang="ts">
  import { onDestroy, onMount, tick } from "svelte";
  import { EditorView, basicSetup } from "codemirror";
  import { EditorState, type Extension } from "@codemirror/state";
  import { keymap } from "@codemirror/view";
  import { oneDark } from "@codemirror/theme-one-dark";
  import { json } from "@codemirror/lang-json";
  import { yaml } from "@codemirror/lang-yaml";
  import { javascript } from "@codemirror/lang-javascript";
  import { python } from "@codemirror/lang-python";
  import { markdown } from "@codemirror/lang-markdown";
  import { html } from "@codemirror/lang-html";
  import { css } from "@codemirror/lang-css";
  import { sql } from "@codemirror/lang-sql";
  import { xml } from "@codemirror/lang-xml";
  import { SFTPService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import { app } from "./state.svelte";
  import { fileDiff } from "./fileDiff";
  import Dialog from "./Dialog.svelte";
  import {
    FileCode,
    Save,
    X,
    Loader2,
    AlertTriangle,
    Check,
  } from "@lucide/svelte";

  type Props = {
    hostID: string;
    remotePath: string;
    onClose: () => void;
  };
  let { hostID, remotePath, onClose }: Props = $props();

  let containerEl: HTMLDivElement | undefined = $state();
  let view: EditorView | undefined;

  let loading = $state(true);
  let saving = $state(false);
  let err = $state("");
  let original = $state("");
  let dirty = $state(false);
  let binaryWarning = $state(false);
  let savedAt = $state<number | null>(null);
  let revision = $state("");
  let reviewDraft = $state<string | null>(null);
  let createBackup = $state(true);
  let backupPath = $state("");
  let restoring = $state(false);
  let conflict = $state(false);
  const diff = $derived(fileDiff(original, reviewDraft ?? original));

  const filename = $derived(remotePath.split("/").pop() ?? remotePath);
  const language = $derived(langForPath(remotePath));

  function langForPath(p: string): Extension | null {
    const ext = p.toLowerCase().split(".").pop() ?? "";
    switch (ext) {
      case "json":
        return json();
      case "yaml":
      case "yml":
        return yaml();
      case "js":
      case "mjs":
      case "cjs":
      case "ts":
      case "tsx":
      case "jsx":
        return javascript({ typescript: ext === "ts" || ext === "tsx" });
      case "py":
        return python();
      case "md":
      case "markdown":
        return markdown();
      case "html":
      case "htm":
        return html();
      case "css":
      case "scss":
        return css();
      case "sql":
        return sql();
      case "xml":
      case "svg":
      case "plist":
        return xml();
      default:
        return null;
    }
  }

  // Heuristic: high ratio of nulls or non-printable bytes => probably binary.
  // This is the classic file(1) approach in 5 lines.
  function looksBinary(s: string): boolean {
    if (s.length === 0) return false;
    let bad = 0;
    const sample = s.length > 4096 ? s.slice(0, 4096) : s;
    for (let i = 0; i < sample.length; i++) {
      const c = sample.charCodeAt(i);
      // null, or control chars excluding tab/lf/cr
      if (c === 0 || (c < 32 && c !== 9 && c !== 10 && c !== 13)) bad++;
    }
    return bad / sample.length > 0.05;
  }

  function b64ToText(b64: string): string {
    const bin = atob(b64);
    // Decode as UTF-8 — atob gives latin1 chars; we re-encode through Uint8Array
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  }

  function textToB64(s: string): string {
    const bytes = new TextEncoder().encode(s);
    let bin = "";
    for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin);
  }

  async function load() {
    loading = true;
    err = "";
    try {
      const snapshot = await SFTPService.ReadForEdit(hostID, remotePath);
      const text = b64ToText(snapshot.contentBase64);
      binaryWarning = looksBinary(text);
      original = text;
      revision = snapshot.revision;
      reviewDraft = null;
      dirty = false;
      conflict = false;
      await tick();
      mountEditor(text);
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      loading = false;
    }
  }

  function mountEditor(initial: string) {
    if (!containerEl) return;
    view?.destroy();

    const exts: Extension[] = [
      basicSetup,
      keymap.of([
        {
          key: "Mod-s",
          run: () => {
            review();
            return true;
          },
        },
      ]),
      EditorView.updateListener.of((u) => {
        if (u.docChanged) {
          dirty = u.state.doc.toString() !== original;
          if (dirty) savedAt = null;
        }
      }),
      EditorView.theme({
        "&": { height: "100%", fontSize: "13px" },
        ".cm-scroller": {
          fontFamily:
            '"JetBrains Mono Variable", "Cascadia Mono", Menlo, Consolas, monospace',
        },
      }),
    ];
    // CodeMirror's default styling is light. Only push oneDark when the app
    // is in dark mode. Active editors keep the theme they spawned with —
    // toggling settings.theme requires reopening the file to switch.
    if (app.settings.theme !== "light") exts.push(oneDark);
    if (language) exts.push(language);

    view = new EditorView({
      state: EditorState.create({ doc: initial, extensions: exts }),
      parent: containerEl,
    });
    view.focus();
  }

  function review() {
    if (!view || saving || !dirty || binaryWarning || conflict) return;
    restoring = false;
    reviewDraft = view.state.doc.toString();
  }

  async function restoreBackup() {
    if (!backupPath || dirty || saving) return;
    saving = true;
    err = "";
    try {
      const snapshot = await SFTPService.ReadForEdit(hostID, backupPath);
      reviewDraft = b64ToText(snapshot.contentBase64);
      restoring = true;
    } catch (e) { err = String(e); }
    finally { saving = false; }
  }

  async function save() {
    if (reviewDraft === null || saving || binaryWarning || conflict) return;
    saving = true;
    err = "";
    try {
      const text = reviewDraft;
      const result = await SFTPService.SaveForEdit(hostID, remotePath, textToB64(text), revision, createBackup);
      revision = result.revision;
      if (result.backupPath) backupPath = result.backupPath;
      original = text;
      reviewDraft = null;
      restoring = false;
      mountEditor(text);
      dirty = false;
      savedAt = Date.now();
    } catch (e: any) {
      err = String(e?.message ?? e);
      conflict = err.includes("REMOTE_FILE_CHANGED");
    } finally {
      saving = false;
    }
  }

  function close() {
    if (saving) return;
    if (dirty) {
      const ok = confirm("You have unsaved changes. Discard?");
      if (!ok) return;
    }
    onClose();
  }

  onMount(load);
  onDestroy(() => view?.destroy());
</script>

<Dialog label={`Edit ${filename}`} onclose={close} panelClass="flex h-[85vh] w-[min(95vw,1200px)] flex-col overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl">
    <div class="flex items-center gap-2 border-b hairline px-4 py-2.5">
      <FileCode size="14" class="text-[var(--color-accent)]" />
      <div class="min-w-0">
        <div class="truncate type-body font-semibold">{filename}</div>
        <div class="truncate font-mono type-micro text-[var(--color-text-3)]">
          {remotePath}
        </div>
      </div>

      {#if binaryWarning}
        <span
          class="ml-2 inline-flex items-center gap-1 rounded-md border border-[var(--color-warn)]/40 bg-[var(--color-warn)]/10 px-2 py-0.5 type-micro text-[var(--color-warn)]"
          title="High ratio of non-printable bytes — saving may corrupt this file"
        >
          <AlertTriangle size="10" /> binary?
        </span>
      {/if}

      {#if dirty}
        <span class="ml-2 type-micro text-[var(--color-warn)]">● modified</span>
      {:else if savedAt}
        <span class="ml-2 inline-flex items-center gap-1 type-micro text-[var(--color-accent)]">
          <Check size="10" /> saved
        </span>
      {/if}

      <div class="ml-auto flex items-center gap-2">
        <kbd class="rounded border hairline px-1 py-0.5 font-mono type-micro text-[var(--color-text-4)]"
          >⌘S</kbd
        >
        <button
          class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-3 py-1.5 type-caption font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
          disabled={!dirty || saving || binaryWarning || conflict || reviewDraft !== null}
          onclick={review}
        >
          {#if saving}
            <Loader2 size="11" class="animate-spin" />
          {:else}
            <Save size="11" />
          {/if}
          Review changes
        </button>
        <button
          class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={close}
          aria-label="Close editor"
          disabled={saving}
        >
          <X size="14" />
        </button>
      </div>
    </div>

    {#if err}
      <div class="m-3 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-3 type-caption text-[var(--color-danger)]">
        {err}
        {#if conflict}
          <div class="mt-2 flex gap-3">
            <button class="underline" onclick={async () => { try { await navigator.clipboard.writeText(reviewDraft ?? view?.state.doc.toString() ?? ""); app.toast("ok", "Edits copied"); } catch (e) { app.toast("error", "Could not copy edits", String(e)); } }}>Copy my edits</button>
            <button class="underline" onclick={() => { if (confirm("Reload the remote file and discard your local edits? Copy them first if you need to merge them.")) void load(); }}>Reload remote file</button>
          </div>
        {/if}
      </div>
    {/if}

    {#if backupPath}
      <div class="flex items-center gap-3 border-b hairline px-4 py-2 type-caption">
        <span class="min-w-0 flex-1 truncate text-[var(--color-text-3)]" title={backupPath}>Backup: {backupPath}</span>
        <button class="shrink-0 rounded border hairline px-2 py-1 disabled:opacity-40" disabled={dirty || saving || conflict} onclick={restoreBackup}>Review restore</button>
      </div>
    {/if}

    {#if reviewDraft !== null}
      <div class="flex min-h-0 flex-1 flex-col">
        <div class="flex items-center gap-3 border-b hairline px-4 py-3 type-caption">
          <strong>{restoring ? "Restore backup" : "Review changes"}</strong>
          <span class="text-[var(--color-text-3)]">− original · + replacement</span>
          <button class="ml-auto underline disabled:opacity-40" disabled={saving} onclick={() => { reviewDraft = null; restoring = false; }}>Back to editor</button>
        </div>
        <pre class="min-h-0 flex-1 overflow-auto p-3 font-mono type-caption">{#each diff.lines as line}<div class="{line.kind === 'removed' ? 'bg-[var(--color-danger)]/10 text-[var(--color-danger)]' : line.kind === 'added' ? 'bg-[var(--color-success)]/10 text-[var(--color-success)]' : 'text-[var(--color-text-3)]'}">{line.kind === 'removed' ? '−' : line.kind === 'added' ? '+' : ' '} {line.text}</div>{/each}</pre>
        {#if diff.omitted}<p class="px-4 py-2 type-caption">Preview limited; {diff.omitted} more lines will also be saved.</p>{/if}
        <div class="flex items-center gap-3 border-t hairline px-4 py-3 type-caption">
          <label class="flex items-center gap-2"><input type="checkbox" bind:checked={createBackup} disabled={saving} />Keep a backup on the server</label>
          <button class="ml-auto rounded bg-[var(--color-accent)] px-3 py-2 text-[var(--color-surface-0)] disabled:opacity-40" disabled={saving || conflict} onclick={save}>{saving ? "Saving…" : restoring ? "Restore backup" : "Save changes"}</button>
        </div>
      </div>
    {/if}

    <div class="min-h-0 flex-1 overflow-hidden" class:hidden={reviewDraft !== null}>
      {#if loading}
        <div class="flex h-full items-center justify-center type-caption text-[var(--color-text-3)]">
          <Loader2 size="14" class="animate-spin" /> &nbsp;loading…
        </div>
      {/if}
      <div bind:this={containerEl} class="h-full" class:hidden={loading}></div>
    </div>
</Dialog>
