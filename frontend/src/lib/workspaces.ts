import type { PaneNode } from "./panes";

export type ConnectionTarget = { hostID: string; via: "ssh" | "mosh"; remoteSession?: string; manager?: string };
export type WorkspaceTab = { id: string; root: PaneNode; activeLeafID: string };
export type SavedPane =
  | { kind: "leaf"; target: ConnectionTarget | null }
  | { kind: "split"; direction: "horizontal" | "vertical"; ratio: number; a: SavedPane; b: SavedPane };
export type WorkspaceFilters = {
  hostSearch: string;
};
export type WorkspaceContext = {
  serviceWorkspaceID: string | null;
};
export type WorkspaceSnapshot = {
  version: 2;
  tabs: { root: SavedPane; activeLeaf: number }[];
  activeTab: number;
  view: string;
  sidebarWidth: number;
  selectedHostID: string | null;
  forwardIDs: string[];
  filters: WorkspaceFilters;
  context: WorkspaceContext;
};
export type SavedWorkspace = { id: string; name: string; snapshot: WorkspaceSnapshot };
export type PersistedWorkspaceState = { version: 1; snapshot: WorkspaceSnapshot };

export const SESSION_KEY = "blacknode.session";
export const WORKSPACES_KEY = "blacknode.workspaces.v1";
const VIEWS = new Set(["terminals", "exec", "ops", "runbooks", "files", "metrics", "logs", "forwards", "recordings", "containers", "network", "processes", "http", "database", "snippets", "history", "topology", "plugins", "activity", "vault", "keys", "settings", "service-workspaces", "kubernetes", "fleet", "incidents", "deployments", "team"]);
const bounded = (n: unknown, fallback: number, min: number, max: number) =>
  typeof n === "number" && Number.isFinite(n) ? Math.max(min, Math.min(max, n)) : fallback;

// Persist only the layout, safe UI preferences, and saved record IDs.
// Credentials, terminal output, broadcast membership, and runtime session IDs
// never enter a snapshot.
export function captureWorkspace(
  tabs: WorkspaceTab[], activeTabID: string,
  targets: Record<string, ConnectionTarget | null>,
  options: Pick<WorkspaceSnapshot, "view" | "sidebarWidth" | "selectedHostID" | "forwardIDs"> &
    Partial<Pick<WorkspaceSnapshot, "filters" | "context">>,
): WorkspaceSnapshot {
  function save(node: PaneNode): SavedPane {
    if (node.kind === "leaf") {
      const target = targets[node.sessionID];
      return { kind: "leaf", target: target ? { ...target } : null };
    }
    return { kind: "split", direction: node.direction, ratio: node.ratio, a: save(node.a), b: save(node.b) };
  }
  return {
    version: 2,
    tabs: tabs.map((tab) => {
      const ids: string[] = [];
      const walk = (node: PaneNode) => { if (node.kind === "leaf") ids.push(node.id); else { walk(node.a); walk(node.b); } };
      walk(tab.root);
      return { root: save(tab.root), activeLeaf: Math.max(0, ids.indexOf(tab.activeLeafID)) };
    }),
    activeTab: Math.max(0, tabs.findIndex((t) => t.id === activeTabID)),
    view: options.view,
    sidebarWidth: options.sidebarWidth,
    selectedHostID: options.selectedHostID,
    forwardIDs: [...options.forwardIDs],
    filters: {
      hostSearch: typeof options.filters?.hostSearch === "string" ? options.filters.hostSearch.slice(0, 200) : "",
    },
    context: {
      serviceWorkspaceID: typeof options.context?.serviceWorkspaceID === "string" && options.context.serviceWorkspaceID
        ? options.context.serviceWorkspaceID.slice(0, 160)
        : null,
    },
  };
}

export function parseWorkspace(value: unknown): WorkspaceSnapshot | null {
  if (!value || typeof value !== "object") return null;
  const raw = value as Record<string, any>;
  let count = 0;
  function pane(node: any, depth = 0): SavedPane {
    if (!node || depth > 12 || ++count > 256) throw new Error("Invalid workspace layout");
    if (node.kind === "leaf") {
      const t = node.target;
      const target: ConnectionTarget | null = t && typeof t.hostID === "string" && t.hostID && (t.via === "ssh" || t.via === "mosh") ? { hostID: t.hostID, via: t.via } : null;
      if (target && typeof t.remoteSession === "string" && /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$/.test(t.remoteSession)) target.remoteSession = t.remoteSession;
      if (target && ["auto", "off", "tmux", "zellij"].includes(t.manager)) target.manager = t.manager;
      return { kind: "leaf", target };
    }
    if (node.kind !== "split" || !["horizontal", "vertical"].includes(node.direction)) throw new Error("Invalid pane");
    return { kind: "split", direction: node.direction, ratio: bounded(node.ratio, 0.5, 0.05, 0.95), a: pane(node.a, depth + 1), b: pane(node.b, depth + 1) };
  }
  try {
    let tabs: WorkspaceSnapshot["tabs"];
    if (raw.version === 2 && Array.isArray(raw.tabs) && raw.tabs.length && raw.tabs.length <= 32) {
      tabs = raw.tabs.map((t: any) => ({ root: pane(t.root), activeLeaf: Math.floor(bounded(t.activeLeaf, 0, 0, 255)) }));
    } else if (raw.version === undefined && Number.isInteger(raw.tabCount) && raw.tabCount > 0) {
      // Migrate the old tab-count-only session without opening unbounded PTYs.
      tabs = Array.from({ length: Math.min(raw.tabCount, 32) }, () => ({ root: { kind: "leaf" as const, target: null }, activeLeaf: 0 }));
    } else return null;
    return {
      version: 2, tabs,
      activeTab: Math.floor(bounded(raw.activeTab, 0, 0, tabs.length - 1)),
      view: VIEWS.has(raw.view) ? raw.view : "terminals",
      sidebarWidth: bounded(raw.sidebarWidth, 252, 160, 600),
      selectedHostID: typeof raw.selectedHostID === "string" ? raw.selectedHostID : null,
      forwardIDs: Array.isArray(raw.forwardIDs) ? [...new Set<string>(raw.forwardIDs.filter((id: unknown) => typeof id === "string" && id.length > 0))].slice(0, 64) : [],
      filters: {
        hostSearch: typeof raw.filters?.hostSearch === "string" ? raw.filters.hostSearch.slice(0, 200) : "",
      },
      context: {
        serviceWorkspaceID: typeof raw.context?.serviceWorkspaceID === "string" && raw.context.serviceWorkspaceID
          ? raw.context.serviceWorkspaceID.slice(0, 160)
          : null,
      },
    };
  } catch { return null; }
}

export function parsePersistedWorkspaceState(value: unknown): WorkspaceSnapshot | null {
  try {
    const raw = typeof value === "string" ? JSON.parse(value) : value;
    if (!raw || typeof raw !== "object") return null;
    const state = raw as Record<string, unknown>;
    if (state.version !== 1) return null;
    return parseWorkspace(state.snapshot);
  } catch { return null; }
}

// A valid snapshot can still refer to hosts or saved forwards deleted after it
// was written. Remove those references before restoring so a stale record can
// never trigger a reconnect against an unknown inventory item.
export function sanitizeWorkspace(snapshot: WorkspaceSnapshot, hostIDs: Iterable<string>, forwardIDs?: Iterable<string>): WorkspaceSnapshot {
  const hosts = new Set(hostIDs);
  const forwards = forwardIDs ? new Set(forwardIDs) : null;
  function pane(node: SavedPane): SavedPane {
    if (node.kind === "leaf") {
      return { kind: "leaf", target: node.target && hosts.has(node.target.hostID) ? { ...node.target } : null };
    }
    return { ...node, a: pane(node.a), b: pane(node.b) };
  }
  return {
    ...snapshot,
    tabs: snapshot.tabs.map((tab) => ({ ...tab, root: pane(tab.root) })),
    selectedHostID: snapshot.selectedHostID && hosts.has(snapshot.selectedHostID) ? snapshot.selectedHostID : null,
    forwardIDs: snapshot.forwardIDs.filter((id) => !forwards || forwards.has(id)),
  };
}

export function readWorkspace(storage: Pick<Storage, "getItem">): WorkspaceSnapshot | null {
  try { return parseWorkspace(JSON.parse(storage.getItem(SESSION_KEY) ?? "null")); } catch { return null; }
}

export function readSavedWorkspaces(storage: Pick<Storage, "getItem">): SavedWorkspace[] {
  try {
    const rows = JSON.parse(storage.getItem(WORKSPACES_KEY) ?? "[]");
    if (!Array.isArray(rows)) return [];
    const seen = new Set<string>();
    return rows.slice(0, 50).flatMap((row) => {
      const snapshot = parseWorkspace(row?.snapshot);
      if (!snapshot || typeof row.id !== "string" || !row.id || seen.has(row.id) || typeof row.name !== "string" || !row.name.trim()) return [];
      seen.add(row.id);
      return [{ id: row.id, name: row.name.trim().slice(0, 80), snapshot }];
    });
  } catch { return []; }
}

export function restoreWorkspace(snapshot: WorkspaceSnapshot, newID: () => string, knownHostIDs?: Iterable<string>) {
  const targets: Record<string, ConnectionTarget | null> = {};
  const hosts = knownHostIDs ? new Set(knownHostIDs) : null;
  const tabs: WorkspaceTab[] = snapshot.tabs.map((tab) => {
    const leafIDs: string[] = [];
    function restore(node: SavedPane): PaneNode {
      const id = newID();
      if (node.kind === "leaf") {
        const sessionID = newID();
        leafIDs.push(id);
        targets[sessionID] = node.target && (!hosts || hosts.has(node.target.hostID)) ? node.target : null;
        return { kind: "leaf", id, sessionID };
      }
      return { kind: "split", id, direction: node.direction, ratio: node.ratio, a: restore(node.a), b: restore(node.b) };
    }
    const root = restore(tab.root);
    return { id: newID(), root, activeLeafID: leafIDs[tab.activeLeaf] ?? leafIDs[0] };
  });
  return { tabs, targets, activeTabID: tabs[snapshot.activeTab]?.id ?? tabs[0].id };
}
