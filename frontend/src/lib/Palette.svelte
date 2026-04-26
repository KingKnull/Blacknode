<script lang="ts">
  import { onMount, tick } from "svelte";
  import { app, type View } from "./state.svelte";
  import { VaultService } from "../../bindings/github.com/blacknode/blacknode";
  import {
    TerminalSquare,
    Server,
    Zap,
    Folder,
    Activity,
    KeyRound,
    ScrollText,
    Settings as SettingsIcon,
    Sparkles,
    Lock,
    Search,
    Network,
    Film,
  } from "@lucide/svelte";

  type Action = {
    id: string;
    label: string;
    hint?: string;
    icon: any;
    category: string;
    run: () => void | Promise<void>;
    keywords?: string;
  };

  type Props = {
    onNewTab: () => void;
  };
  let { onNewTab }: Props = $props();

  let input = $state("");
  let highlighted = $state(0);
  let inputEl: HTMLInputElement | undefined = $state();

  const VIEW_ACTIONS: { id: View; label: string; icon: any }[] = [
    { id: "terminals", label: "Go to Terminals", icon: TerminalSquare },
    { id: "exec", label: "Go to Multi-host", icon: Zap },
    { id: "files", label: "Go to Files", icon: Folder },
    { id: "metrics", label: "Go to Metrics", icon: Activity },
    { id: "logs", label: "Go to Logs", icon: ScrollText },
    { id: "forwards", label: "Go to Forwards", icon: Network },
    { id: "recordings", label: "Go to Recordings", icon: Film },
    { id: "keys", label: "Go to Keys", icon: KeyRound },
    { id: "settings", label: "Go to Settings", icon: SettingsIcon },
  ];

  let actions = $derived<Action[]>([
    ...VIEW_ACTIONS.map((v) => ({
      id: `view:${v.id}`,
      label: v.label,
      hint: "view",
      icon: v.icon,
      category: "Navigate",
      run: () => {
        app.view = v.id;
      },
    })),
    {
      id: "tab:new",
      label: "New terminal tab",
      hint: "⌘T · open a fresh local shell",
      icon: TerminalSquare,
      category: "Terminal",
      run: () => {
        app.view = "terminals";
        onNewTab();
      },
    },
    {
      id: "ai:open",
      label: app.aiOpen ? "Close AI assistant" : "Open AI assistant",
      hint: "⌘I · translate · explain",
      icon: Sparkles,
      category: "AI",
      run: () => {
        app.aiOpen = !app.aiOpen;
      },
    },
    ...app.hosts.map(
      (h): Action => ({
        id: `host:${h.id}`,
        label: `Select host: ${h.name}`,
        hint: `${h.username}@${h.host}:${h.port}`,
        icon: Server,
        category: "Hosts",
        keywords: `${h.host} ${h.username} ${h.group}`,
        run: () => {
          app.selectedHostID = h.id;
        },
      }),
    ),
    {
      id: "vault:lock",
      label: "Lock vault",
      hint: "clear master key from memory",
      icon: Lock,
      category: "System",
      run: async () => {
        await VaultService.Lock();
        await app.refreshAll();
      },
    },
  ]);

  function score(a: Action, q: string): number {
    if (!q) return 1;
    const haystack = `${a.label} ${a.hint ?? ""} ${a.keywords ?? ""} ${a.category}`.toLowerCase();
    const needle = q.toLowerCase();
    if (haystack.includes(needle)) return 100 - haystack.indexOf(needle);
    // Cheap fuzzy: every char in order somewhere.
    let i = 0;
    for (const c of haystack) {
      if (c === needle[i]) i++;
      if (i === needle.length) return 50;
    }
    return 0;
  }

  let filtered = $derived(
    actions
      .map((a) => ({ a, s: score(a, input) }))
      .filter((x) => x.s > 0)
      .sort((x, y) => y.s - x.s)
      .map((x) => x.a),
  );

  $effect(() => {
    if (app.paletteOpen) {
      input = "";
      highlighted = 0;
      void focusInput();
    }
  });

  async function focusInput() {
    await tick();
    inputEl?.focus();
  }

  onMount(() => {
    const onKey = (e: KeyboardEvent) => {
      const isMod = e.metaKey || e.ctrlKey;
      if (isMod && e.key.toLowerCase() === "k") {
        e.preventDefault();
        app.paletteOpen = !app.paletteOpen;
        return;
      }
      if (!app.paletteOpen) return;
      if (e.key === "Escape") {
        e.preventDefault();
        app.paletteOpen = false;
      } else if (e.key === "ArrowDown") {
        e.preventDefault();
        highlighted = Math.min(highlighted + 1, filtered.length - 1);
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        highlighted = Math.max(highlighted - 1, 0);
      } else if (e.key === "Enter") {
        e.preventDefault();
        const a = filtered[highlighted];
        if (a) {
          void a.run();
          app.paletteOpen = false;
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  // Group filtered into category buckets in input order.
  let grouped = $derived(() => {
    const groups: { name: string; items: Action[] }[] = [];
    for (const a of filtered) {
      let g = groups.find((g) => g.name === a.category);
      if (!g) {
        g = { name: a.category, items: [] };
        groups.push(g);
      }
      g.items.push(a);
    }
    return groups;
  });

  // Map filtered action -> overall index for keyboard highlight.
  function indexOf(a: Action) {
    return filtered.indexOf(a);
  }
</script>

{#if app.paletteOpen}
  <div
    class="fixed inset-0 z-50 flex items-start justify-center bg-black/60 backdrop-blur-sm pt-[15vh]"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) app.paletteOpen = false;
    }}
  >
    <div
      class="w-[560px] overflow-hidden rounded-xl border hairline-strong surface-2 shadow-2xl shadow-black/50"
    >
      <div class="flex items-center gap-2 border-b hairline px-4 py-3">
        <Search size="14" class="text-[var(--color-text-3)]" />
        <input
          bind:this={inputEl}
          bind:value={input}
          class="flex-1 bg-transparent text-sm outline-none placeholder:text-[var(--color-text-4)]"
          placeholder="Type a command, host name, or view…"
        />
        <kbd
          class="rounded border hairline px-1.5 py-0.5 text-[10px] font-mono text-[var(--color-text-4)]"
          >esc</kbd
        >
      </div>

      <div class="max-h-[420px] overflow-y-auto py-1">
        {#each grouped() as group (group.name)}
          <div class="px-4 pt-2 pb-1 text-[9px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-4)]">
            {group.name}
          </div>
          {#each group.items as a (a.id)}
            {@const idx = indexOf(a)}
            <button
              class="flex w-full items-center gap-2.5 px-4 py-2 text-left text-xs {idx ===
              highlighted
                ? 'bg-[var(--color-surface-3)] text-[var(--color-text-1)]'
                : 'text-[var(--color-text-2)] hover:bg-[var(--color-surface-2)]'}"
              onmouseenter={() => (highlighted = idx)}
              onclick={() => {
                void a.run();
                app.paletteOpen = false;
              }}
            >
              <a.icon size="13" class="text-[var(--color-text-3)]" />
              <span class="flex-1 truncate">{a.label}</span>
              {#if a.hint}
                <span class="font-mono text-[10px] text-[var(--color-text-4)]"
                  >{a.hint}</span
                >
              {/if}
            </button>
          {/each}
        {/each}
        {#if filtered.length === 0}
          <div class="px-4 py-8 text-center text-xs text-[var(--color-text-3)]">
            No matches.
          </div>
        {/if}
      </div>

      <div
        class="flex items-center gap-3 border-t hairline px-4 py-1.5 text-[10px] text-[var(--color-text-4)]"
      >
        <span class="flex items-center gap-1">
          <kbd class="rounded border hairline px-1 font-mono">↑↓</kbd> navigate
        </span>
        <span class="flex items-center gap-1">
          <kbd class="rounded border hairline px-1 font-mono">↵</kbd> select
        </span>
        <span class="ml-auto flex items-center gap-1">
          <kbd class="rounded border hairline px-1 font-mono">⌘K</kbd> toggle
        </span>
      </div>
    </div>
  </div>
{/if}
