export type SyncEntryView = {
  path: string;
  action: string;
  localHash: string;
  remoteHash: string;
  size: number;
};

export type SyncFilter = "all" | "conflicts" | "deletes" | "uploads" | "downloads";

export type SyncSummary = {
  changes: number;
  conflicts: number;
  uploads: number;
  downloads: number;
  deletes: number;
  bytes: number;
};

export function summarizeSyncEntries(entries: SyncEntryView[]): SyncSummary {
  return entries.reduce<SyncSummary>((summary, entry) => {
    summary.changes++;
    summary.bytes += Math.max(0, entry.size || 0);
    if (entry.action === "conflict") summary.conflicts++;
    else if (entry.action === "upload") summary.uploads++;
    else if (entry.action === "download") summary.downloads++;
    else if (entry.action === "delete-local" || entry.action === "delete-remote") summary.deletes++;
    return summary;
  }, { changes: 0, conflicts: 0, uploads: 0, downloads: 0, deletes: 0, bytes: 0 });
}

export function filterSyncEntries(entries: SyncEntryView[], filter: SyncFilter, phrase = ""): SyncEntryView[] {
  const needle = phrase.trim().toLowerCase();
  return entries.filter((entry) => {
    if (needle && !entry.path.toLowerCase().includes(needle)) return false;
    switch (filter) {
      case "conflicts": return entry.action === "conflict";
      case "deletes": return entry.action === "delete-local" || entry.action === "delete-remote";
      case "uploads": return entry.action === "upload";
      case "downloads": return entry.action === "download";
      default: return true;
    }
  });
}

export function syncActionLabel(action: string): string {
  switch (action) {
    case "upload": return "Use local / upload";
    case "download": return "Use remote / download";
    case "delete-local": return "Delete local";
    case "delete-remote": return "Delete remote";
    case "conflict": return "Resolve conflict";
    case "skip": return "Skip";
    default: return action;
  }
}

export function formatSyncSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GiB`;
}
