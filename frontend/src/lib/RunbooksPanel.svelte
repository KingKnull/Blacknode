<script lang="ts">
  import { onMount } from "svelte";
  import AutomationHistory from "./AutomationHistory.svelte";
  import { JobService, RunbookService, SnippetService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Runbook, RunbookStep } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import type { OperationDocument, Snippet } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import { checkCommand } from "./danger";
  import PageHeader from "./PageHeader.svelte";
  import Dialog from "./Dialog.svelte";
  import { ListChecks, Plus, Play, Square, Save, Trash2, ArrowUp, ArrowDown } from "@lucide/svelte";

  const blank = (): Runbook => ({ id: "", name: "New runbook", steps: [{ name: "Check host", command: "uname -a", dependsOn: [], rollbackCommand: "" }], timeoutSeconds: 60, stopOnFailure: true });
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
  let activeJobID = $state("");
  let mode = $state<"run" | "schedule">("run");
  let everyMinutes = $state(60);
  let revisions = $state<OperationDocument[]>([]);
  let revisionToView = $state("");
  let pending = $state<{ book: Runbook; steps: RunbookStep[]; hosts: string[]; values: Record<string, string>; phrase: string; warning: string; everySeconds: number } | null>(null);
  const variables = $derived.by(() => {
    const result = new Map<string, string>();
    for (const step of book.steps) for (const match of [step.command, step.rollbackCommand, step.when, step.healthCheck].filter(Boolean).join("\n").matchAll(/\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:\|([^}]*))?\}\}/g)) {
      if (!result.has(match[1])) result.set(match[1], match[2]?.trim() ?? "");
    }
    return [...result].map(([name, fallback]) => ({ name, fallback }));
  });

  onMount(() => {
    void Promise.all([RunbookService.List(), SnippetService.List()]).then(([saved, savedSnippets]) => { books = saved ?? []; snippets = savedSnippets ?? []; }).catch((e) => error = String(e));

  });

  function load() {
    const saved = books.find((b) => b.id === selectedID);
    book = saved ? JSON.parse(JSON.stringify(saved)) : blank();
    values = {}; error = ""; revisions = []; revisionToView = "";
    if (book.id) void JobService.Revisions(book.id).then((items) => revisions = items).catch((e) => error = String(e));
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
      const snapshot = { ...$state.snapshot(book), workspaceID: app.serviceContext?.id ?? book.workspaceID };
      const inputs = Object.fromEntries(variables.filter((v) => v.name in values).map((v) => [v.name, values[v.name]]));
      const steps = await RunbookService.Preview(snapshot, inputs);
      const production = hosts.filter((id) => app.hosts.some((h) => h.id === id && h.environment?.toLowerCase() === "production"));
      const dangers = steps.flatMap((step) => [step.command, step.rollbackCommand, step.when, step.healthCheck].filter((command): command is string => !!command).map(checkCommand)).filter(Boolean);
      const blocked = dangers.some((d) => d?.level === "block-without-confirm");
      phrase = "";
      pending = { book: snapshot, steps, hosts: [...hosts], values: inputs,
        phrase: blocked ? production.length ? "destroy production" : "I understand" : production.length ? "production" : "",
        everySeconds: mode === "schedule" ? everyMinutes * 60 : 0,
        warning: [production.length ? `${production.length} production host(s) selected.` : "", ...dangers.map((d) => d!.reason)].filter(Boolean).join(" "),
      };
    } catch (e) { error = String(e); }
    finally { busy = false; }
  }
  async function run() {
    if (!pending || (pending.phrase && phrase !== pending.phrase)) return;
    const request = pending; pending = null;
    busy = true; error = "";
    try {
      if (request.everySeconds) {
        await JobService.SaveSchedule({ id: "", name: request.book.name, book: request.book, hostIDs: request.hosts, everySeconds: request.everySeconds, nextRunAt: 0, enabled: true, lastJobID: "", lastError: "" }, request.values, 0);
        app.toast("ok", "Schedule created", "It will run while Blacknode or its runner is active and unlocked.");
      } else {
        const job = await JobService.Start(request.book, request.hosts, request.values);
        activeJobID = job.id;
      }
    } catch (e) { error = String(e); }
    finally { busy = false; }
  }

</script>

<div class="flex h-full flex-col">
  <PageHeader icon={ListChecks} title="Runbooks" subtitle="Versioned automation, durable runs and health-gated deployments" />
  <div class="flex-1 overflow-y-auto p-4 space-y-4">
    {#if error}<p role="alert" class="type-caption text-[var(--color-danger)]">{error}</p>{/if}
    <fieldset disabled={busy} class="space-y-4 disabled:opacity-60">
      <div class="flex flex-wrap items-center gap-2 type-caption">
        <select aria-label="Saved runbook" bind:value={selectedID} onchange={load} class="rounded border hairline surface-3 p-2"><option value="">New runbook</option>{#each books as saved}<option value={saved.id}>{saved.name}</option>{/each}</select>
        <input aria-label="Runbook name" maxlength="120" bind:value={book.name} class="min-w-0 flex-1 rounded border hairline surface-3 p-2" />
        <button class="flex items-center gap-1 rounded border hairline px-3 py-2" onclick={save}><Save size="13" />Save</button>
        <button aria-label="Delete runbook" disabled={!book.id} class="rounded border hairline p-2 disabled:opacity-40" onclick={remove}><Trash2 size="13" /></button>
      </div>
      {#if revisions.length}<div class="ops-actions"><label>Saved revisions<select bind:value={revisionToView} onchange={() => {
        const old = revisions.find((item) => String(item.revision) === revisionToView);
        if (old) book = { ...JSON.parse(old.body), revision: books.find((item) => item.id === selectedID)?.revision };
      }}><option value="">Current revision {book.revision}</option>{#each revisions as revision}<option value={String(revision.revision)}>Revision {revision.revision} · {new Date(revision.updatedAt).toLocaleString()}</option>{/each}</select></label><span class="ops-muted">Loading an older revision changes the editor. Save to create a new revision.</span></div>{/if}
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
            <div class="ops-grid ops-form mt-2">
              <label>Condition (exit 1 skips)<input bind:value={step.when} placeholder="test -f /srv/app/ready" /></label>
              <label>Health check after command<input bind:value={step.healthCheck} placeholder="curl --fail http://127.0.0.1:8080/health" /></label>
              <label>Timeout override (seconds)<input type="number" min="0" max="3600" bind:value={step.timeoutSeconds} placeholder="Use runbook timeout" /></label>
              <label class="ops-check"><input type="checkbox" bind:checked={step.retrySafe} onchange={() => { if (!step.retrySafe) step.retries = 0; }} />This command is safe to retry</label>
              <label>Retries<input type="number" min="0" max="5" disabled={!step.retrySafe} bind:value={step.retries} /></label>
            </div>
            <div class="mt-2 grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
              <label class="type-micro text-[var(--color-text-4)]">
                Depends on
                <select
                  multiple
                  aria-label={`Step ${i+1} dependencies`}
                  class="mt-1 h-20 w-full rounded border hairline surface-3 p-1 font-mono type-micro"
                >
                  {#each book.steps.filter((candidate) => candidate.name !== step.name) as candidate (candidate.name)}
                    <option
                      selected={step.dependsOn?.includes(candidate.name)}
                      onclick={() => {
                        const set = new Set(step.dependsOn ?? []);
                        if (set.has(candidate.name)) set.delete(candidate.name);
                        else set.add(candidate.name);
                        step.dependsOn = [...set];
                      }}
                    >{candidate.name}</option>
                  {/each}
                </select>
              </label>
              <label class="type-micro text-[var(--color-text-4)]">
                Rollback command (optional)
                <textarea aria-label={`Step ${i+1} rollback command`} rows="2" maxlength="16384" bind:value={step.rollbackCommand} class="mt-1 w-full rounded border hairline surface-3 p-2 font-mono type-micro"></textarea>
              </label>
            </div>
          </div>
        {/each}
      </div>
      <div class="flex flex-wrap items-center gap-2 type-caption">
        <button class="flex items-center gap-1 rounded border hairline px-3 py-2 disabled:opacity-40" disabled={book.steps.length >= 32} onclick={() => book.steps.push({ name: `Step ${book.steps.length+1}`, command: "", dependsOn: [], rollbackCommand: "" })}><Plus size="13" />Add step</button>
        <select aria-label="Snippet to add" bind:value={snippetID} class="rounded border hairline surface-3 p-2"><option value="">Choose a snippet</option>{#each snippets as snippet}<option value={snippet.id}>{snippet.name}</option>{/each}</select>
        <button class="rounded border hairline px-3 py-2 disabled:opacity-40" disabled={!snippetID || book.steps.length >= 32} onclick={() => { const snippet = snippets.find((s) => s.id === snippetID); if (snippet) book.steps.push({ name: snippet.name, command: snippet.body, dependsOn: [], rollbackCommand: "" }); }}>Add snippet</button>
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
      <div class="ops-grid ops-form">
        <label>Working directory<input bind:value={book.workingDirectory} placeholder="Host default directory" /></label>
        <label>Batch size<input type="number" min="0" max="256" bind:value={book.batchSize} placeholder="16" /></label>
        <label>Canary hosts first<input type="number" min="0" max="256" bind:value={book.canarySize} placeholder="0" /></label>
      </div>
      <p class="ops-muted">Canaries must pass all commands and health checks before later batches start. Steps use fresh shells in the working directory above.</p>
      <fieldset class="rounded border hairline p-3"><legend class="type-caption">Target hosts</legend>
        <div class="flex flex-wrap gap-3">{#each app.scopedHosts.filter((h) => !h.protocol || h.protocol === 'ssh') as host}<label class="flex items-center gap-2 type-caption"><input type="checkbox" checked={hosts.includes(host.id)} onchange={(e) => hosts = e.currentTarget.checked ? [...hosts, host.id] : hosts.filter((id) => id !== host.id)} />{host.name}{host.environment === 'production' ? ' · production' : ''}</label>{/each}</div>
      </fieldset>
      <div class="ops-actions"><label>Execution<select bind:value={mode}><option value="run">Run now</option><option value="schedule">Repeat on a schedule</option></select></label>{#if mode === 'schedule'}<label>Every (minutes)<input type="number" min="1" max="525600" bind:value={everyMinutes} /></label>{/if}</div>
      <button class="flex items-center gap-2 rounded bg-[var(--color-accent)] px-4 py-2 type-caption text-[var(--color-surface-0)] disabled:opacity-40" disabled={!hosts.length} onclick={review}><Play size="13" />{mode === "schedule" ? "Review schedule" : "Review run"}</button>
    </fieldset>
    <AutomationHistory {activeJobID} refreshKey={busy} />
  </div>
</div>

{#if pending}
  <Dialog portal label="Review runbook" onclose={() => pending = null} panelClass="w-[min(95vw,720px)] max-h-[85vh] overflow-auto rounded-xl border hairline surface-2 p-5">
    <h2 class="type-title">{pending.everySeconds ? "Schedule" : "Run"} {pending.book.name}</h2>
    {#if pending.everySeconds}<p class="mt-2 type-caption">Repeats every {pending.everySeconds / 60} minutes. The saved commands and host list stay pinned to this review.</p>{/if}
    <p class="mt-2 type-caption">Hosts: {pending.hosts.map((id) => app.hosts.find((h) => h.id === id)?.name || id).join(", ")}</p>
    {#if pending.warning}<p class="mt-3 type-caption text-[var(--color-warn)]">{pending.warning}</p>{/if}
    {#each pending.steps as step, i}<div class="mt-3"><p class="type-caption">{i+1}. {step.name}{#if step.dependsOn?.length}<span class="ml-2 text-[var(--color-text-4)]">after {step.dependsOn.join(", ")}</span>{/if}</p><pre class="mt-1 overflow-auto rounded surface-3 p-2 font-mono type-caption">{step.command}</pre>{#if step.when}<p class="mt-1 type-micro">Condition: {step.when}</p>{/if}{#if step.healthCheck}<p class="mt-1 type-micro">Health check: {step.healthCheck}</p>{/if}{#if step.rollbackCommand}<p class="mt-1 type-micro text-[var(--color-text-4)]">rollback: {step.rollbackCommand}</p>{/if}</div>{/each}
    {#if pending.phrase}<label class="mt-3 block type-caption">Type <strong>{pending.phrase}</strong> to continue<input class="mt-1 w-full rounded border hairline surface-3 p-2" bind:value={phrase} /></label>{/if}
    <div class="mt-4 flex justify-end gap-2 type-caption"><button class="rounded border hairline px-3 py-2" onclick={() => pending = null}>Cancel</button><button class="rounded bg-[var(--color-accent)] px-3 py-2 text-[var(--color-surface-0)] disabled:opacity-40" disabled={!!pending.phrase && phrase !== pending.phrase} onclick={run}>{pending.everySeconds ? "Create schedule" : `Run ${pending.steps.length} steps`}</button></div>
  </Dialog>
{/if}
