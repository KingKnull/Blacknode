<script lang="ts">
  import { onDestroy, onMount, tick } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { RecordingService } from "../../bindings/github.com/blacknode/blacknode";
  import type { RecordingDetail } from "../../bindings/github.com/blacknode/blacknode/models";
  import { Play, Pause, RotateCcw, Gauge, X, Loader2 } from "@lucide/svelte";

  type Props = {
    recordingID: string;
    seekToOffset?: number; // optional initial offset (used when jumping from a search hit)
    onClose: () => void;
  };
  let { recordingID, seekToOffset, onClose }: Props = $props();

  let detail = $state<RecordingDetail | null>(null);
  let loading = $state(true);
  let err = $state("");

  let containerEl: HTMLDivElement | undefined = $state();
  let term: Terminal | undefined;
  let fit: FitAddon | undefined;

  let speed = $state(1);
  let playing = $state(false);
  let currentOffset = $state(0); // seconds into the cast
  let totalDuration = $derived(
    detail?.events.length
      ? detail.events[detail.events.length - 1].offset
      : 0,
  );
  let nextEventIdx = 0;
  let raf: number | null = null;
  let wallStart = 0;
  let castStartOffset = 0; // where we started this play burst from

  onMount(async () => {
    try {
      detail = (await RecordingService.Get(
        recordingID,
      )) as unknown as RecordingDetail;
    } catch (e: any) {
      err = String(e?.message ?? e);
      loading = false;
      return;
    }
    loading = false;
    await tick();
    if (!detail) return;

    term = new Terminal({
      fontFamily:
        '"JetBrains Mono Variable", "Cascadia Mono", Menlo, Consolas, monospace',
      fontSize: 12,
      cols: detail.width || 80,
      rows: detail.height || 24,
      cursorBlink: false,
      allowProposedApi: true,
      theme: {
        background: "#08080b",
        foreground: "#ededf3",
        cursor: "#22d3ee",
        cursorAccent: "#08080b",
      },
    });
    fit = new FitAddon();
    term.loadAddon(fit);
    term.open(containerEl!);
    fit.fit();

    if (seekToOffset != null && seekToOffset > 0) {
      seek(seekToOffset);
    }
  });

  onDestroy(() => {
    if (raf) cancelAnimationFrame(raf);
    term?.dispose();
  });

  function play() {
    if (!detail || playing) return;
    playing = true;
    wallStart = performance.now();
    castStartOffset = currentOffset;
    raf = requestAnimationFrame(tick_);
  }

  function pause() {
    playing = false;
    if (raf) {
      cancelAnimationFrame(raf);
      raf = null;
    }
  }

  function tick_() {
    if (!detail || !playing) return;
    const elapsed = ((performance.now() - wallStart) / 1000) * speed;
    const targetOffset = castStartOffset + elapsed;
    while (
      nextEventIdx < detail.events.length &&
      detail.events[nextEventIdx].offset <= targetOffset
    ) {
      const e = detail.events[nextEventIdx++];
      if (e.kind === "o") term?.write(e.data);
    }
    currentOffset = Math.min(targetOffset, totalDuration);
    if (nextEventIdx >= detail.events.length) {
      playing = false;
      raf = null;
      return;
    }
    raf = requestAnimationFrame(tick_);
  }

  function restart() {
    pause();
    term?.reset();
    nextEventIdx = 0;
    currentOffset = 0;
  }

  function seek(offset: number) {
    if (!detail) return;
    pause();
    term?.reset();
    nextEventIdx = 0;
    currentOffset = 0;
    // Replay everything up to `offset` instantly so the visible terminal
    // state matches.
    while (
      nextEventIdx < detail.events.length &&
      detail.events[nextEventIdx].offset <= offset
    ) {
      const e = detail.events[nextEventIdx++];
      if (e.kind === "o") term?.write(e.data);
    }
    currentOffset = offset;
  }

  function fmt(secs: number) {
    if (!isFinite(secs) || secs < 0) return "0:00";
    const m = Math.floor(secs / 60);
    const s = Math.floor(secs % 60);
    return `${m}:${s.toString().padStart(2, "0")}`;
  }

  const SPEEDS = [0.5, 1, 2, 4, 8];
</script>

<div
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm"
  role="presentation"
  onclick={(e) => {
    if (e.target === e.currentTarget) onClose();
  }}
>
  <div
    class="flex max-h-[85vh] w-[min(95vw,1100px)] flex-col overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/60"
  >
    <div class="flex items-center gap-2 border-b hairline px-4 py-2.5">
      <div class="text-sm font-semibold">
        {detail?.title || "Recording"}
        {#if detail}
          <span class="ml-2 font-mono text-[11px] text-[var(--color-text-3)]">
            {fmt(detail.durationSeconds)} · {(detail.sizeBytes / 1024).toFixed(
              1,
            )} KB
          </span>
        {/if}
      </div>
      <button
        class="ml-auto rounded p-1 text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
        onclick={onClose}
      >
        <X size="14" />
      </button>
    </div>

    <div class="flex-1 overflow-auto bg-[var(--color-surface-0)] p-3">
      {#if loading}
        <div class="flex h-32 items-center justify-center text-xs text-[var(--color-text-3)]">
          <Loader2 size="14" class="animate-spin" /> &nbsp;loading…
        </div>
      {:else if err}
        <div class="text-xs text-[var(--color-danger)]">{err}</div>
      {/if}
      <div bind:this={containerEl} class="rounded"></div>
    </div>

    {#if detail}
      <div class="flex items-center gap-3 border-t hairline px-4 py-2 text-xs">
        {#if playing}
          <button
            class="rounded-md border hairline-strong px-2 py-1 hover:bg-[var(--color-surface-3)]"
            onclick={pause}
          >
            <Pause size="12" />
          </button>
        {:else}
          <button
            class="rounded-md bg-[var(--color-accent)] px-2 py-1 text-[var(--color-surface-0)] hover:opacity-90"
            onclick={play}
          >
            <Play size="12" />
          </button>
        {/if}
        <button
          class="rounded-md border hairline-strong px-2 py-1 hover:bg-[var(--color-surface-3)]"
          onclick={restart}
          title="Restart"
        >
          <RotateCcw size="12" />
        </button>

        <div class="flex flex-1 items-center gap-2">
          <span class="font-mono text-[10px] text-[var(--color-text-3)]"
            >{fmt(currentOffset)}</span
          >
          <input
            type="range"
            min="0"
            max={totalDuration || 0}
            step="0.1"
            value={currentOffset}
            class="flex-1 accent-[var(--color-accent)]"
            oninput={(e) =>
              seek(parseFloat((e.target as HTMLInputElement).value))}
          />
          <span class="font-mono text-[10px] text-[var(--color-text-3)]"
            >{fmt(totalDuration)}</span
          >
        </div>

        <div class="flex items-center gap-1">
          <Gauge size="11" class="text-[var(--color-text-3)]" />
          {#each SPEEDS as s (s)}
            <button
              class="rounded px-1.5 py-0.5 text-[10px] {speed === s
                ? 'bg-[var(--color-surface-3)] text-[var(--color-text-1)]'
                : 'text-[var(--color-text-3)] hover:text-[var(--color-text-1)]'}"
              onclick={() => (speed = s)}>{s}×</button
            >
          {/each}
        </div>
      </div>
    {/if}
  </div>
</div>
