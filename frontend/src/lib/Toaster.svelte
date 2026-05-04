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

<div class="pointer-events-none fixed right-4 top-12 z-[60] flex w-[340px] flex-col gap-2">
  {#each toasts as t (t.id)}
    {@const k = kindStyle(t.kind)}
    {@const Icon = k.icon}
    <div
      class="toast-enter pointer-events-auto overflow-hidden rounded-xl border {k.border} {k.bg} shadow-2xl shadow-black/50 backdrop-blur-md"
    >
      <div class="flex items-start gap-3 px-3.5 py-3">
        <Icon size="15" class="mt-0.5 shrink-0 {k.accent}" />
        <div class="min-w-0 flex-1">
          <div class="flex items-start gap-2">
            <span class="flex-1 text-[12px] font-semibold leading-snug text-[var(--color-text-1)]">
              {t.title}
            </span>
            {#if t.source}
              <span class="mt-0.5 shrink-0 rounded border hairline px-1.5 py-px font-mono text-[9px] uppercase tracking-wider text-[var(--color-text-3)]">{t.source}</span>
            {/if}
            <button
              class="-mr-1 -mt-0.5 rounded p-1 text-[var(--color-text-3)] hover:bg-white/5 hover:text-[var(--color-text-1)]"
              onclick={() => dismiss(t.id)}
            >
              <X size="11" />
            </button>
          </div>
          {#if t.body}
            <p class="mt-0.5 text-[11px] leading-snug text-[var(--color-text-2)]">{t.body}</p>
          {/if}
          {#if t.hostName}
            <p class="mt-1 font-mono text-[10px] text-[var(--color-text-3)]">{t.hostName}</p>
          {/if}
        </div>
      </div>
      <!-- Auto-dismiss progress bar -->
      <div class="h-px w-full {k.bar} opacity-30">
        <div class="toast-progress h-full w-full {k.bar} opacity-100"></div>
      </div>
    </div>
  {/each}
</div>

