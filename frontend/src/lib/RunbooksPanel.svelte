<script module lang="ts">
  import type { RunbookResult } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  // Keep an in-flight run and its final results when navigating to another tool.
  const execution = $state({ running: false, runID: "", name: "", results: [] as RunbookResult[], error: "" });
</script>

<script lang="ts">
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { RunbookService, SnippetService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Runbook, RunbookStep } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import type { Snippet } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import { checkCommand } from "./danger";
  import PageHeader from "./PageHeader.svelte";
  import Dialog from "./Dialog.svelte";
  import { ListChecks, Plus, Play, Square, Save, Trash2, ArrowUp, ArrowDown } from "@lucide/svelte";

  const blank = (): Runbook => ({ id: "", name: "New runbook", steps: [{ name: "Check host", command: "uname -a" }], timeoutSeconds: 60, stopOnFailure: true });
  let books = $state<Runbook[]>([]);
  let snippets = $state<Snippet[]>([]);
  let book = $state<Runbook>(blank());
  let selectedID = $state("");
  let snippetID = $state("");
  let hosts = $state<string[]>([]);
  let values = $state<Record<string, string>>({});
  let busy = $state(false);
  let error = $state("");
  let phrase = $state("");
  let pending = $state<{ book: Runbook; steps: RunbookStep[]; hosts: string[]; values: Record<string, string>; phrase: string; warning: string } | null>(null);
  const variables = $derived.by(() => {
    const result = new Map<string, string>();
    for (const step of book.steps) for (const match of step.command.matchAll(/\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:\|([^}]*))?\}\}/g)) {
      if (!result.has(match[1])) result.set(match[1], match[2]?.trim() ?? "");
    }
    return [...result].map(([name, fallback]) => ({ name, fallback }));
  });

  onMount(() => {
    void Promise.all([RunbookService.List(), SnippetService.List()]).then(([saved, savedSnippets]) => { books = saved ?? []; snippets = savedSnippets ?? []; }).catch((e) => error = String(e));
    return Events.On("runbook:progress", (event: any) => {
      const progress = event?.data;
      if (progress?.runID !== execution.runID) return;
      const result = progress.result as RunbookResult;
      execution.results = [...execution.results.filter((r) => r.stepIndex !== result.stepIndex || r.result.hostID !== result.result.hostID), result];
    });
  });

  function load() {
    const saved = books.find((b) => b.id === selectedID);
    book = saved ? JSON.parse(JSON.stringify(saved)) : blank();
    values = {}; error = "";
  }
  function move(index: number, delta: number) {
    const next = [...book.steps];
    [next[index], next[index+delta]] = [next[index+delta], next[index]];
    book.steps = next;
  }
  async function save() {
    busy = true; error = "";
    try { book = await RunbookService.Save($state.snapshot(book)); selectedID = book.id; books = await RunbookService.List(); app.toast("ok", "Runbook saved", book.name); }
    catch (e) { error = String(e); }
    finally { busy = false; }
  }
  async function remove() {
    if (!book.id || !confirm(`Delete runbook “${book.name}”?`)) return;
    busy = true;
    try { await RunbookService.Delete(book.id); books = await RunbookService.List(); selectedID = ""; load(); }
    catch (e) { error = String(e); }
    finally { busy = false; }
  }
  async function review() {
    if (!hosts.length) return;
    busy = true; error = "";
    try {
      const snapshot = $state.snapshot(book);
      const inputs = Object.fromEntries(variables.filter((v) => v.name in values).map((v) => [v.name, values[v.name]]));
      const steps = await RunbookService.Preview(snapshot, inputs);
      const production = hosts.filter((id) => app.hosts.some((h) => h.id === id && h.environment?.toLowerCase() === "production"));
      const dangers = steps.map((step) => checkCommand(step.command)).filter(Boolean);
      const blocked = dangers.some((d) => d?.level === "block-without-confirm");
      phrase = "";
      pending = { book: snapshot, steps, hosts: [...hosts], values: inputs,
        phrase: blocked ? production.length ? "destroy production" : "I understand" : "",
        warning: [production.length ? `${production.length} production host(s) selected.` : "", ...dangers.map((d) => d!.reason)].filter(Boolean).join(" "),
      };
    } catch (e) { error = String(e); }
    finally { busy = false; }
  }
  async function run() {
    if (!pending || (pending.phrase && phrase !== pending.phrase)) return;
    const request = pending; pending = null;
    execution.running = true; execution.runID = crypto.randomUUID(); execution.name = request.book.name; execution.results = []; execution.error = "";
    try { execution.results = await RunbookService.Run(execution.runID, request.book, request.hosts, request.values); }
    catch (e) { execution.error = String(e); execution.results = execution.results.map((r) => r.status === "running" ? { ...r, status: "failed" } : r); }
    finally { execution.running = false; }
  }
  async function cancel() {
    try { await RunbookService.Cancel(execution.runID); }
    catch (e) { execution.error = String(e); }
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader icon={ListChecks} title="Runbooks" subtitle="Reusable steps across SSH hosts" />
  <div class="flex-1 overflow-y-auto p-4 space-y-4">
    {#if error}<p role="alert" class="type-caption text-[var(--color-danger)]">{error}</p>{/if}
    <fieldset disabled={busy || execution.running} class="space-y-4 disabled:opacity-60">
      <div class="flex flex-wrap items-center gap-2 type-caption">
        <select aria-label="Saved runbook" bind:value={selectedID} onchange={load} class="rounded border hairline surface-3 p-2"><option value="">New runbook</option>{#each books as saved}<option value={saved.id}>{saved.name}</option>{/each}</select>
        <input aria-label="Runbook name" maxlength="120" bind:value={book.name} class="min-w-0 flex-1 rounded border hairline surface-3 p-2" />
        <button class="flex items-center gap-1 rounded border hairline px-3 py-2" onclick={save}><Save size="13" />Save</button>
        <button aria-label="Delete runbook" disabled={!book.id} class="rounded border hairline p-2 disabled:opacity-40" onclick={remove}><Trash2 size="13" /></button>
      </div>
      <div class="space-y-2">
        {#each book.steps as step, i}
          <div class="rounded border hairline surface-2 p-3">
            <div class="mb-2 flex items-center gap-2 type-caption">
              <span class="text-[var(--color-text-3)]">{i+1}.</span>
              <input aria-label={`Step ${i+1} name`} bind:value={step.name} maxlength="120" class="min-w-0 flex-1 rounded border hairline surface-3 px-2 py-1" />
              <button aria-label={`Move step ${i+1} up`} disabled={i === 0} onclick={() => move(i,-1)} class="disabled:opacity-30"><ArrowUp size="13" /></button>
              <button aria-label={`Move step ${i+1} down`} disabled={i === book.steps.length-1} onclick={() => move(i,1)} class="disabled:opacity-30"><ArrowDown size="13" /></button>
              <button aria-label={`Remove step ${i+1}`} disabled={book.steps.length === 1} onclick={() => book.steps = book.steps.filter((_, index) => i !== index)} class="disabled:opacity-30"><Trash2 size="13" /></button>
            </div>
            <textarea aria-label={`Step ${i+1} command`} rows="2" maxlength="16384" bind:value={step.command} class="w-full rounded border hairline surface-3 p-2 font-mono type-caption"></textarea>
          </div>
        {/each}
      </div>
      <div class="flex flex-wrap items-center gap-2 type-caption">
        <button class="flex items-center gap-1 rounded border hairline px-3 py-2 disabled:opacity-40" disabled={book.steps.length >= 32} onclick={() => book.steps.push({ name: `Step ${book.steps.length+1}`, command: "" })}><Plus size="13" />Add step</button>
        <select aria-label="Snippet to add" bind:value={snippetID} class="rounded border hairline surface-3 p-2"><option value="">Choose a snippet</option>{#each snippets as snippet}<option value={snippet.id}>{snippet.name}</option>{/each}</select>
        <button class="rounded border hairline px-3 py-2 disabled:opacity-40" disabled={!snippetID || book.steps.length >= 32} onclick={() => { const snippet = snippets.find((s) => s.id === snippetID); if (snippet) book.steps.push({ name: snippet.name, command: snippet.body }); }}>Add snippet</button>
      </div>
      {#if variables.length}
        <div class="rounded border hairline p-3">
          <p class="mb-2 type-caption text-[var(--color-text-3)]">Variable values are inserted as typed. Check the final commands before running.</p>
          <div class="grid grid-cols-2 gap-2">{#each variables as variable}<label class="type-caption">{variable.name}<input class="mt-1 w-full rounded border hairline surface-3 p-2" placeholder={variable.fallback || "Required"} value={values[variable.name] ?? ""} oninput={(e) => values[variable.name] = e.currentTarget.value} /></label>{/each}</div>
        </div>
      {/if}
      <div class="flex flex-wrap gap-4 type-caption">
        <label class="flex items-center gap-2"><input type="checkbox" bind:checked={book.stopOnFailure} />Skip later steps on hosts that fail</label>
        <label class="flex items-center gap-2">Timeout per step (seconds)<input type="number" min="1" max="3600" bind:value={book.timeoutSeconds} class="w-24 rounded border hairline surface-3 p-1" /></label>
      </div>
      <p class="type-caption text-[var(--color-text-3)]">Each step runs in a fresh shell. Changes to the working directory or shell variables do not carry into later steps.</p>
      <fieldset class="rounded border hairline p-3"><legend class="type-caption">Target hosts</legend>
        <div class="flex flex-wrap gap-3">{#each app.hosts.filter((h) => !h.protocol || h.protocol === 'ssh') as host}<label class="flex items-center gap-2 type-caption"><input type="checkbox" checked={hosts.includes(host.id)} onchange={(e) => hosts = e.currentTarget.checked ? [...hosts, host.id] : hosts.filter((id) => id !== host.id)} />{host.name}{host.environment === 'production' ? ' · production' : ''}</label>{/each}</div>
      </fieldset>
      <button class="flex items-center gap-2 rounded bg-[var(--color-accent)] px-4 py-2 type-caption text-[var(--color-surface-0)] disabled:opacity-40" disabled={!hosts.length} onclick={review}><Play size="13" />Review run</button>
    </fieldset>
    {#if execution.runID}
      <div class="border-t hairline pt-4">
        <div class="flex items-center gap-3"><h3 class="type-body">{execution.name} — {execution.running ? "running" : "finished"}</h3>{#if execution.running}<button class="flex items-center gap-1 rounded border hairline px-3 py-1 type-caption" onclick={cancel}><Square size="12" />Cancel run</button>{/if}</div>
        {#if execution.error}<p role="alert" class="type-caption text-[var(--color-danger)]">{execution.error}</p>{/if}
        {#each [...execution.results].sort((a,b) => a.stepIndex-b.stepIndex || a.result.hostID.localeCompare(b.result.hostID)) as result (`${result.stepIndex}:${result.result.hostID}`)}
          <details class="mt-2 rounded border hairline surface-2 p-3" open={result.status === 'failed'}>
            <summary class="cursor-pointer type-caption">{result.stepIndex+1}. {result.stepName} · {result.result.hostName || app.hosts.find((h) => h.id === result.result.hostID)?.name || result.result.hostID} · <span class={result.status === 'failed' ? 'text-[var(--color-danger)]' : result.status === 'ok' ? 'text-[var(--color-success)]' : ''}>{result.status}</span></summary>
            <pre class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap font-mono type-caption">{result.result.stdout}{result.result.stderr}{result.result.error}</pre>
          </details>
        {/each}
      </div>
    {/if}
  </div>
</div>

{#if pending}
  <Dialog portal label="Review runbook" onclose={() => pending = null} panelClass="w-[min(95vw,720px)] max-h-[85vh] overflow-auto rounded-xl border hairline surface-2 p-5">
    <h2 class="type-title">Run {pending.book.name}</h2>
    <p class="mt-2 type-caption">Hosts: {pending.hosts.map((id) => app.hosts.find((h) => h.id === id)?.name || id).join(", ")}</p>
    {#if pending.warning}<p class="mt-3 type-caption text-[var(--color-warn)]">{pending.warning}</p>{/if}
    {#each pending.steps as step, i}<div class="mt-3"><p class="type-caption">{i+1}. {step.name}</p><pre class="mt-1 overflow-auto rounded surface-3 p-2 font-mono type-caption">{step.command}</pre></div>{/each}
    {#if pending.phrase}<label class="mt-3 block type-caption">Type <strong>{pending.phrase}</strong> to continue<input class="mt-1 w-full rounded border hairline surface-3 p-2" bind:value={phrase} /></label>{/if}
    <div class="mt-4 flex justify-end gap-2 type-caption"><button class="rounded border hairline px-3 py-2" onclick={() => pending = null}>Cancel</button><button class="rounded bg-[var(--color-accent)] px-3 py-2 text-[var(--color-surface-0)] disabled:opacity-40" disabled={!!pending.phrase && phrase !== pending.phrase} onclick={run}>Run {pending.steps.length} steps</button></div>
  </Dialog>
{/if}
