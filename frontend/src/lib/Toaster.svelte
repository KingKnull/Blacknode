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

  type ToastKind = { icon: any; accent: string; bar: string; bg: string; border: string };

  function kindStyle(kind: string): ToastKind {
    switch (kind) {
      case "ok":
        return {
          icon: CheckCircle2,
          accent: "text-[var(--color-success)]",
          bar: "bg-[var(--color-success)]",
          bg: "bg-[var(--color-success)]/8",
          border: "border-[var(--color-success)]/25",
        };
      case "warn":
        return {
          icon: AlertTriangle,
          accent: "text-[var(--color-warn)]",
          bar: "bg-[var(--color-warn)]",
          bg: "bg-[var(--color-warn)]/8",
          border: "border-[var(--color-warn)]/25",
        };
      case "error":
        return {
          icon: XCircle,
          accent: "text-[var(--color-danger)]",
          bar: "bg-[var(--color-danger)]",
          bg: "bg-[var(--color-danger)]/8",
          border: "border-[var(--color-danger)]/25",
        };
      default:
        return {
          icon: Info,
          accent: "text-[var(--color-info)]",
          bar: "bg-[var(--color-info)]",
          bg: "bg-[var(--color-info)]/8",
          border: "border-[var(--color-info)]/25",
        };
    }
  }
</script>

<div class="pointer-events-none fixed right-3 top-11 z-[60] flex w-[320px] flex-col gap-1.5" role="status" aria-live="polite" aria-atomic="false">
  {#each toasts as t (t.id)}
    {@const k = kindStyle(t.kind)}
    {@const Icon = k.icon}
    <div
      class="toast-enter pointer-events-auto overflow-hidden border {k.border} {k.bg} shadow-2xl shadow-black/60"
    >
      <!-- Top accent line -->
      <div class="h-px w-full {k.bar} opacity-60"></div>
      <div class="flex items-start gap-3 px-3 py-2.5">
        <Icon size="12" class="mt-px shrink-0 {k.accent}" />
        <div class="min-w-0 flex-1">
          <div class="flex items-start gap-2">
            <span class="flex-1 font-mono text-[10px] font-bold uppercase tracking-wider text-[var(--color-text-1)] leading-snug">
              {t.title}
            </span>
            {#if t.source}
              <span class="mt-px shrink-0 border border-[var(--color-line-strong)] px-1 font-mono text-[8px] uppercase tracking-widest text-[var(--color-text-4)]">{t.source}</span>
            {/if}
            <button
              class="-mr-0.5 -mt-0.5 p-0.5 text-[var(--color-text-4)] hover:text-[var(--color-danger)] transition-colors"
              onclick={() => dismiss(t.id)}
              aria-label="Dismiss notification"
              title="Dismiss"
            >
              <X size="10" />
            </button>
          </div>
          {#if t.body}
            <p class="mt-0.5 text-xs leading-snug text-[var(--color-text-3)]">{t.body}</p>
          {/if}
          {#if t.hostName}
            <p class="mt-1 font-mono text-[10px] text-[var(--color-text-4)]">{t.hostName}</p>
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

