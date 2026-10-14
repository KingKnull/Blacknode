<script lang="ts">
  // Global consumer of the mid-handshake `auth:prompt` event. The backend
  // (AuthPromptService) blocks an SSH handshake waiting on these answers — this
  // is what surfaces keyboard-interactive auth in the UI: TOTP/2FA codes, Duo
  // confirmations, in-handshake password retries, and (the reason this exists)
  // the forced "your password has expired, choose a new one" exchange.
  //
  // On a password-change round it offers a strong suggestion and, once the
  // connection that triggered it succeeds, Terminal.svelte saves the new value
  // to the vault for that host.
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import {
    AuthPromptService,
    HostService,
  } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { AuthPromptRequest } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { app } from "./state.svelte";
  import { classifyFields, newPasswordIndex, isPasswordChange, type FieldRole } from "./authprompt";
  import Dialog from "./Dialog.svelte";
  import { KeyRound, Sparkles } from "@lucide/svelte";

  // Pending prompts, oldest first. Usually one; queued in case two panes connect
  // at once so a second challenge isn't dropped.
  let queue = $state<AuthPromptRequest[]>([]);
  let answers = $state<string[]>([]);
  let genLength = $state(20);
  let genSymbols = $state(true);
  let busy = $state(false);

  const current = $derived<AuthPromptRequest | null>(queue[0] ?? null);
  const roles = $derived<FieldRole[]>(
    current ? classifyFields(current.questions.map((q) => ({ prompt: q.prompt, echo: q.echo }))) : [],
  );
  const newIdx = $derived(newPasswordIndex(roles));
  const isChange = $derived(isPasswordChange(roles));

  // Reset the answer buffer to match whatever prompt is now at the front.
  function syncAnswers() {
    const cur = queue[0] ?? null;
    answers = cur ? cur.questions.map(() => "") : [];
  }

  onMount(() => {
    const offPrompt = Events.On("auth:prompt", (e: any) => {
      const req = e?.data as AuthPromptRequest;
      if (!req) return;
      const wasEmpty = queue.length === 0;
      queue = [...queue, req];
      if (wasEmpty) syncAnswers();
    });
    const offExpired = Events.On("auth:prompt:expired", (e: any) => {
      const id = e?.data as string;
      const wasCurrent = current?.id === id;
      queue = queue.filter((r) => r.id !== id);
      if (wasCurrent) syncAnswers();
    });
    return () => {
      offPrompt();
      offExpired();
    };
  });

  async function suggest() {
    if (newIdx < 0) return;
    busy = true;
    try {
      const pw = await HostService.GeneratePassword({ length: genLength, symbols: genSymbols });
      answers[newIdx] = pw;
      // Mirror into the confirm field(s) so the user isn't retyping a 20-char
      // string by hand.
      roles.forEach((role, i) => {
        if (role === "confirm") answers[i] = pw;
      });
      answers = [...answers];
    } catch (e: any) {
      app.toast("error", "COULD NOT GENERATE", String(e?.message ?? e));
    } finally {
      busy = false;
    }
  }

  async function submit() {
    const req = current;
    if (!req) return;
    // Capture a changed password *before* answering: some servers drop the
    // connection right after the change, and Terminal.svelte saves this once the
    // (re)connection succeeds.
    if (isChange && req.hostId && newIdx >= 0 && answers[newIdx]) {
      app.setPendingNewPassword(req.hostId, answers[newIdx]);
    }
    try {
      await AuthPromptService.Submit(req.id, answers);
    } catch {
      // Unknown id = the handshake already moved on; nothing to surface.
    }
    dequeue(req.id);
  }

  async function cancel() {
    const req = current;
    if (!req) return;
    try {
      await AuthPromptService.Cancel(req.id);
    } catch {
      // ignore
    }
    dequeue(req.id);
  }

  function dequeue(id: string) {
    queue = queue.filter((r) => r.id !== id);
    syncAnswers();
  }

  // Enter submits only from the last field, so multi-field rounds (change,
  // TOTP + password) aren't submitted half-filled.
  function onKey(e: KeyboardEvent, index: number) {
    if (e.key === "Enter" && index === answers.length - 1) submit();
  }
</script>

{#if current}
  <Dialog
    onclose={cancel}
    labelledby="auth-prompt-title"
    closeOnBackdrop={false}
    backdropClass="bg-black/70"
    panelClass="w-96 overflow-hidden border hairline-strong surface-2 shadow-2xl shadow-black/80"
  >
    {#snippet children()}
      <div class="flex items-center gap-2 border-b hairline px-4 py-2.5">
        <KeyRound size="11" class="text-[var(--color-accent)]" />
        <span id="auth-prompt-title" class="font-mono type-eyebrow text-[var(--color-text-1)]">
          {isChange ? "CHANGE PASSWORD" : "AUTHENTICATION"}
        </span>
      </div>
      <div class="p-4">
        <div class="mb-3 font-mono type-micro text-[var(--color-text-3)]">
          {current.user}@{current.host}
          {#if current.attempt && current.attempt > 1}
            <span class="text-[var(--color-warn)]"> · attempt {current.attempt}</span>
          {/if}
        </div>
        <!-- Server's own wording, verbatim: this is where it explains the policy
             ("Password expired") or the challenge ("Enter your 6-digit code"). -->
        {#if current.name}
          <div class="mb-1 font-mono type-caption text-[var(--color-text-2)]">{current.name}</div>
        {/if}
        {#if current.instruction}
          <div class="mb-3 whitespace-pre-wrap font-mono type-micro text-[var(--color-text-3)]">{current.instruction}</div>
        {/if}

        {#each current.questions as q, i (i)}
          <label class="mb-3 block">
            <span class="mb-1 block font-mono type-micro text-[var(--color-text-3)]">{q.prompt}</span>
            <div class="flex items-center gap-2">
              <input
                type={q.echo ? "text" : "password"}
                class="w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-caption outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50"
                bind:value={answers[i]}
                data-autofocus={i === 0 ? true : undefined}
                autocomplete="off"
                aria-label={q.prompt}
                onkeydown={(e) => onKey(e, i)}
              />
              {#if roles[i] === "new"}
                <button
                  type="button"
                  class="flex shrink-0 items-center gap-1 border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 px-2 py-2 font-mono type-eyebrow text-[var(--color-accent)] hover:bg-[var(--color-accent)]/15 disabled:opacity-30 transition-all"
                  disabled={busy}
                  onclick={suggest}
                  title="Suggest a strong password"
                >
                  <Sparkles size="11" /> SUGGEST
                </button>
              {/if}
            </div>
          </label>
        {/each}

        {#if newIdx >= 0}
          <!-- Generator controls, shown only when there's a new-password field. -->
          <div class="mt-1 flex items-center gap-3 font-mono type-micro text-[var(--color-text-4)]">
            <label class="flex items-center gap-1.5">
              length
              <input
                type="number"
                min="8"
                max="128"
                class="w-16 border hairline bg-[var(--color-surface-3)] px-1.5 py-1 text-[var(--color-text-2)] outline-none focus:border-[var(--color-accent)]/50"
                bind:value={genLength}
              />
            </label>
            <label class="flex items-center gap-1.5">
              <input type="checkbox" class="accent-[var(--color-accent)]" bind:checked={genSymbols} />
              symbols
            </label>
            {#if isChange && current.hostId}
              <span class="ml-auto text-[var(--color-text-3)]">saved to vault on connect</span>
            {/if}
          </div>
        {/if}
      </div>
      <div class="flex items-center justify-end gap-2 border-t hairline px-4 py-2.5">
        <button
          class="border border-[var(--color-line)] px-3 py-1.5 font-mono type-eyebrow text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
          onclick={cancel}
        >CANCEL</button>
        <button
          class="border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 px-3 py-1.5 font-mono type-eyebrow text-[var(--color-accent)] hover:bg-[var(--color-accent)]/15 disabled:opacity-30 disabled:cursor-not-allowed transition-all"
          onclick={submit}
        >SUBMIT</button>
      </div>
    {/snippet}
  </Dialog>
{/if}
