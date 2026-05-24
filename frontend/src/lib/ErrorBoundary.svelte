<script lang="ts">
  import { onMount } from "svelte";
  import { AlertTriangle, RotateCcw } from "@lucide/svelte";

  type Props = {
    name: string;
    children: any;
  };
  let { name, children }: Props = $props();

  let error = $state<string | null>(null);
  let key = $state(0);

  // Catch unhandled errors from the child tree. The onerror handler on the
  // wrapper div catches errors that bubble up from child component renders.
  function handleError(e: Event) {
    const err = (e as ErrorEvent).error ?? (e as ErrorEvent).message ?? "Unknown error";
    error = String(err);
    e.stopPropagation();
    e.preventDefault();
  }

  function retry() {
    error = null;
    key++;
  }

  onMount(() => {
    // Also catch promise rejections from async operations inside children.
    const onRejection = (e: PromiseRejectionEvent) => {
      // Only capture if we don't already have an error showing.
      if (error) return;
      error = String(e.reason?.message ?? e.reason ?? "Async error");
      e.preventDefault();
    };
    window.addEventListener("unhandledrejection", onRejection);
    return () => window.removeEventListener("unhandledrejection", onRejection);
  });
</script>

{#if error}
  <div class="flex h-full w-full items-center justify-center bg-[var(--color-surface-0)]">
    <div class="max-w-md text-center">
      <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-[var(--color-danger)]/10">
        <AlertTriangle size="22" class="text-[var(--color-danger)]" />
      </div>
      <h3 class="mt-3 text-sm font-semibold text-[var(--color-text-1)]">
        {name} crashed
      </h3>
      <p class="mt-1 font-mono text-[11px] text-[var(--color-text-3)] break-all">
        {error}
      </p>
      <button
        class="mt-4 inline-flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-surface-0)] hover:opacity-90 transition-opacity"
        onclick={retry}
      >
        <RotateCcw size="11" />
        Retry
      </button>
    </div>
  </div>
{:else}
  {#key key}
    {@render children()}
  {/key}
{/if}
