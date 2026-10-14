<script lang="ts">
  import Dialog from "./Dialog.svelte";
  import { PlatformService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { ChangePlan, ExecResult } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { app } from "./state.svelte";
  import { checkCommand } from "./danger";
  let { plan, onClose, onComplete }: { plan: ChangePlan; onClose: () => void; onComplete?: (result: ExecResult) => void } = $props();
  let busy = $state(false);
  let error = $state("");
  let result = $state<ExecResult | null>(null);
  let phrase = $state("");
  const host = $derived(app.hosts.find((h) => h.id === plan.hostID));
  const danger = $derived(checkCommand(plan.command));
  const required = $derived((host?.environment === "production" || (plan.productionHosts?.length ?? 0)>0) ? "production" : danger?.level === "block-without-confirm" ? "I understand" : "");
  async function close() { if (busy) return; if (!result) await PlatformService.DiscardChange(plan.id); onClose(); }
  async function run() {
    if (busy || (required && phrase !== required)) return;
    busy = true; error = "";
    try { result = await PlatformService.ExecuteChange(plan.id); onComplete?.(result); }
    catch (e) { error = String(e); }
    finally { busy = false; }
  }
</script>
<Dialog portal label="Review change" onclose={close} panelClass="w-[min(95vw,860px)] max-h-[90vh] overflow-auto rounded-xl border hairline surface-2 p-5">
  <div class="ops-form">
    <h2 class="type-title">{plan.title}</h2>
    <p class="type-caption">Target: <strong>{host?.name ?? (plan.hostID || "Local machine")}</strong>{#if host?.environment} · {host.environment}{/if}</p>
    {#if plan.productionHosts?.length}<p class="ops-warning">Production targets: {plan.productionHosts.join(", ")}</p>{/if}
    {#if danger}<p class="ops-warning">{danger.reason}</p>{/if}
    <pre class="ops-code">{plan.command}</pre>
    {#if plan.input}<details open><summary>Content to apply</summary><pre class="ops-code">{plan.input}</pre></details>{/if}
    {#if plan.preview}<details open><summary>Change preview</summary><pre class="ops-code">{plan.preview}</pre></details>{/if}
    {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
    {#if result}<p class={result.exitCode === 0 && !result.error ? 'ops-success' : 'ops-error'}>Exit code: {result.exitCode} · {result.durationMs} ms</p><pre class="ops-code">{result.stdout}{result.stderr}{result.error}</pre>{/if}
    {#if required && !result}<label>Type “{required}” to execute on this target<input bind:value={phrase} autocomplete="off" /></label>{/if}
    <div class="ops-actions"><button onclick={close} disabled={busy}>{result ? "Close" : "Cancel"}</button>{#if !result}<button class="primary" onclick={run} disabled={busy || !!error || (!!required && phrase !== required)}>{busy ? "Executing…" : "Execute reviewed change"}</button>{/if}</div>
  </div>
</Dialog>
