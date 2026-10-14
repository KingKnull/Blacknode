<script lang="ts">
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { TransferService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { TransferTask } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import { app } from "./state.svelte";
  let tasks = $state<TransferTask[]>([]);
  onMount(() => {
    let live = true;
    const refresh = () => void TransferService.List().then((items) => { if (live) tasks = items ?? []; }).catch((e) => app.toast("error", "Transfer queue", String(e)));
    refresh();
    const off = Events.On("transfers:changed", (event: any) => {
      const task = event.data as TransferTask;
      tasks = [...tasks.filter((t) => t.id !== task.id), task];
    });
    return () => { live = false; off(); };
  });
  async function act(task: TransferTask, resume: boolean) {
    try { if (resume) await TransferService.Resume(task.id); else await TransferService.Cancel(task.id); }
    catch (e) { app.toast("error", "Transfer", String(e)); }
  }
  const size = (n: number) => n < 0 ? "unknown" : n < 1048576 ? `${(n / 1024).toFixed(1)} KiB` : `${(n / 1048576).toFixed(1)} MiB`;
</script>

{#if tasks.length}
  <details class="max-h-64 shrink-0 overflow-auto border-t hairline surface-1 p-3" open>
    <summary class="cursor-pointer type-caption">Transfers · {tasks.filter((t) => t.status === 'running' || t.status === 'queued').length} active</summary>
    {#each [...tasks].sort((a,b) => b.updatedAt-a.updatedAt).slice(0,30) as task (task.id)}
      <div class="mt-2 grid grid-cols-[1fr_auto] gap-2 rounded border hairline p-2 type-caption">
        <div class="min-w-0"><p class="truncate">{task.request.direction} · {task.request.remotePath}</p><p class="text-[var(--color-text-3)]">{task.status} · {size(task.transferred)} / {size(task.total)}</p>
          {#if task.total > 0}<progress class="mt-1 h-1 w-full accent-[var(--color-accent)]" max={task.total} value={task.transferred} aria-label="Transfer progress"></progress>{/if}
          {#if task.error}<p class="text-[var(--color-warn)]">{task.error}</p>{/if}
        </div>
        {#if task.status === 'running' || task.status === 'queued'}<button class="self-start rounded border hairline px-2 py-1" onclick={() => act(task,false)}>Cancel</button>
        {:else if (task.status === 'paused' || task.status === 'failed') && !task.request.directory}<button class="self-start rounded border hairline px-2 py-1" onclick={() => act(task,true)}>Verify &amp; resume</button>{/if}
      </div>
    {/each}
  </details>
{/if}
