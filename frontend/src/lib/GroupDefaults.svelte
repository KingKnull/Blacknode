<script lang="ts">
  // Per-group connection defaults. A host inherits any field it leaves empty,
  // so this is the place to set a username or key once for a whole fleet
  // instead of on every host — see store.ApplyGroupDefaults for precedence.
  import { onMount } from "svelte";
  import { HostGroupService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { GroupSummary } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import type { HostGroup } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import { Users, Loader2, X, ChevronRight } from "@lucide/svelte";

  let summaries = $state<GroupSummary[]>([]);
  let loading = $state(true);
  let expanded = $state<string | null>(null);
  let busy = $state("");
  let err = $state("");

  // The group being edited, as a working copy so cancelling discards edits.
  let draft = $state<HostGroup | null>(null);

  async function load() {
    loading = true;
    try {
      summaries = ((await HostGroupService.List()) ?? []) as GroupSummary[];
      err = "";
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      loading = false;
    }
  }
  onMount(load);

  function open(summary: GroupSummary) {
    if (expanded === summary.group.name) {
      expanded = null;
      draft = null;
      return;
    }
    expanded = summary.group.name;
    draft = {
      ...summary.group,
      envVars: (summary.group.envVars ?? []).map((v) => ({ name: v.name, value: v.value })),
    } as HostGroup;
  }

  const ENV_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
  function envNameError(index: number): string {
    if (!draft?.envVars) return "";
    const entry = draft.envVars[index];
    if (!entry.name) return "";
    if (!ENV_NAME_RE.test(entry.name)) return "Letters, digits and underscores only; cannot start with a digit.";
    if (draft.envVars.some((other, i) => i !== index && other.name === entry.name)) return "Defined twice.";
    return "";
  }
  let envValid = $derived((draft?.envVars ?? []).every((_, i) => envNameError(i) === ""));

  function addEnvVar() {
    if (!draft) return;
    draft.envVars = [...(draft.envVars ?? []), { name: "", value: "" }];
  }
  function removeEnvVar(index: number) {
    if (!draft) return;
    draft.envVars = (draft.envVars ?? []).filter((_, i) => i !== index);
  }

  async function save() {
    if (!draft) return;
    if (!envValid) {
      err = "Fix the highlighted environment variable names first";
      return;
    }
    busy = draft.name;
    err = "";
    try {
      await HostGroupService.Save({
        ...draft,
        // Empty rows are dropped rather than rejected, matching the host editor.
        envVars: (draft.envVars ?? []).filter((v) => v.name.trim() !== ""),
      } as HostGroup);
      await load();
      await app.refreshHosts();
      expanded = null;
      draft = null;
      app.toast("ok", "GROUP DEFAULTS SAVED", "Member hosts inherit these for any field they leave empty.");
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = "";
    }
  }

  async function clearDefaults(name: string) {
    busy = name;
    try {
      await HostGroupService.Delete(name);
      await load();
      await app.refreshHosts();
      expanded = null;
      draft = null;
      app.toast("ok", "GROUP DEFAULTS CLEARED", "Hosts keep their group but stop inheriting.");
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = "";
    }
  }
</script>

<section id="section-groups" class="border hairline-strong surface-2 p-6 shadow-xl" style="backdrop-filter: blur(12px) saturate(1.2);">
  <div class="mb-4 flex items-center gap-2">
    <Users size="14" class="text-[var(--color-accent)]" />
    <h3 class="type-eyebrow text-[var(--color-text-1)]">Group defaults</h3>
    <span class="ml-auto type-micro text-[var(--color-text-4)]">{summaries.length} groups</span>
  </div>
  <p class="mb-4 type-caption text-[var(--color-text-3)] leading-relaxed">
    Set a username, port, key or environment variables once per group. Each host inherits a field
    only when it leaves that field empty, so anything set on the host itself always wins. Agent
    forwarding can only be switched on here — a group never disables forwarding a host asked for.
  </p>

  {#if err}<p role="alert" class="mb-3 type-caption text-[var(--color-danger)]">{err}</p>{/if}

  {#if loading}
    <div class="flex items-center gap-2 type-caption text-[var(--color-text-4)]">
      <Loader2 size="12" class="animate-spin" /> Loading groups...
    </div>
  {:else if summaries.length === 0}
    <p class="type-caption text-[var(--color-text-4)]">
      No groups yet. Assign a group to a host in its editor and it will appear here.
    </p>
  {:else}
    <div class="divide-y hairline">
      {#each summaries as summary (summary.group.name)}
        <div class="py-2">
          <button
            class="flex w-full items-center gap-2 text-left"
            onclick={() => open(summary)}
            aria-expanded={expanded === summary.group.name}
          >
            <ChevronRight
              size="11"
              class="shrink-0 text-[var(--color-text-4)] transition-transform duration-150 {expanded === summary.group.name ? 'rotate-90' : ''}"
            />
            <span class="type-caption font-medium text-[var(--color-text-1)]">{summary.group.name}</span>
            <span class="rounded-full bg-[var(--color-surface-3)] px-1.5 type-micro tabular text-[var(--color-text-4)]">
              {summary.hostCount}
            </span>
            {#if summary.configured}
              <span class="type-micro text-[var(--color-accent)]">defaults set</span>
            {/if}
          </button>

          {#if expanded === summary.group.name && draft}
            <div class="mt-3 grid gap-3 pl-5">
              <div class="grid grid-cols-2 gap-2">
                <label class="block">
                  <span class="type-caption text-[var(--color-text-4)]">Username</span>
                  <input
                    class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-2 py-1.5 type-caption outline-none focus:border-[var(--color-accent)]/50"
                    placeholder="inherit nothing"
                    bind:value={draft.username}
                  />
                </label>
                <label class="block">
                  <span class="type-caption text-[var(--color-text-4)]">Port</span>
                  <input
                    type="number"
                    min="0"
                    max="65535"
                    class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-2 py-1.5 type-caption outline-none focus:border-[var(--color-accent)]/50"
                    bind:value={draft.port}
                  />
                </label>
                <label class="block">
                  <span class="type-caption text-[var(--color-text-4)]">Auth method</span>
                  <select
                    class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-2 py-1.5 type-caption outline-none focus:border-[var(--color-accent)]/50"
                    bind:value={draft.authMethod}
                  >
                    <option value="">— no default —</option>
                    <option value="password">password</option>
                    <option value="key">key</option>
                    <option value="agent">agent</option>
                  </select>
                </label>
                <label class="block">
                  <span class="type-caption text-[var(--color-text-4)]">Key</span>
                  <select
                    class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-2 py-1.5 type-caption outline-none focus:border-[var(--color-accent)]/50"
                    bind:value={draft.keyID}
                  >
                    <option value="">— no default —</option>
                    {#each app.keys as k (k.id)}
                      <option value={k.id}>{k.name}{k.hardware ? " (hardware)" : ""}</option>
                    {/each}
                  </select>
                </label>
                <label class="block">
                  <span class="type-caption text-[var(--color-text-4)]">ProxyJump (bastion)</span>
                  <select
                    class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-2 py-1.5 type-caption outline-none focus:border-[var(--color-accent)]/50"
                    bind:value={draft.proxyJump}
                  >
                    <option value="">— direct connect —</option>
                    {#each app.hosts as h (h.id)}
                      <option value={h.name}>{h.name}</option>
                    {/each}
                  </select>
                </label>
                <label class="flex items-end gap-2 pb-1.5">
                  <input type="checkbox" class="h-3 w-3 border hairline bg-[var(--color-surface-3)]" bind:checked={draft.forwardAgent} />
                  <span class="type-caption text-[var(--color-text-3)]">Forward SSH agent</span>
                </label>
              </div>

              <div>
                <span class="type-caption text-[var(--color-text-4)]">Environment variables</span>
                {#each draft.envVars ?? [] as entry, i (i)}
                  {@const nameError = envNameError(i)}
                  <div class="mt-1.5 flex items-start gap-1.5">
                    <input
                      class="w-2/5 border hairline bg-[var(--color-surface-3)] px-2 py-1.5 font-mono type-caption outline-none transition-colors {nameError ? 'border-[var(--color-danger)]/60' : 'focus:border-[var(--color-accent)]/50'}"
                      placeholder="NAME"
                      aria-label="Group variable {i + 1} name"
                      bind:value={entry.name}
                    />
                    <input
                      class="min-w-0 flex-1 border hairline bg-[var(--color-surface-3)] px-2 py-1.5 font-mono type-caption outline-none focus:border-[var(--color-accent)]/50"
                      placeholder="value"
                      aria-label="Group variable {i + 1} value"
                      bind:value={entry.value}
                    />
                    <button
                      class="shrink-0 border hairline px-2 py-1.5 text-[var(--color-text-3)] transition-colors hover:border-[var(--color-danger)]/40 hover:text-[var(--color-danger)]"
                      onclick={() => removeEnvVar(i)}
                      aria-label="Remove group variable {i + 1}"
                    ><X size="11" /></button>
                  </div>
                  {#if nameError}<p role="alert" class="mt-1 type-caption text-[var(--color-danger)]">{nameError}</p>{/if}
                {/each}
                <button
                  class="mt-2 border hairline px-3 py-1.5 type-caption text-[var(--color-text-2)] hover:border-[var(--color-accent)]/40 hover:text-[var(--color-accent)] disabled:opacity-40"
                  onclick={addEnvVar}
                  disabled={(draft.envVars ?? []).length >= 64}
                >+ Add variable</button>
                <p class="mt-1 type-caption text-[var(--color-text-4)]">
                  Merged with each host's own, group values first so a host value can reference them.
                  A host redefining a name replaces the group's.
                </p>
              </div>

              <div class="flex items-center gap-2">
                <button
                  class="flex items-center gap-1 bg-[var(--color-accent)] px-3 py-1.5 type-caption font-medium text-[var(--color-surface-0)] disabled:opacity-40"
                  disabled={busy === draft.name}
                  onclick={save}
                >
                  {#if busy === draft.name}<Loader2 size="11" class="animate-spin" />{/if}Save defaults
                </button>
                {#if summary.configured}
                  <button
                    class="border hairline-strong px-3 py-1.5 type-caption text-[var(--color-danger)] hover:bg-[var(--color-danger)]/10"
                    onclick={() => clearDefaults(summary.group.name)}
                  >Clear</button>
                {/if}
                <span class="type-caption text-[var(--color-text-4)]">
                  Applies to {summary.hostCount} host{summary.hostCount === 1 ? "" : "s"}
                </span>
              </div>
            </div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</section>
