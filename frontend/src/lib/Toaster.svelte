<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import type { Notification } from "../../bindings/github.com/blacknode/blacknode/models";
  import {
    CheckCircle2,
    AlertTriangle,
    XCircle,
    X,
    Info,
  } from "@lucide/svelte";

  // The toaster is a fire-and-forget overlay — every backend Notify() emits
  // "notification:toast" and we surface the payload as a small card in the
  // top-right that auto-dismisses after 6s. The desktop notification +
  // webhook are handled backend-side; this is the in-app channel.

  type Toast = Notification & { dismissAt: number };

  let toasts = $state<Toast[]>([]);
  let off: (() => void) | undefined;
  let timer: ReturnType<typeof setInterval> | undefined;

  onMount(() => {
    off = Events.On("notification:toast", (e: any) => {
      const n: Notification = e?.data;
      if (!n) return;
      const t: Toast = { ...n, dismissAt: Date.now() + 6000 };
      // Cap to 6 toasts on screen.
      toasts = [...toasts, t].slice(-6);
    });
    timer = setInterval(() => {
      const now = Date.now();
      const next = toasts.filter((t) => t.dismissAt > now);
      if (next.length !== toasts.length) toasts = next;
    }, 500);
  });

  onDestroy(() => {
    off?.();
    if (timer) clearInterval(timer);
  });

  function dismiss(id: string) {
    toasts = toasts.filter((t) => t.id !== id);
  }

  function iconFor(kind: string) {
    switch (kind) {
      case "ok":
        return CheckCircle2;
      case "warn":
        return AlertTriangle;
      case "error":
        return XCircle;
      default:
        return Info;
    }
  }

  function colorFor(kind: string) {
    switch (kind) {
      case "ok":
        return "border-[var(--color-accent)]/40 bg-[var(--color-accent)]/10 text-[var(--color-accent)]";
      case "warn":
        return "border-[var(--color-warn)]/40 bg-[var(--color-warn)]/10 text-[var(--color-warn)]";
      case "error":
        return "border-[var(--color-danger)]/40 bg-[var(--color-danger)]/10 text-[var(--color-danger)]";
      default:
        return "border-[var(--color-info)]/40 bg-[var(--color-info)]/10 text-[var(--color-info)]";
    }
  }
</script>

<div class="pointer-events-none fixed right-3 top-14 z-[60] flex w-[360px] flex-col gap-2">
  {#each toasts as t (t.id)}
    {@const Icon = iconFor(t.kind)}
    <div
      class="pointer-events-auto rounded-lg border surface-2 shadow-2xl shadow-black/40 backdrop-blur transition-all"
    >
      <div class="flex items-start gap-2 p-3">
        <div
          class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md {colorFor(
            t.kind,
          )}"
        >
          <Icon size="14" />
        </div>
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-2">
            <span class="truncate text-xs font-semibold text-[var(--color-text-1)]">
              {t.title}
            </span>
            {#if t.source}
              <span
                class="ml-auto rounded-sm border hairline px-1.5 py-0.5 text-[9px] font-mono uppercase tracking-wider text-[var(--color-text-3)]"
                >{t.source}</span
              >
            {/if}
          </div>
          {#if t.body}
            <p class="mt-0.5 text-[11px] leading-snug text-[var(--color-text-2)]">
              {t.body}
            </p>
          {/if}
          {#if t.hostName}
            <p class="mt-1 font-mono text-[10px] text-[var(--color-text-3)]">
              {t.hostName}
            </p>
          {/if}
        </div>
        <button
          class="rounded p-0.5 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
          onclick={() => dismiss(t.id)}
        >
          <X size="11" />
        </button>
      </div>
    </div>
  {/each}
</div>

