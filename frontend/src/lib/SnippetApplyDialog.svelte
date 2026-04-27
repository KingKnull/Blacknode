<script lang="ts">
  import { onMount, tick } from "svelte";
  import { SnippetService } from "../../bindings/github.com/blacknode/blacknode";
  import type { SnippetVariable } from "../../bindings/github.com/blacknode/blacknode/models";
  import type { Snippet } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { Wand, X, Loader2 } from "@lucide/svelte";

  type Props = {
    snippet: Snippet;
    onCancel: () => void;
    onApply: (rendered: string) => void;
  };
  let { snippet, onCancel, onApply }: Props = $props();

  let vars = $state<SnippetVariable[]>([]);
  let values = $state<Record<string, string>>({});
  // svelte-ignore state_referenced_locally
  let preview = $state(snippet.body);
  let busy = $state(false);
  let firstInput: HTMLInputElement | undefined = $state();

  onMount(async () => {
    vars = ((await SnippetService.ExtractVariables(snippet.body)) ??
      []) as SnippetVariable[];
    for (const v of vars) values[v.name] = v.default ?? "";
    recompute();
    await tick();
    firstInput?.focus();
  });

  function recompute() {
    let out = snippet.body;
    out = out.replace(
      /\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:\|([^}]*))?\}\}/g,
      (_m, name: string, def: string) => {
        const v = values[name];
        if (v && v !== "") return v;
        return (def ?? "").trim();
      },
    );
    preview = out;
  }

  async function apply() {
    busy = true;
    try {
      const rendered = (await SnippetService.Apply(
        snippet.id,
        values,
        "",
        "",
        true,
      )) as string;
      onApply(rendered);
    } finally {
      busy = false;
    }
  }
</script>

<div
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm"
  role="presentation"
  onclick={(e) => {
    if (e.target === e.currentTarget) onCancel();
  }}
>
  <div
    class="w-[560px] overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/50"
  >
    <div class="flex items-center gap-2 border-b hairline px-5 py-3">
      <Wand size="14" class="text-[var(--color-accent)]" />
      <h3 class="truncate text-sm font-semibold">{snippet.name}</h3>
      <button
        class="ml-auto rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={onCancel}
      >
        <X size="14" />
      </button>
    </div>

    <div class="space-y-3 p-5 text-sm">
      {#if snippet.description}
        <p class="text-xs text-[var(--color-text-3)]">{snippet.description}</p>
      {/if}

      {#if vars.length === 0}
        <p class="text-xs text-[var(--color-text-3)]">
          No variables — the snippet will be inserted as-is.
        </p>
      {:else}
        <div class="space-y-2">
          {#each vars as v, i (v.name)}
            <label class="block">
              <span
                class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
                >{v.name}</span
              >
              {#if i === 0}
                <input
                  bind:this={firstInput}
                  class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-sm outline-none"
                  placeholder={v.default || `value for ${v.name}`}
                  bind:value={values[v.name]}
                  oninput={recompute}
                />
              {:else}
                <input
                  class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-sm outline-none"
                  placeholder={v.default || `value for ${v.name}`}
                  bind:value={values[v.name]}
                  oninput={recompute}
                />
              {/if}
            </label>
          {/each}
        </div>
      {/if}

      <div>
        <div
          class="mb-1 text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
        >
          Preview
        </div>
        <pre
          class="overflow-x-auto rounded-md border hairline bg-[var(--color-code-bg)] px-3 py-2 font-mono text-[12px] text-[var(--color-text-1)]">{preview}</pre>
      </div>
    </div>

    <div class="flex items-center justify-end gap-2 border-t hairline px-5 py-3">
      <button
        class="rounded-md px-3 py-1.5 text-xs text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={onCancel}>Cancel</button
      >
      <button
        class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
        disabled={busy}
        onclick={apply}
      >
        {#if busy}<Loader2 size="11" class="animate-spin" />{:else}Insert into terminal{/if}
      </button>
    </div>
  </div>
</div>
