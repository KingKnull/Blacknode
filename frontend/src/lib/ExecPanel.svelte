<script lang="ts">
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { ExecService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { ExecResult } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { app } from "./state.svelte";
  import { bus } from "./events";
  import PageHeader from "./PageHeader.svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";
  import { isReadOnlyHost, policyBlock, summarizePolicyHosts } from "./commandPolicy";
  import { checkCommand, anyProduction } from "./danger";
  import { envBadge } from "./envColor";
  import {
    Zap,
    Play,
    Loader2,
    Check,
    AlertTriangle,
    Server,
    Columns,
  } from "@lucide/svelte";

  let command = $state("uname -a");
  let selected = $state<Set<string>>(new Set());
  let running = $state(false);
  let runID = $state("");
  let results = $state<Record<string, ExecResult>>({});

  type Confirmation = {
    title: string;
    body: string;
    severity: "warn" | "block-without-confirm";
    requirePhrase?: string;
    productionHosts: string[];
    readOnlyHosts: string[];
  };
  let pendingConfirm: Confirmation | null = $state(null);
  let commandInput: HTMLInputElement | undefined = $state();

  onMount(() => {
    return Events.On("exec:progress", (e: any) => {
      const p = e?.data;
      if (!p || p.runID !== runID) return;
      results[p.result.hostID] = p.result;
    });
  });

  function toggle(id: string) {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    selected = next;
  }

  function selectAll() {
    selected = new Set(app.hosts.map((h) => h.id));
  }
  function selectNone() {
    selected = new Set();
  }

  function selectedHosts() {
    return [...selected]
      .map((id) => app.hosts.find((h) => h.id === id))
      .filter((h): h is NonNullable<typeof h> => !!h);
  }

  let selectedPolicy = $derived(summarizePolicyHosts(selectedHosts()));

  function buildConfirmation(): Confirmation | null {
    const hosts = selectedHosts();
    const { production: prodNames, readOnly: readOnlyNames } = summarizePolicyHosts(hosts);
    const hasProd = anyProduction(hosts.map((h) => h.environment));
    const danger = checkCommand(command);
    const readOnlyNote = readOnlyNames.length
      ? ` Read-only hosts are included; mutating commands will be blocked by host policy.`
      : "";

    if (danger && danger.level === "block-without-confirm") {
      return {
        title: `Dangerous command — ${danger.reason}`,
        body: `The command matches a known destructive pattern (“${danger.matched}”) and will run on ${selected.size} host${selected.size === 1 ? "" : "s"}. Type the phrase below if you really mean this.${readOnlyNote}`,
        severity: "block-without-confirm",
        requirePhrase: hasProd ? "destroy production" : "I understand",
        productionHosts: prodNames,
        readOnlyHosts: readOnlyNames,
      };
    }
    if (danger) {
      return {
        title: `Risky command — ${danger.reason}`,
        body: `The command matches the pattern “${danger.matched}”. Confirm before running on ${selected.size} host${selected.size === 1 ? "" : "s"}.${readOnlyNote}`,
        severity: "warn",
        productionHosts: prodNames,
        readOnlyHosts: readOnlyNames,
      };
    }
    if (hasProd) {
      return {
        title: "Production hosts in scope",
        body: `${prodNames.length} of the selected hosts are tagged production. Confirm you want to run “${command}” against them.${readOnlyNote}`,
        severity: "warn",
        productionHosts: prodNames,
        readOnlyHosts: readOnlyNames,
      };
    }
    return null;
  }

  async function run() {
    if (!command || selected.size === 0) return;
    const confirm = buildConfirmation();
    if (confirm) {
      pendingConfirm = confirm;
      return;
    }
    await actuallyRun();
  }

  async function actuallyRun(approved = false) {
    pendingConfirm = null;
    running = true;
    runID = crypto.randomUUID();
    results = {};
    try {
      const completed = approved
        ? await ExecService.RunApproved(runID, command, [...selected], 60)
        : await ExecService.Run(runID, command, [...selected], 60);
      for (const result of completed ?? []) results[result.hostID] = result;
    } finally {
      running = false;
    }
  }

  let resultList = $derived(
    [...selected].map((id) => ({
      host: app.hosts.find((h) => h.id === id),
      r: results[id],
    })),
  );

  let policyBlockedResults = $derived(
    resultList.filter((item) => item.r?.error && policyBlock(item.r.error)),
  );

  function removeReadOnlyHosts() {
    selected = new Set([...selected].filter((id) => {
      const host = app.hosts.find((candidate) => candidate.id === id);
      return !host || !isReadOnlyHost(host);
    }));
  }

  // ── Compare / diff mode ───────────────────────────────────────────
  let compareMode = $state(false);
  let referenceHostID = $state<string | null>(null);

  // Finished results for comparison
  let finishedResults = $derived(
    resultList.filter((item) => item.r && !item.r.error && item.host),
  );

  // Compute line-level diff. Returns lines with a `same` flag.
  function diffLines(refOutput: string, targetOutput: string): { text: string; same: boolean }[] {
    const refLines = refOutput.split('\n');
    const targetLines = targetOutput.split('\n');
    const maxLen = Math.max(refLines.length, targetLines.length);
    const result: { text: string; same: boolean }[] = [];
    for (let i = 0; i < maxLen; i++) {
      const tLine = targetLines[i] ?? '';
      const rLine = refLines[i] ?? '';
      result.push({ text: tLine, same: tLine === rLine });
    }
    return result;
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Zap}
    title="Multi-host run"
    subtitle="Execute one command across many hosts in parallel"
  />

  <div class="border-b hairline surface-1 px-4 py-3">
    <div class="mb-2 flex items-center gap-2 type-caption text-[var(--color-text-3)]">
      <span class="font-mono"
        >{selected.size} <span class="text-[var(--color-text-4)]">/</span>
        {app.hosts.length}</span
      >
      <span>selected</span>
      <button
        class="rounded px-1.5 py-0.5 hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={selectAll}>all</button
      >
      <button
        class="rounded px-1.5 py-0.5 hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={selectNone}>none</button
      >
    </div>
    <div class="flex items-stretch gap-2">
      <input
        class="flex-1 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-body outline-none focus:border-[var(--color-accent)]/50 focus:shadow-[0_0_12px_rgba(59,130,246,0.06)] transition-all"
        bind:value={command}
        bind:this={commandInput}
        placeholder="command to run on every selected host"
        aria-label="Command to run on selected hosts"
        onkeydown={(e) => e.key === "Enter" && run()}
      />
      <button
        class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-4 py-2 type-body font-medium text-[var(--color-surface-0)] hover:opacity-90 hover:shadow-[0_0_20px_rgba(59,130,246,0.15)] disabled:opacity-50 transition-all"
        onclick={run}
        disabled={running || !command || selected.size === 0}
      >
        {#if running}
          <Loader2 size="14" class="animate-spin" />Running…
        {:else}
          <Play size="14" />Run
        {/if}
      </button>
      {#if finishedResults.length >= 2}
        <button
          class="flex items-center gap-1.5 rounded-md border px-3 py-2 type-body font-medium transition-all {compareMode ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 text-[var(--color-accent)]' : 'border-[var(--color-line)] text-[var(--color-text-3)] hover:border-[var(--color-accent)]/30 hover:text-[var(--color-accent)]'}"
          onclick={() => { compareMode = !compareMode; if (compareMode && !referenceHostID && finishedResults[0]?.host) referenceHostID = finishedResults[0].host.id; }}
          title="Compare output across hosts"
        >
          <Columns size="14" />{compareMode ? 'List' : 'Compare'}
        </button>
      {/if}
    </div>
    {#if selectedPolicy.readOnly.length > 0}
      <div class="mt-2 flex flex-wrap items-center gap-2 rounded border border-[var(--color-accent)]/25 bg-[var(--color-accent)]/6 px-3 py-2 type-caption text-[var(--color-text-2)]" role="status">
        <span class="font-semibold text-[var(--color-accent)]">Read-only scope:</span>
        <span>{selectedPolicy.readOnly.join(", ")}</span>
        <span class="text-[var(--color-text-4)]">Inspection commands only; policy blocks mutations.</span>
        <button class="ml-auto rounded border border-[var(--color-accent)]/35 px-2 py-1 type-micro text-[var(--color-accent)] hover:bg-[var(--color-accent)]/10" onclick={removeReadOnlyHosts}>Remove from selection</button>
      </div>
    {/if}
    {#if policyBlockedResults.length > 0}
      <div class="mt-2 rounded border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/8 px-3 py-2 type-caption" role="alert">
        <div class="font-semibold text-[var(--color-danger)]">Command blocked by host policy</div>
        <div class="mt-1 text-[var(--color-text-2)]">{policyBlockedResults.map((item) => `${item.host?.name ?? item.r?.hostID}: ${policyBlock(item.r?.error)?.reason ?? item.r?.error}`).join(" · ")}</div>
        <div class="mt-1 text-[var(--color-text-3)]">{policyBlock(policyBlockedResults[0]?.r?.error)?.action}</div>
        {#if policyBlockedResults.some((item) => policyBlock(item.r?.error)?.reason.toLowerCase().includes("production host"))}
          <button class="mt-2 rounded border border-[var(--color-danger)]/35 px-2 py-1 type-micro text-[var(--color-danger)] hover:bg-[var(--color-danger)]/10" onclick={() => commandInput?.focus()}>Review command and retry</button>
        {/if}
      </div>
    {/if}
  </div>

  <div class="grid h-full grid-cols-[260px_1fr] overflow-hidden">
    <div class="overflow-y-auto border-r hairline">
      {#each app.scopedHosts as h (h.id)}
        {@const env = envBadge(h.environment)}
        {@const readOnly = isReadOnlyHost(h)}
        <label
          class="flex cursor-pointer items-center gap-2.5 border-b hairline px-3 py-2 type-caption transition-colors hover:bg-[var(--color-surface-2)]"
        >
          <input
            type="checkbox"
            class="accent-[var(--color-accent)]"
            checked={selected.has(h.id)}
            onchange={() => toggle(h.id)}
          />
          <Server size="12" class="text-[var(--color-text-3)]" />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-1.5 truncate text-[var(--color-text-1)]">
              <span class="truncate">{h.name}</span>
              {#if env.label}
                <span
                  class="shrink-0 rounded-sm px-1 type-nano font-mono font-semibold"
                  style:color={env.color}
                  style:background={env.bg}
                  style:border="1px solid {env.border}"
                  >{env.label}</span
                >
              {/if}
              {#if readOnly}
                <span class="shrink-0 rounded-sm border border-[var(--color-accent)]/30 bg-[var(--color-accent)]/8 px-1 type-nano font-mono font-semibold text-[var(--color-accent)]" title="Read-only host — inspection commands only">RO</span>
              {/if}
            </div>
            <div class="truncate type-micro text-[var(--color-text-3)]">
              {h.username}@{h.host}
            </div>
          </div>
        </label>
      {/each}
      {#if app.hosts.length === 0}
        <div class="space-y-2 p-4 text-center type-caption text-[var(--color-text-3)]">
          <p>No hosts to run on.</p>
          <button
            class="rounded-md bg-[var(--color-accent)] px-3 py-1.5 type-caption font-medium text-[var(--color-surface-0)] hover:opacity-90 transition-opacity"
            onclick={() => bus.emit("new-host")}
          >+ Add a host</button>
        </div>
      {/if}
    </div>

    <div class="overflow-y-auto">
      {#if compareMode && finishedResults.length >= 2}
        <!-- Reference host picker -->
        <div class="flex items-center gap-2 border-b hairline surface-1 px-4 py-2 type-caption">
          <span class="text-[var(--color-text-3)]">Reference:</span>
          <select
            aria-label="Reference host for comparison"
            class="rounded border hairline bg-[var(--color-surface-3)] px-2 py-1 font-mono type-micro text-[var(--color-text-1)] outline-none"
            onchange={(e) => referenceHostID = (e.target as HTMLSelectElement).value}
          >
            {#each finishedResults as item}
              {#if item.host}
                <option value={item.host.id} selected={item.host.id === referenceHostID}>{item.host.name}</option>
              {/if}
            {/each}
          </select>
        </div>
        <!-- Side-by-side diff grid -->
        {@const refResult = finishedResults.find((i) => i.host?.id === referenceHostID)}
        <div class="grid overflow-x-auto" style:grid-template-columns="repeat({finishedResults.length}, minmax(300px, 1fr))">
          {#each finishedResults as item}
            {#if item.host && item.r}
              <div class="border-r hairline">
                <div class="flex items-center gap-2 surface-1 px-3 py-1.5 type-micro font-mono border-b hairline">
                  <Server size="10" class="text-[var(--color-text-3)]" />
                  <span class="text-[var(--color-text-1)] font-medium">{item.host.name}</span>
                  {#if item.host.id === referenceHostID}
                    <span class="rounded border border-[var(--color-accent)]/30 bg-[var(--color-accent)]/10 px-1 type-nano text-[var(--color-accent)]">REF</span>
                  {/if}
                  {#if item.r.exitCode === 0}
                    <Check size="10" class="ml-auto text-[var(--color-accent)]" />
                  {:else}
                    <AlertTriangle size="10" class="ml-auto text-[var(--color-warn)]" />
                  {/if}
                </div>
                <pre class="overflow-x-auto px-3 py-2 font-mono type-micro leading-relaxed">{#if refResult && item.host.id !== referenceHostID}{#each diffLines(refResult.r?.stdout ?? '', item.r.stdout ?? '') as line}<span class="{line.same ? 'text-[var(--color-text-4)]' : 'text-[var(--color-text-1)] bg-[var(--color-accent)]/8'}">{line.text}
</span>{/each}{:else}{item.r.stdout ?? ''}{/if}</pre>
              </div>
            {/if}
          {/each}
        </div>
      {:else}
      {#each resultList as item (item.host?.id)}
        {#if item.host}
          {@const policy = item.r?.error ? policyBlock(item.r.error) : null}
          <div class="border-b hairline">
            <div class="flex items-center gap-2 surface-1 px-4 py-2 type-caption">
              <Server size="11" class="text-[var(--color-text-3)]" />
              <span class="font-mono text-[var(--color-text-1)]"
                >{item.host.name}</span
              >
              {#if item.r}
                {#if policy}
                  <span class="flex items-center gap-1 text-[var(--color-danger)]" title={policy.action}>
                    <AlertTriangle size="11" />blocked by policy
                  </span>
                {:else if item.r.error}
                  <span class="flex items-center gap-1 text-[var(--color-danger)]">
                    <AlertTriangle size="11" />error: {item.r.error}
                  </span>
                {:else if item.r.exitCode === 0}
                  <span class="flex items-center gap-1 text-[var(--color-accent)]">
                    <Check size="11" /> exit 0
                  </span>
                {:else}
                  <span class="flex items-center gap-1 text-[var(--color-warn)]">
                    <AlertTriangle size="11" /> exit {item.r.exitCode}
                  </span>
                {/if}
                <span class="ml-auto font-mono text-[var(--color-text-4)]"
                  >{item.r.durationMs}ms</span
                >
              {:else if running}
                <span class="flex items-center gap-1 text-[var(--color-text-3)]">
                  <Loader2 size="11" class="animate-spin" /> running…
                </span>
              {:else}
                <span class="text-[var(--color-text-4)]">pending</span>
              {/if}
            </div>
            {#if item.r}
              {#if item.r.stdout}
                <pre
                  class="overflow-x-auto bg-[var(--color-code-bg)] px-4 py-2 font-mono type-caption text-[var(--color-text-1)]">{item.r.stdout}</pre>
              {/if}
              {#if item.r.stderr}
                <pre
                  class="overflow-x-auto bg-[var(--color-danger)]/10 px-4 py-2 font-mono type-caption text-[var(--color-danger)]/90">{item.r.stderr}</pre>
              {/if}
            {/if}
          </div>
        {/if}
      {/each}
      {#if resultList.length === 0}
        <div class="flex h-full items-center justify-center">
          <div class="text-center">
            <Zap size="20" class="mx-auto text-[var(--color-text-4)]" />
            <p class="mt-2 type-caption text-[var(--color-text-3)]">
              {app.hosts.length === 0
                ? "Add a host first — then pick hosts here, type a command, and hit Run."
                : "Pick hosts on the left, type a command, hit Run."}
            </p>
          </div>
        </div>
      {/if}
      {/if}
    </div>
  </div>

  {#if pendingConfirm}
    <ConfirmDanger
      title={pendingConfirm.title}
      body={pendingConfirm.body}
      severity={pendingConfirm.severity}
      productionHosts={pendingConfirm.productionHosts}
      readOnlyHosts={pendingConfirm.readOnlyHosts}
      requirePhrase={pendingConfirm.requirePhrase}
      onCancel={() => (pendingConfirm = null)}
      onConfirm={() => actuallyRun(true)}
    />
  {/if}
</div>
