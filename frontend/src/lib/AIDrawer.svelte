<script lang="ts">
  import { onDestroy, onMount, tick } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { AIService } from "../../bindings/github.com/blacknode/blacknode";
  import { app } from "./state.svelte";
  import {
    Sparkles,
    X,
    Send,
    Wand,
    Brain,
    Square,
    Loader2,
    Settings as SettingsIcon,
  } from "@lucide/svelte";

  type Mode = "translate" | "explain";
  type Msg = {
    role: "user" | "assistant";
    kind: Mode;
    text: string;
    streaming?: boolean;
    error?: string;
  };

  type Props = { onInsertCommand?: (cmd: string) => void };
  let { onInsertCommand }: Props = $props();

  let mode = $state<Mode>("translate");
  let prompt = $state("");
  let busy = $state(false);
  let messages = $state<Msg[]>([]);
  let scrollEl: HTMLDivElement | undefined = $state();
  let activeStreamID = $state<string | null>(null);
  let off: (() => void) | undefined;

  onMount(() => {
    off = Events.On("ai:chunk", (e: any) => {
      const p = e?.data;
      if (!p || p.streamID !== activeStreamID) return;
      const last = messages[messages.length - 1];
      if (!last || last.role !== "assistant") return;
      if (p.error) {
        last.error = p.error;
        last.streaming = false;
      } else if (p.delta) {
        last.text += p.delta;
      }
      if (p.done) {
        last.streaming = false;
        activeStreamID = null;
        busy = false;
      }
      void scrollDown();
    });
  });

  onDestroy(() => off?.());

  async function scrollDown() {
    await tick();
    if (scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
  }

  async function send() {
    const text = prompt.trim();
    if (!text || busy) return;
    if (!app.settings.hasAnthropicKey) {
      app.view = "settings";
      return;
    }

    messages.push({ role: "user", kind: mode, text });
    prompt = "";
    busy = true;

    if (mode === "translate") {
      messages.push({ role: "assistant", kind: "translate", text: "" });
      try {
        const out = await AIService.Translate(text, "bash", "");
        messages[messages.length - 1].text = String(out ?? "");
      } catch (e: any) {
        messages[messages.length - 1].error = String(e?.message ?? e);
      } finally {
        busy = false;
        void scrollDown();
      }
    } else {
      // Explain — streamed
      const id = crypto.randomUUID();
      activeStreamID = id;
      messages.push({ role: "assistant", kind: "explain", text: "", streaming: true });
      try {
        await AIService.Explain(id, text, "");
      } catch (e: any) {
        const last = messages[messages.length - 1];
        last.error = String(e?.message ?? e);
        last.streaming = false;
        activeStreamID = null;
        busy = false;
      }
      void scrollDown();
    }
  }

  async function stop() {
    if (!activeStreamID) return;
    await AIService.Stop(activeStreamID);
    activeStreamID = null;
    busy = false;
    const last = messages[messages.length - 1];
    if (last && last.streaming) last.streaming = false;
  }

  function insertLast() {
    const last = messages[messages.length - 1];
    if (!last || last.role !== "assistant" || last.kind !== "translate") return;
    if (last.text && onInsertCommand) onInsertCommand(last.text);
  }

  function clear() {
    messages = [];
    prompt = "";
  }

  function copy(text: string) {
    navigator.clipboard.writeText(text);
  }
</script>

<div class="flex h-full flex-col surface-1 border-l hairline">
  <div
    class="flex items-center gap-2 border-b hairline px-3 py-2 text-xs"
  >
    <Sparkles size="13" class="text-[var(--color-accent)]" />
    <span class="font-medium">AI assistant</span>
    <div class="ml-2 flex items-center gap-0.5 rounded-md border hairline p-0.5">
      <button
        class="flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] {mode ===
        'translate'
          ? 'bg-[var(--color-surface-3)] text-[var(--color-text-1)]'
          : 'text-[var(--color-text-3)] hover:text-[var(--color-text-1)]'}"
        onclick={() => (mode = "translate")}
        title="Natural language → command"
      >
        <Wand size="10" /> translate
      </button>
      <button
        class="flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] {mode ===
        'explain'
          ? 'bg-[var(--color-surface-3)] text-[var(--color-text-1)]'
          : 'text-[var(--color-text-3)] hover:text-[var(--color-text-1)]'}"
        onclick={() => (mode = "explain")}
        title="Explain pasted output / errors / logs"
      >
        <Brain size="10" /> explain
      </button>
    </div>

    <button
      class="ml-auto rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
      onclick={clear}
      title="Clear"
      disabled={messages.length === 0}>×</button
    >
    <button
      class="rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
      onclick={() => (app.aiOpen = false)}
      title="Close drawer"
    >
      <X size="12" />
    </button>
  </div>

  <div bind:this={scrollEl} class="flex-1 overflow-y-auto px-3 py-2 text-xs">
    {#if !app.settings.hasAnthropicKey}
      <div class="m-auto flex h-full flex-col items-center justify-center gap-2 text-center">
        <Sparkles size="20" class="text-[var(--color-text-4)]" />
        <p class="text-[var(--color-text-3)]">
          Add an Anthropic API key to enable the assistant.
        </p>
        <button
          class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90"
          onclick={() => (app.view = "settings")}
        >
          <SettingsIcon size="11" /> Open settings
        </button>
      </div>
    {:else if messages.length === 0}
      <div class="flex h-full flex-col items-center justify-center gap-2 text-center">
        <Sparkles size="18" class="text-[var(--color-text-4)]" />
        <p class="text-[var(--color-text-3)]">
          {#if mode === "translate"}
            Describe what you want — I'll write the command.
          {:else}
            Paste an error, log line, or output and I'll explain it.
          {/if}
        </p>
      </div>
    {:else}
      <div class="space-y-3">
        {#each messages as m, i (i)}
          {#if m.role === "user"}
            <div class="rounded-md border hairline surface-2 px-3 py-2">
              <div class="text-[9px] uppercase tracking-[0.14em] text-[var(--color-text-4)]">
                you · {m.kind}
              </div>
              <div class="mt-1 whitespace-pre-wrap break-words">{m.text}</div>
            </div>
          {:else}
            <div
              class="rounded-md border border-[var(--color-accent)]/25 bg-[var(--color-accent-soft)] px-3 py-2"
            >
              <div
                class="flex items-center gap-1 text-[9px] uppercase tracking-[0.14em] text-[var(--color-accent)]"
              >
                <Sparkles size="9" /> blacknode · {m.kind}
                {#if m.streaming}
                  <Loader2 size="9" class="animate-spin" />
                {/if}
              </div>
              {#if m.kind === "translate"}
                <pre
                  class="mt-1 overflow-x-auto rounded bg-black/40 px-2 py-1.5 font-mono text-[11px] text-[var(--color-text-1)]">{m.text}</pre>
                {#if m.text}
                  <div class="mt-2 flex gap-1">
                    <button
                      class="rounded-md bg-[var(--color-accent)] px-2 py-0.5 text-[10px] font-medium text-[var(--color-surface-0)] hover:opacity-90"
                      onclick={insertLast}
                      disabled={i !== messages.length - 1 || !onInsertCommand}
                      >insert into terminal</button
                    >
                    <button
                      class="rounded-md border hairline-strong px-2 py-0.5 text-[10px] hover:bg-[var(--color-surface-3)]"
                      onclick={() => copy(m.text)}>copy</button
                    >
                  </div>
                {/if}
              {:else}
                <div class="mt-1 whitespace-pre-wrap break-words leading-relaxed">{m.text}</div>
              {/if}
              {#if m.error}
                <div class="mt-1 text-[10px] text-[var(--color-danger)]">{m.error}</div>
              {/if}
            </div>
          {/if}
        {/each}
      </div>
    {/if}
  </div>

  <div class="border-t hairline px-3 py-2">
    <div class="flex items-end gap-2">
      <textarea
        class="max-h-32 min-h-[36px] flex-1 resize-none rounded-md border hairline bg-[var(--color-surface-3)] px-2 py-1.5 text-xs outline-none"
        rows="1"
        bind:value={prompt}
        placeholder={mode === "translate"
          ? "e.g. find all files larger than 100MB modified this week"
          : "paste error / log / output…"}
        onkeydown={(e) => {
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            send();
          }
        }}
      ></textarea>

      {#if busy && activeStreamID}
        <button
          class="flex items-center gap-1 rounded-md border hairline-strong px-2.5 py-1.5 text-[11px] hover:bg-[var(--color-surface-3)]"
          onclick={stop}
        >
          <Square size="11" /> stop
        </button>
      {:else}
        <button
          class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
          disabled={!prompt.trim() || busy}
          onclick={send}
        >
          {#if busy}<Loader2 size="11" class="animate-spin" />{:else}<Send size="11" />{/if}
        </button>
      {/if}
    </div>
  </div>
</div>
