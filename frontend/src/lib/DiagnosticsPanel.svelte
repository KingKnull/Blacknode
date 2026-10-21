<script lang="ts">
  import { DiagnosticService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import { downloadText } from "./operationRecords";
  import { saveDiagnosticPreview, validateDiagnosticPreview } from "./diagnosticDownload";
  import Dialog from "./Dialog.svelte";
  import { Download, Loader2, ShieldCheck } from "@lucide/svelte";

  let busy = $state(false);
  let preview = $state("");
  let reviewed = $state(false);
  let error = $state("");
  let downloadRequested = $state(false);
  const previewID = $props.id();

  async function generatePreview() {
    busy = true;
    error = "";
    preview = "";
    reviewed = false;
    downloadRequested = false;
    try {
      const text = await DiagnosticService.Preview();
      validateDiagnosticPreview(text);
      preview = text;
    } catch {
      // Bridge errors can contain local paths; do not echo them into the UI.
      error = "Could not generate the local diagnostic preview. No file was saved.";
    } finally {
      busy = false;
    }
  }

  function savePreview() {
    if (!reviewed || !preview) return;
    try {
      if (!saveDiagnosticPreview(preview, reviewed, downloadText)) return;
      preview = "";
      reviewed = false;
      downloadRequested = true;
    } catch {
      error = "Could not start the JSON download. You can retry from the preview.";
    }
  }
</script>

<section id="section-diagnostics" class="border hairline-strong surface-2 p-6">
  <div class="mb-4 flex items-center gap-2">
    <ShieldCheck size="14" class="text-[var(--color-accent)]" />
    <h3 class="type-eyebrow text-[var(--color-text-1)]">Local diagnostics</h3>
  </div>
  <p class="type-caption text-[var(--color-text-3)] leading-relaxed">
    Export a redacted, allowlisted JSON bundle for support: app version, OS/architecture,
    compiler and Go runtime version, and Linux GTK/WebKit package-version availability.
    Maximum 8 KiB. Package checks are local and time-limited; they describe development
    metadata, not the loaded runtime.
  </p>
  <p class="mt-2 type-caption text-[var(--color-text-3)] leading-relaxed">
    No logs, configuration, environment values, credentials, host names or private file paths
    are collected. No network access, installation or repair. Review the complete JSON before saving.
  </p>
  <button
    class="mt-4 flex items-center gap-2 border hairline-strong px-3 py-2 type-caption disabled:opacity-50 hover:bg-[var(--color-surface-3)]"
    disabled={busy}
    onclick={generatePreview}
  >
    {#if busy}<Loader2 size="12" class="animate-spin" />{:else}<Download size="12" />{/if}
    {busy ? "GENERATING LOCAL PREVIEW…" : "PREVIEW DIAGNOSTIC BUNDLE"}
  </button>
  {#if error}<p role="alert" class="mt-3 type-caption text-[var(--color-danger)]">{error}</p>{/if}
  {#if downloadRequested}<p role="status" class="mt-3 type-caption text-[var(--color-text-3)]">JSON download requested. Your browser or desktop controls where it is saved.</p>{/if}
</section>

{#if preview}
  <Dialog
    portal
    labelledby={previewID}
    onclose={() => { preview = ""; reviewed = false; }}
    panelClass="w-[720px] max-h-[90vh] overflow-y-auto border hairline-strong surface-2 p-6"
  >
    {#snippet children()}
      <h3 id={previewID} class="type-eyebrow">Review diagnostic JSON</h3>
      <p class="mt-2 type-caption text-[var(--color-text-3)]">
        This is the complete file ({new TextEncoder().encode(preview).length} bytes / 8192 maximum).
        Missing package metadata and probe failures are explicitly recorded. Nothing has been saved or uploaded.
      </p>
      <pre class="my-4 max-h-[50vh] overflow-auto whitespace-pre-wrap break-all border hairline surface-3 p-3 type-caption" aria-label="Complete diagnostic JSON preview">{preview}</pre>
      <label class="flex items-center gap-2 type-caption">
        <input type="checkbox" bind:checked={reviewed} />
        I reviewed this JSON and want to save it locally.
      </label>
      {#if error}<p role="alert" class="mt-3 type-caption text-[var(--color-danger)]">{error}</p>{/if}
      <div class="mt-4 flex justify-end gap-2">
        <button class="border hairline-strong px-3 py-2 type-caption" data-autofocus onclick={() => { preview = ""; reviewed = false; }}>Cancel</button>
        <button class="border hairline-strong px-3 py-2 type-caption disabled:opacity-50" disabled={!reviewed} onclick={savePreview}>Confirm &amp; download JSON</button>
      </div>
    {/snippet}
  </Dialog>
{/if}
