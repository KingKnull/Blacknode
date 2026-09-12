<script lang="ts">
  import { onMount } from "svelte";
  import { RecordingService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { SessionState } from "../../bindings/github.com/blacknode/blacknode/internal/recorder/models";
  import { app } from "./state.svelte";
  let { sessionID }: { sessionID: string } = $props();
  let capture = $state<SessionState | null>(null);
  let busy = $state(false);
  let unavailable = $state(false);
  onMount(() => {
    let stopped = false;
    let loading = false;
    async function refresh() {
      if (loading) return;
      loading = true;
      try { const next = await RecordingService.SessionState(sessionID); if (!stopped) { capture = next; unavailable = false; } }
      catch { if (!stopped) unavailable = true; }
      finally { loading = false; }
    }
    void refresh();
    const timer = setInterval(refresh, 2000);
    return () => { stopped = true; clearInterval(timer); };
  });
  async function toggle() {
    if (!capture) return;
    busy = true;
    try { await RecordingService.SetPaused(sessionID, !capture.paused); capture = await RecordingService.SessionState(sessionID); unavailable = false; }
    catch (e) { app.toast("error", "Could not change recording state", String(e)); }
    finally { busy = false; }
  }
</script>

{#if capture?.recording || capture?.error || unavailable}
  <button class="shrink-0 rounded border px-1.5 py-px font-mono type-eyebrow disabled:opacity-40 {capture?.paused || unavailable ? 'text-[var(--color-warn)] border-[var(--color-warn)]/30' : 'text-[var(--color-danger)] border-[var(--color-danger)]/30'}"
    disabled={busy || unavailable || !capture?.recording} onclick={toggle}
    title={unavailable ? 'Recording status unavailable' : !capture?.recording ? `Recording did not start: ${capture?.error}. Resolve the problem and open a new session.` : capture?.limitReached ? 'Storage limit reached. Delete recordings or increase the limit, then resume.' : capture?.error || (capture?.paused ? 'Resume recording' : 'Pause recording')}>
    {unavailable ? 'REC ?' : capture?.limitReached ? 'REC LIMIT' : !capture?.recording ? 'REC ERROR' : capture?.paused ? 'REC PAUSED' : '● REC'}
  </button>
{/if}
