import type { Host } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";

/** Fields users reasonably expect the host-list search to match. */
export function hostMatchesFilter(host: Host, query: string): boolean {
  const filter = query.trim().toLowerCase();
  if (!filter) return true;
  return [
    host.name,
    host.alias ?? "",
    host.host,
    host.username,
    host.group ?? "",
    host.environment ?? "",
    ...(host.tags ?? []),
  ].some((value) => value.toLowerCase().includes(filter));
}

/** Parse editor input into stable, non-empty, case-preserving tags. */
export function parseHostTags(input: string): string[] {
  return [...new Set(input.split(",").map((tag) => tag.trim()).filter(Boolean))];
}

/** Return the most recently connected hosts without changing the source list. */
export function recentHostProfiles(hosts: Host[], limit = 5): Host[] {
  return hosts
    .filter((host) => host.lastConnectedAt > 0)
    .sort((a, b) => b.lastConnectedAt - a.lastConnectedAt)
    .slice(0, limit);
}
