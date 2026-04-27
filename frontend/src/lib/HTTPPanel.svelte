<script lang="ts">
  import { HTTPService } from "../../bindings/github.com/blacknode/blacknode";
  import type { HTTPResponse } from "../../bindings/github.com/blacknode/blacknode/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import {
    Globe2,
    Send,
    Loader2,
    Copy,
    AlertTriangle,
  } from "@lucide/svelte";

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

  let host = $derived(
    app.selectedHostID ? app.hosts.find((h) => h.id === app.selectedHostID) : null,
  );

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
      response = (await HTTPService.Request(host.id, password, {
        method,
        url,
        headers: parseHeaders(headersText),
        body,
        insecureSkipVerify,
      } as any)) as HTTPResponse;
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = false;
    }
  }

  function statusColor(code: number) {
    if (code >= 200 && code < 300) return "text-[var(--color-accent)]";
    if (code >= 300 && code < 400) return "text-[var(--color-info)]";
    if (code >= 400 && code < 500) return "text-[var(--color-warn)]";
    if (code >= 500) return "text-[var(--color-danger)]";
    return "text-[var(--color-text-3)]";
  }

  function copy(text: string) {
    navigator.clipboard.writeText(text);
  }

  function fmtSize(n: number) {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    return `${(n / 1024 / 1024).toFixed(2)} MB`;
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
          class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-4 py-2 text-sm font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
          disabled={busy || !url}
          onclick={send}
        >
          {#if busy}<Loader2 size="14" class="animate-spin" />{:else}<Send size="14" />{/if}
          Send
        </button>
      </div>

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

