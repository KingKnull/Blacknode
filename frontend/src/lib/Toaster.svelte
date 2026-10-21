<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import type { Notification } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import {
    CheckCircle2,
    AlertTriangle,
    XCircle,
    X,
    Info,
  } from "@lucide/svelte";

  type Toast = Notification & { dismissAt: number };

  let toasts = $state<Toast[]>([]);
  let off: (() => void) | undefined;
  let timer: ReturnType<typeof setInterval> | undefined;

  onMount(() => {
    off = Events.On("notification:toast", (e: any) => {
      const n: Notification = e?.data;
      if (!n) return;
      const t: Toast = { ...n, dismissAt: Date.now() + 6000 };
      toasts = [...toasts, t].slice(-5);
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

  type ToastKind = { icon: any; accent: string; bar: string; border: string };

  function kindStyle(kind: string): ToastKind {
    switch (kind) {
      case "ok":
        return {
          icon: CheckCircle2,
          accent: "text-[var(--color-success)]",
          bar: "bg-[var(--color-success)]",
          border: "border-[var(--color-success)]/25",
        };
      case "warn":
        return {
          icon: AlertTriangle,
          accent: "text-[var(--color-warn)]",
          bar: "bg-[var(--color-warn)]",
          border: "border-[var(--color-warn)]/25",
        };
      case "error":
        return {
          icon: XCircle,
          accent: "text-[var(--color-danger)]",
          bar: "bg-[var(--color-danger)]",
          border: "border-[var(--color-danger)]/25",
        };
      default:
        return {
          icon: Info,
          accent: "text-[var(--color-info)]",
          bar: "bg-[var(--color-info)]",
          border: "border-[var(--color-info)]/25",
        };
    }
  }
</script>

<div class="pointer-events-none fixed right-3 top-11 z-[60] flex w-[320px] max-w-[calc(100vw-24px)] flex-col gap-1.5" role="status" aria-live="polite" aria-atomic="false">
  {#each toasts as t (t.id)}
    {@const k = kindStyle(t.kind)}
    {@const Icon = k.icon}
    <div
      class="toast-enter pointer-events-auto overflow-hidden border {k.border} surface-2 shadow-2xl shadow-black/60"
      style="border-radius: var(--radius-sm);"
    >
      <!-- Top accent line -->
      <div class="h-px w-full {k.bar} opacity-60"></div>
      <div class="flex items-start gap-3 px-3 py-2.5">
        <Icon size="16" class="mt-px shrink-0 {k.accent}" />
        <div class="min-w-0 flex-1">
          <div class="flex items-start gap-2">
            <span class="min-w-0 flex-1 break-words type-body font-semibold text-[var(--color-text-1)]">
              {t.title}
            </span>
            {#if t.source}
              <span class="mt-px shrink-0 border border-[var(--color-line-strong)] px-1 font-mono type-eyebrow text-[var(--color-text-4)]" style="border-radius: var(--radius-sm);">{t.source}</span>
            {/if}
            <button
              class="-mr-1 -mt-1 flex h-6 w-6 shrink-0 items-center justify-center text-[var(--color-text-4)] hover:text-[var(--color-danger)] transition-colors"
              onclick={() => dismiss(t.id)}
              aria-label="Dismiss notification"
              title="Dismiss"
            >
              <X size="14" />
            </button>
          </div>
          {#if t.body}
            <p class="mt-1 break-words type-caption text-[var(--color-text-3)]">{t.body}</p>
          {/if}
          {#if t.hostName}
            <p class="mt-1 font-mono type-micro text-[var(--color-text-4)]">{t.hostName}</p>
          {/if}
        </div>
      </div>
      <!-- Auto-dismiss progress bar -->
      <div class="relative h-px w-full bg-[var(--color-surface-3)]">
        <div class="toast-progress absolute inset-0 {k.bar} opacity-50"></div>
      </div>
    </div>
  {/each}
</div>

