<script lang="ts">
  import { onMount, tick } from "svelte";
  import { app, type View } from "./state.svelte";
  import {
    VaultService,
    SnippetService,
  } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Snippet } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { bus } from "./events";
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
    Boxes,
    Radar,
    Cpu,
    Globe2,
    Database,
    Bookmark,
    History as HistoryIcon,
    LayoutGrid,
    Radio,
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
  let snippets = $state<Snippet[]>([]);

  const VIEW_ACTIONS: { id: View; label: string; icon: any }[] = [
    { id: "terminals", label: "Go to Terminals", icon: TerminalSquare },
    { id: "exec", label: "Go to Multi-host", icon: Zap },
    { id: "files", label: "Go to Files", icon: Folder },
    { id: "metrics", label: "Go to Metrics", icon: Activity },
    { id: "logs", label: "Go to Logs", icon: ScrollText },
    { id: "forwards", label: "Go to Forwards", icon: Network },
    { id: "recordings", label: "Go to Recordings", icon: Film },
    { id: "containers", label: "Go to Containers", icon: Boxes },
    { id: "network", label: "Go to Network diagnostics", icon: Radar },
    { id: "processes", label: "Go to Processes", icon: Cpu },
    { id: "http", label: "Go to HTTP", icon: Globe2 },
    { id: "database", label: "Go to Database", icon: Database },
    { id: "snippets", label: "Go to Snippets", icon: Bookmark },
    { id: "history", label: "Go to History", icon: HistoryIcon },
    { id: "keys", label: "Go to Keys", icon: KeyRound },
    { id: "vault", label: "Go to Vault", icon: Lock },
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
    {
      id: "cmd:tile",
      label: "Tile active hosts",
      hint: "grid split all connected sessions",
      icon: LayoutGrid,
      category: "Multi-host",
      keywords: "split grid pane tile layout",
      run: () => {
        bus.emit('tile-active-hosts');
        app.view = "terminals";
      },
    },
    {
      id: "cmd:broadcast",
      label: app.broadcastEnabled ? "Disable broadcast mode" : "Enable broadcast mode",
      hint: "type once, send to all panes",
      icon: Radio,
      category: "Multi-host",
      keywords: "cast multi broadcast",
      run: () => {
        if (!app.broadcastEnabled && app.broadcastSet.size === 0) {
          app.toast('warn', 'NO PANES IN BROADCAST', 'Click the \'cast\' button on each pane first.');
        }
        app.broadcastEnabled = !app.broadcastEnabled;
      },
    },
    ...snippets.map(
      (s): Action => ({
        id: `snippet:${s.id}`,
        label: `Snippet: ${s.name}`,
        hint: s.description || s.body.slice(0, 60),
        icon: Bookmark,
        category: "Snippets",
        keywords: `${s.body} ${(s.tags ?? []).join(" ")}`,
        run: async () => {
          const rendered = (await SnippetService.Apply(
            s.id,
            {},
            "",
            "",
            true,
          )) as string;
          bus.emit('insert-into-active-terminal', rendered);
          app.view = "terminals";
        },
      }),
    ),
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
      // Refresh snippets on each open so newly-saved ones show up.
      void SnippetService.List().then((s) => {
        snippets = (s ?? []) as Snippet[];
      });
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
    class="fixed inset-0 z-50 flex items-start justify-center bg-black/80 pt-[14vh]"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) app.paletteOpen = false;
    }}
  >
    <div
      class="w-[580px] overflow-hidden border hairline-strong surface-2 shadow-2xl"
      style="box-shadow: 0 0 0 1px var(--color-line-strong), 0 0 60px rgba(0,255,136,0.05), 0 40px 80px rgba(0,0,0,0.6);"
    >
      <!-- Search input -->
      <div class="flex items-center gap-3 border-b hairline px-4 py-3">
        <span class="font-mono text-xs text-[var(--color-accent)]/60">&gt;_</span>
        <input
          bind:this={inputEl}
          bind:value={input}
          class="flex-1 bg-transparent text-sm text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)]"
          placeholder="Type a command, host, or view..."
        />
        <kbd class="border border-[var(--color-line-strong)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--color-text-4)]">ESC</kbd>
      </div>

      <!-- Results -->
      <div class="max-h-[400px] overflow-y-auto">
        {#each grouped() as group (group.name)}
          <div class="px-4 pt-3 pb-1 text-[10px] font-semibold text-[var(--color-text-4)] uppercase tracking-wider">
            {group.name}
          </div>
          {#each group.items as a (a.id)}
            {@const idx = indexOf(a)}
            <button
              class="flex w-full items-center gap-2.5 border-l-2 px-4 py-2 text-left text-sm transition-colors {idx === highlighted
                ? 'border-[var(--color-accent)] bg-[var(--color-accent)]/6 text-[var(--color-text-1)]'
                : 'border-transparent text-[var(--color-text-2)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-1)]'}"
              onmouseenter={() => (highlighted = idx)}
              onclick={() => { void a.run(); app.paletteOpen = false; }}
            >
              <a.icon size="13" class={idx === highlighted ? 'text-[var(--color-accent)] shrink-0' : 'text-[var(--color-text-4)] shrink-0'} />
              <span class="flex-1 truncate">{a.label}</span>
              {#if a.hint}
                <span class="shrink-0 font-mono text-[10px] text-[var(--color-text-4)]">{a.hint}</span>
              {/if}
            </button>
          {/each}
        {/each}
        {#if filtered.length === 0}
          <div class="px-4 py-10 text-center text-sm text-[var(--color-text-4)]">
            No matches
          </div>
        {/if}
      </div>

      <!-- Footer -->
      <div class="flex items-center gap-4 border-t hairline px-4 py-2 font-mono text-[10px] text-[var(--color-text-4)]">
        <span class="flex items-center gap-1.5"><kbd class="border border-[var(--color-line-strong)] px-1">↑↓</kbd> navigate</span>
        <span class="flex items-center gap-1.5"><kbd class="border border-[var(--color-line-strong)] px-1">↵</kbd> select</span>
        <span class="ml-auto flex items-center gap-1.5"><kbd class="border border-[var(--color-line-strong)] px-1">⌘K</kbd> toggle</span>
      </div>
    </div>
  </div>
{/if}
