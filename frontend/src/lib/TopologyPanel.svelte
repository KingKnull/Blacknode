<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import { Network, Server, Shield } from "@lucide/svelte";

  // Force-directed graph rendering hosts as nodes and ProxyJump as directed
  // edges (target → bastion). The layout runs locally — no graph library —
  // and converges in a few hundred frames for the host counts we expect
  // (single users typically have < 100 saved hosts).
  //
  // IMPORTANT: The physics state (positions, velocities) is stored in plain
  // arrays — NOT Svelte $state. We render by copying snapshots into a
  // reactive `renderNodes` array on a throttled schedule. This keeps the
  // 60fps physics loop off the Svelte reactivity graph entirely.

  type Node = {
    id: string;
    name: string;
    env: string;
    isBastion: boolean;
    x: number;
    y: number;
    vx: number;
    vy: number;
  };
  type Edge = { from: string; to: string };

  // --- Reactive state (Svelte touches these) ---
  let width = $state(800);
  let height = $state(600);
  let svgEl: SVGSVGElement | null = $state(null);
  let renderNodes = $state<Node[]>([]);
  let renderEdges = $state<Edge[]>([]);
  let hovered = $state<string | null>(null);

  // --- Plain state (physics loop only, NO reactivity) ---
  let physNodes: Node[] = [];
  let physEdges: Edge[] = [];
  let dragging: string | null = null;
  let raf = 0;
  let mounted = false;

  function rebuild() {
    const hostsByName = new Map<string, string>();
    for (const h of app.hosts) hostsByName.set(h.name, h.id);
    const bastions = new Set<string>();
    const newEdges: Edge[] = [];
    for (const h of app.hosts) {
      if (h.proxyJump) {
        const targetID = hostsByName.get(h.proxyJump);
        if (targetID) {
          newEdges.push({ from: h.id, to: targetID });
          bastions.add(targetID);
        }
      }
    }

    const seedX = width / 2;
    const seedY = height / 2;
    const oldByID = new Map(physNodes.map((n) => [n.id, n]));
    const newNodes: Node[] = app.hosts.map((h, i) => {
      const prev = oldByID.get(h.id);
      if (prev) {
        return {
          ...prev,
          name: h.name,
          env: h.environment ?? "",
          isBastion: bastions.has(h.id),
        };
      }
      const angle = (i / Math.max(app.hosts.length, 1)) * Math.PI * 2;
      const r = 180;
      return {
        id: h.id,
        name: h.name,
        env: h.environment ?? "",
        isBastion: bastions.has(h.id),
        x: seedX + Math.cos(angle) * r,
        y: seedY + Math.sin(angle) * r,
        vx: 0,
        vy: 0,
      };
    });

    physNodes = newNodes;
    physEdges = newEdges;
    flush();
    wakeSimulation();
  }

  // Copy physics state into Svelte-reactive arrays for rendering.
  function flush() {
    renderNodes = physNodes.map((n) => ({ ...n }));
    renderEdges = [...physEdges];
  }

  // Re-derive when hosts change.
  $effect(() => {
    void app.hosts;
    if (mounted) rebuild();
  });

  // --- Physics loop ---
  // Runs entirely outside Svelte reactivity. Only `flush()` bridges back.
  let tickCount = 0;
  const FLUSH_EVERY = 4; // flush to DOM every 4th frame (~15fps visual)

  function step() {
    physicsTick();
    tickCount++;

    if (tickCount % FLUSH_EVERY === 0) {
      flush();
    }

    if (!dragging) {
      let maxV = 0;
      for (const n of physNodes) {
        const v = Math.abs(n.vx) + Math.abs(n.vy);
        if (v > maxV) maxV = v;
      }
      if (maxV < 0.05) {
        raf = 0;
        flush(); // final render
        return;
      }
    }
    raf = requestAnimationFrame(step);
  }

  function wakeSimulation() {
    if (raf) return;
    raf = requestAnimationFrame(step);
  }

  // One physics step. Repulsion between every pair, spring along each edge,
  // weak gravity toward the centroid, light damping. Tuned for ~100 nodes.
  function physicsTick() {
    const n = physNodes.length;
    if (n === 0) return;
    const repulse = 1400;
    const spring = 0.012;
    const restLen = 140;
    const gravity = 0.005;
    const damping = 0.86;
    const cx = width / 2;
    const cy = height / 2;

    for (const node of physNodes) {
      if (dragging === node.id) {
        node.vx = 0;
        node.vy = 0;
        continue;
      }
      let fx = 0;
      let fy = 0;
      for (const other of physNodes) {
        if (other === node) continue;
        const dx = node.x - other.x;
        const dy = node.y - other.y;
        const distSq = dx * dx + dy * dy + 0.01;
        const dist = Math.sqrt(distSq);
        const force = repulse / distSq;
        fx += (dx / dist) * force;
        fy += (dy / dist) * force;
      }
      fx += (cx - node.x) * gravity;
      fy += (cy - node.y) * gravity;
      node.vx = (node.vx + fx) * damping;
      node.vy = (node.vy + fy) * damping;
    }
    for (const e of physEdges) {
      const a = physNodes.find((m) => m.id === e.from);
      const b = physNodes.find((m) => m.id === e.to);
      if (!a || !b) continue;
      const dx = b.x - a.x;
      const dy = b.y - a.y;
      const dist = Math.sqrt(dx * dx + dy * dy + 0.01);
      const diff = dist - restLen;
      const fx = (dx / dist) * diff * spring;
      const fy = (dy / dist) * diff * spring;
      if (dragging !== a.id) {
        a.vx += fx;
        a.vy += fy;
      }
      if (dragging !== b.id) {
        b.vx -= fx;
        b.vy -= fy;
      }
    }
    for (const node of physNodes) {
      if (dragging === node.id) continue;
      node.x += node.vx;
      node.y += node.vy;
      const pad = 30;
      if (node.x < pad) { node.x = pad; node.vx = 0; }
      if (node.x > width - pad) { node.x = width - pad; node.vx = 0; }
      if (node.y < pad) { node.y = pad; node.vy = 0; }
      if (node.y > height - pad) { node.y = height - pad; node.vy = 0; }
    }
  }

  // --- Drag handlers ---
  // Uses window-level listeners instead of pointer capture to avoid
  // WebKitGTK pointer capture bugs in the Wails webview.
  function startDrag(e: MouseEvent, id: string) {
    e.preventDefault();
    dragging = id;
    window.addEventListener("mousemove", moveDrag);
    window.addEventListener("mouseup", endDrag);
    wakeSimulation();
  }

  function moveDrag(e: MouseEvent) {
    if (!dragging || !svgEl) return;
    const rect = svgEl.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const y = e.clientY - rect.top;
    const node = physNodes.find((m) => m.id === dragging);
    if (node) {
      node.x = x;
      node.y = y;
      flush();
    }
  }

  function endDrag() {
    dragging = null;
    window.removeEventListener("mousemove", moveDrag);
    window.removeEventListener("mouseup", endDrag);
  }

  function envColor(env: string): string {
    if (env === "production") return "var(--color-danger)";
    if (env === "staging") return "var(--color-warn)";
    if (env === "dev") return "var(--color-info)";
    return "var(--color-text-3)";
  }

  function onResize() {
    if (!svgEl) return;
    const rect = svgEl.getBoundingClientRect();
    width = rect.width;
    height = rect.height;
  }

  onMount(() => {
    mounted = true;
    onResize();
    window.addEventListener("resize", onResize);
    rebuild();
  });

  onDestroy(() => {
    mounted = false;
    if (raf) cancelAnimationFrame(raf);
    raf = 0;
    window.removeEventListener("resize", onResize);
    window.removeEventListener("mousemove", moveDrag);
    window.removeEventListener("mouseup", endDrag);
  });
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={Network}
    title="Topology"
    subtitle="Hosts and ProxyJump bastion edges. Drag to rearrange."
  />

  <div class="flex flex-1 overflow-hidden">
    <div class="flex-1 surface-1">
      {#if app.hosts.length === 0}
        <div class="flex h-full items-center justify-center">
          <div class="text-center">
            <Network size="22" class="mx-auto text-[var(--color-text-4)]" />
            <p class="mt-2 text-xs text-[var(--color-text-3)]">
              No hosts saved. Add some, then come back here to see the graph.
            </p>
          </div>
        </div>
      {:else}
        <svg
          bind:this={svgEl}
          class="h-full w-full"
          role="presentation"
        >
          <defs>
            <marker
              id="arrowhead"
              viewBox="0 0 10 10"
              refX="10"
              refY="5"
              markerWidth="6"
              markerHeight="6"
              orient="auto-start-reverse"
            >
              <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--color-text-3)" />
            </marker>
          </defs>
          {#each renderEdges as e (e.from + "->" + e.to)}
            {@const a = renderNodes.find((m) => m.id === e.from)}
            {@const b = renderNodes.find((m) => m.id === e.to)}
            {#if a && b}
              {@const dx = b.x - a.x}
              {@const dy = b.y - a.y}
              {@const dist = Math.sqrt(dx * dx + dy * dy) || 1}
              {@const ux = dx / dist}
              {@const uy = dy / dist}
              <!-- Pull endpoints in by node radius so the arrow lands on the rim. -->
              <line
                x1={a.x + ux * 20}
                y1={a.y + uy * 20}
                x2={b.x - ux * 20}
                y2={b.y - uy * 20}
                stroke="var(--color-text-4)"
                stroke-width="1"
                marker-end="url(#arrowhead)"
                opacity={hovered &&
                hovered !== e.from &&
                hovered !== e.to
                  ? 0.2
                  : 0.7}
              />
            {/if}
          {/each}
          {#each renderNodes as node (node.id)}
            <g
              transform="translate({node.x},{node.y})"
              role="button"
              tabindex="0"
              style="cursor: grab"
              onmousedown={(e) => startDrag(e, node.id)}
              onmouseenter={() => (hovered = node.id)}
              onmouseleave={() => (hovered = null)}
              onclick={() => (app.selectedHostID = node.id)}
              onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); app.selectedHostID = node.id; } }}
            >
              <circle
                r="18"
                fill={node.isBastion
                  ? "var(--color-accent)"
                  : "var(--color-surface-3)"}
                stroke={envColor(node.env)}
                stroke-width="2"
                opacity={hovered === null || hovered === node.id ? 1 : 0.5}
              />
              <foreignObject x="-9" y="-9" width="18" height="18">
                <div
                  class="flex h-full w-full items-center justify-center"
                  style="color: {node.isBastion
                    ? 'var(--color-surface-0)'
                    : 'var(--color-text-1)'}"
                >
                  {#if node.isBastion}
                    <Shield size="11" />
                  {:else}
                    <Server size="11" />
                  {/if}
                </div>
              </foreignObject>
              <text
                y="32"
                text-anchor="middle"
                style="pointer-events: none; user-select: none; font-size: 10px; fill: var(--color-text-2)"
              >
                {node.name}
              </text>
            </g>
          {/each}
        </svg>
      {/if}
    </div>

    <aside class="w-60 shrink-0 border-l hairline surface-2 p-3 text-[11px]">
      <h4 class="mb-2 text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]">
        Legend
      </h4>
      <ul class="space-y-2 text-[var(--color-text-2)]">
        <li class="flex items-center gap-2">
          <span
            class="inline-block h-3 w-3 rounded-full"
            style="background: var(--color-accent)"
          ></span>
          Bastion (referenced by ProxyJump)
        </li>
        <li class="flex items-center gap-2">
          <span
            class="inline-block h-3 w-3 rounded-full border-2"
            style="background: var(--color-surface-3); border-color: var(--color-text-3)"
          ></span>
          Regular host
        </li>
        <li class="flex items-center gap-2">
          <span
            class="inline-block h-3 w-3 rounded-full border-2"
            style="background: var(--color-surface-3); border-color: var(--color-danger)"
          ></span>
          production
        </li>
        <li class="flex items-center gap-2">
          <span
            class="inline-block h-3 w-3 rounded-full border-2"
            style="background: var(--color-surface-3); border-color: var(--color-warn)"
          ></span>
          staging
        </li>
        <li class="flex items-center gap-2">
          <span
            class="inline-block h-3 w-3 rounded-full border-2"
            style="background: var(--color-surface-3); border-color: var(--color-info)"
          ></span>
          dev
        </li>
      </ul>

      <h4 class="mt-4 mb-2 text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]">
        Stats
      </h4>
      <ul class="space-y-1 text-[var(--color-text-2)]">
        <li>{app.hosts.length} hosts</li>
        <li>{renderEdges.length} ProxyJump edges</li>
        <li>{renderNodes.filter((n) => n.isBastion).length} bastions</li>
      </ul>

      <p class="mt-4 text-[10px] text-[var(--color-text-4)]">
        Click a node to select that host in the sidebar. Drag to pin position.
      </p>
    </aside>
  </div>
</div>
