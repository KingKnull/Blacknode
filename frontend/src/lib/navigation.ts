export const OPTIONAL_VIEWS = [
  { id: "exec", label: "Multi-host execution" },
  { id: "runbooks", label: "Runbooks" },
  { id: "snippets", label: "Snippets" },
  { id: "recordings", label: "Recordings" },
  { id: "history", label: "Command history" },
  { id: "metrics", label: "Metrics" },
  { id: "logs", label: "Logs" },
  { id: "processes", label: "Processes" },
  { id: "activity", label: "Activity" },
  { id: "forwards", label: "Port forwarding" },
  { id: "network", label: "Network diagnostics" },
  { id: "topology", label: "Topology" },
  { id: "containers", label: "Containers" },
  { id: "database", label: "Database" },
  { id: "http", label: "HTTP client" },
  { id: "plugins", label: "Plugins" },
] as const;
export const NAVIGATION_KEY = "blacknode.navigation.v1";
const optional = new Set<string>(OPTIONAL_VIEWS.map((v) => v.id));

export function readHiddenViews(storage: Pick<Storage, "getItem">): string[] {
  try {
    const value = JSON.parse(storage.getItem(NAVIGATION_KEY) ?? "[]");
    return Array.isArray(value) ? [...new Set(value.filter((id): id is string => typeof id === "string" && optional.has(id)))] : [];
  } catch { return []; }
}

export function viewVisible(view: string, hidden: string[]): boolean {
  return !hidden.includes(view.startsWith("plugin:") ? "plugins" : view);
}
