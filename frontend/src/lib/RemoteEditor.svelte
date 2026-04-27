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
  import { SFTPService } from "../../bindings/github.com/blacknode/blacknode";
  import { app } from "./state.svelte";
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
    return new TextDecoder("utf-8", { fatal: false }).decode(bytes);
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
      const password = app.hostPasswords[hostID] ?? "";
      const b64 = (await SFTPService.Download(hostID, password, remotePath)) as string;
      const text = b64ToText(b64);
      binaryWarning = looksBinary(text);
      original = text;
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

    const exts: Extension[] = [
      basicSetup,
      keymap.of([
        {
          key: "Mod-s",
          run: () => {
            void save();
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

  async function save() {
    if (!view || saving || !dirty) return;
    saving = true;
    err = "";
    try {
      const text = view.state.doc.toString();
      const password = app.hostPasswords[hostID] ?? "";
      await SFTPService.WriteFile(hostID, password, remotePath, textToB64(text));
      original = text;
      dirty = false;
      savedAt = Date.now();
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      saving = false;
    }
  }

  function close() {
    if (dirty) {
      const ok = confirm("You have unsaved changes. Discard?");
      if (!ok) return;
    }
    onClose();
  }

  onMount(load);
  onDestroy(() => view?.destroy());
</script>

<div
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm"
  role="presentation"
  onclick={(e) => {
    if (e.target === e.currentTarget) close();
  }}
>
  <div
    class="flex max-h-[90vh] w-[min(95vw,1200px)] flex-col overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/60"
  >
    <div class="flex items-center gap-2 border-b hairline px-4 py-2.5">
      <FileCode size="14" class="text-[var(--color-accent)]" />
      <div class="min-w-0">
        <div class="truncate text-sm font-semibold">{filename}</div>
        <div class="truncate font-mono text-[10px] text-[var(--color-text-3)]">
          {remotePath}
        </div>
      </div>

      {#if binaryWarning}
        <span
          class="ml-2 inline-flex items-center gap-1 rounded-md border border-[var(--color-warn)]/40 bg-[var(--color-warn)]/10 px-2 py-0.5 text-[10px] text-[var(--color-warn)]"
          title="High ratio of non-printable bytes — saving may corrupt this file"
        >
          <AlertTriangle size="10" /> binary?
        </span>
      {/if}

      {#if dirty}
        <span class="ml-2 text-[10px] text-[var(--color-warn)]">● modified</span>
      {:else if savedAt}
        <span class="ml-2 inline-flex items-center gap-1 text-[10px] text-[var(--color-accent)]">
          <Check size="10" /> saved
        </span>
      {/if}

      <div class="ml-auto flex items-center gap-2">
        <kbd class="rounded border hairline px-1 py-0.5 font-mono text-[10px] text-[var(--color-text-4)]"
          >⌘S</kbd
        >
        <button
          class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
          disabled={!dirty || saving}
          onclick={save}
        >
          {#if saving}
            <Loader2 size="11" class="animate-spin" />
          {:else}
            <Save size="11" />
          {/if}
          Save
        </button>
        <button
          class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={close}
        >
          <X size="14" />
        </button>
      </div>
    </div>

    {#if err}
      <div class="m-3 rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-3 text-xs text-[var(--color-danger)]">
        {err}
      </div>
    {/if}

    <div class="flex-1 overflow-hidden">
      {#if loading}
        <div class="flex h-full items-center justify-center text-xs text-[var(--color-text-3)]">
          <Loader2 size="14" class="animate-spin" /> &nbsp;loading…
        </div>
      {/if}
      <div bind:this={containerEl} class="h-full" class:hidden={loading}></div>
    </div>
  </div>
</div>
