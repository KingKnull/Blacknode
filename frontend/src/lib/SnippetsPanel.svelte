<script lang="ts">
  import { onMount } from "svelte";
  import { SnippetService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Snippet } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import SnippetApplyDialog from "./SnippetApplyDialog.svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";
  import {
    BookmarkIcon,
    Plus,
    Pencil,
    Trash2,
    Search,
    Wand,
    X,
    Loader2,
    Tag as TagIcon,
  } from "@lucide/svelte";
  import Dialog from "./Dialog.svelte";
  import { bus } from "./events";

  let list = $state<Snippet[]>([]);
  let filter = $state("");
  let editing: Snippet | null = $state(null);
  let creating = $state(false);
  let applying: Snippet | null = $state(null);

  let f_name = $state("");
  let f_body = $state("");
  let f_desc = $state("");
  let f_tags = $state(""); // comma-separated for the form
  let f_busy = $state(false);
  let f_err = $state("");

  onMount(refresh);

  async function refresh() {
    list = ((await SnippetService.List()) ?? []) as Snippet[];
  }

  let visible = $derived(
    list.filter((s) => {
      if (!filter) return true;
      const q = filter.toLowerCase();
      return (
        s.name.toLowerCase().includes(q) ||
        s.body.toLowerCase().includes(q) ||
        (s.description ?? "").toLowerCase().includes(q) ||
        s.tags.some((t) => t.toLowerCase().includes(q))
      );
    }),
  );

  function startCreate() {
    creating = true;
    editing = null;
    f_name = "";
    f_body = "";
    f_desc = "";
    f_tags = "";
    f_err = "";
  }

  function startEdit(s: Snippet) {
    editing = s;
    creating = false;
    f_name = s.name;
    f_body = s.body;
    f_desc = s.description ?? "";
    f_tags = (s.tags ?? []).join(", ");
    f_err = "";
  }

  function closeForm() {
    editing = null;
    creating = false;
  }

  async function save() {
    f_err = "";
    if (!f_name.trim() || !f_body.trim()) {
      f_err = "Name and body are required";
      return;
    }
    f_busy = true;
    try {
      const tags = f_tags
        .split(",")
        .map((t) => t.trim())
        .filter(Boolean);
      if (editing) {
        await SnippetService.Update({
          ...editing,
          name: f_name,
          body: f_body,
          description: f_desc,
          tags,
        } as Snippet);
      } else {
        await SnippetService.Create({
          name: f_name,
          body: f_body,
          description: f_desc,
          tags,
        } as unknown as Snippet);
      }
      await refresh();
      closeForm();
    } catch (e: any) {
      f_err = String(e?.message ?? e);
    } finally {
      f_busy = false;
    }
  }

  let snippetToDelete = $state<Snippet | null>(null);

  async function del() {
    if (!snippetToDelete) return;
    const id = snippetToDelete.id;
    snippetToDelete = null;
    try {
      await SnippetService.Delete(id);
      await refresh();
    } catch (e: any) {
      app.toast('error', 'DELETE FAILED', String(e?.message ?? e));
    }
  }

  // Find the active terminal session and insert the rendered command into it.
  function insertIntoActiveTerminal(rendered: string) {
    bus.emit('insert-into-active-terminal', rendered);
    app.view = "terminals";
  }

  function preview(body: string) {
    return body.length > 200 ? body.slice(0, 200) + "…" : body;
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={BookmarkIcon}
    title="Snippets"
    subtitle={"Reusable command templates with {{variables}} that get prompted on apply"}
  >
    {#snippet actions()}
      <button
        class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-2.5 py-1 type-caption font-medium text-[var(--color-surface-0)] hover:opacity-90"
        onclick={startCreate}
      >
        <Plus size="11" /> new
      </button>
    {/snippet}
  </PageHeader>

  <div class="border-b hairline surface-1 px-4 py-3">
    <div
      class="relative flex items-center rounded-md border hairline bg-[var(--color-surface-3)] focus-within:border-[var(--color-accent)]/40"
    >
      <Search size="13" class="absolute left-3 text-[var(--color-text-4)]" />
      <input
        bind:value={filter}
        placeholder="filter by name, body, tag…"
        class="w-full bg-transparent py-2 pl-9 pr-3 type-body outline-none placeholder:text-[var(--color-text-4)]"
      />
    </div>
  </div>

  <div class="flex-1 overflow-y-auto">
    {#if visible.length === 0 && !creating && !editing}
      <div class="flex h-full items-center justify-center">
        <div class="max-w-md text-center">
          <BookmarkIcon size="22" class="mx-auto text-[var(--color-text-4)]" />
          <p class="mt-2 type-caption text-[var(--color-text-3)]">
            {#if filter}
              no snippets match "{filter}"
            {:else}
              No snippets yet. Click "new" to add one. Use <span
                class="font-mono">&#123;&#123;name&#125;&#125;</span
              > or <span class="font-mono">&#123;&#123;name|default&#125;&#125;</span>
              for variables.
            {/if}
          </p>
        </div>
      </div>
    {:else}
      <div class="divide-y divide-[var(--color-line)]">
        {#each visible as s (s.id)}
          <div class="px-4 py-3 transition-colors hover:bg-[var(--color-surface-2)]">
            <div class="flex items-center gap-2">
              <span class="type-body font-medium">{s.name}</span>
              {#each s.tags as t (t)}
                <span
                  class="inline-flex items-center gap-1 rounded-sm border hairline bg-[var(--color-surface-3)] px-1.5 py-0.5 type-nano font-mono text-[var(--color-text-3)]"
                >
                  <TagIcon size="9" />
                  {t}
                </span>
              {/each}
              <div class="ml-auto flex items-center gap-1">
                <button
                  class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-2 py-0.5 type-micro font-medium text-[var(--color-surface-0)] hover:opacity-90"
                  onclick={() => (applying = s)}
                >
                  <Wand size="10" /> apply
                </button>
                <button
                  class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
                  onclick={() => startEdit(s)}
                  title="Edit"
                >
                  <Pencil size="10" />
                </button>
                <button
                  class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
                  onclick={() => (snippetToDelete = s)}
                  title="Delete"
                  aria-label="Delete snippet {s.name}"
                >
                  <Trash2 size="10" />
                </button>
              </div>
            </div>
            {#if s.description}
              <p class="mt-1 type-caption text-[var(--color-text-3)]">
                {s.description}
              </p>
            {/if}
            <pre
              class="mt-2 overflow-x-auto rounded bg-[var(--color-code-bg)] px-3 py-2 font-mono type-caption text-[var(--color-text-2)]">{preview(s.body)}</pre>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

{#if creating || editing}
  <Dialog
    onclose={closeForm}
    labelledby="snippet-editor-title"
    backdropClass="bg-black/70 backdrop-blur-sm"
    panelClass="w-[640px] overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/50"
  >
    {#snippet children()}
      <div class="flex items-center gap-2 border-b hairline px-5 py-3">
        <BookmarkIcon size="14" class="text-[var(--color-accent)]" />
        <h3 id="snippet-editor-title" class="type-body font-semibold">{editing ? "Edit snippet" : "New snippet"}</h3>
        <button
          class="ml-auto rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={closeForm}
        >
          <X size="14" />
        </button>
      </div>

      <div class="space-y-3 p-5 type-body">
        <label class="block">
          <span class="type-eyebrow text-[var(--color-text-3)]"
            >Name</span
          >
          <input
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            bind:value={f_name}
            placeholder="restart nginx"
            data-autofocus
          />
        </label>
        <label class="block">
          <span class="type-eyebrow text-[var(--color-text-3)]"
            >Description</span
          >
          <input
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            bind:value={f_desc}
            placeholder="optional"
          />
        </label>
        <label class="block">
          <span class="type-eyebrow text-[var(--color-text-3)]"
            >Body — use &#123;&#123;name&#125;&#125; for variables</span
          >
          <textarea
            class="mt-1 h-40 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-caption outline-none"
            bind:value={f_body}
            placeholder={"sudo systemctl restart {{service|nginx}}"}
          ></textarea>
        </label>
        <label class="block">
          <span class="type-eyebrow text-[var(--color-text-3)]"
            >Tags (comma-separated)</span
          >
          <input
            class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 outline-none"
            bind:value={f_tags}
            placeholder="deploy, prod"
          />
        </label>
        {#if f_err}
          <p class="type-caption text-[var(--color-danger)]" role="alert">{f_err}</p>
        {/if}
      </div>

      <div class="flex items-center justify-end gap-2 border-t hairline px-5 py-3">
        <button
          class="rounded-md px-3 py-1.5 type-caption text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={closeForm}>Cancel</button
        >
        <button
          class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-3 py-1.5 type-caption font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
          disabled={f_busy}
          onclick={save}
        >
          {#if f_busy}<Loader2 size="11" class="animate-spin" />{:else}Save{/if}
        </button>
      </div>
    {/snippet}
  </Dialog>
{/if}

{#if applying}
  <SnippetApplyDialog
    snippet={applying}
    onCancel={() => (applying = null)}
    onApply={(rendered) => {
      applying = null;
      insertIntoActiveTerminal(rendered);
    }}
  />
{/if}

{#if snippetToDelete}
  <ConfirmDanger
    title="DELETE SNIPPET"
    body="Delete snippet '{snippetToDelete.name}'? This cannot be undone."
    severity="warn"
    productionHosts={[]}
    onCancel={() => (snippetToDelete = null)}
    onConfirm={del}
  />
{/if}
