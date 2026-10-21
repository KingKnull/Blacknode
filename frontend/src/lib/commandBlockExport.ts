import type { CommandBlock } from "./commandBlocks";

export type CommandExportContent = "block" | "output";
export type CommandExportSelection = { source: "blocks" | "bookmarks"; id: string };

/** Resolve by source and stable id; never export a running or expired block. */
export function selectedCommandBlock(
  blocks: readonly CommandBlock[],
  bookmarks: readonly CommandBlock[],
  selection: CommandExportSelection | null,
): CommandBlock | undefined {
  if (!selection) return undefined;
  return (selection.source === "bookmarks" ? bookmarks : blocks)
    .find((block) => block.id === selection.id && !block.running);
}

/** Memory-only snapshots remain available if terminal capture evicts old blocks. */
export function toggleCommandBookmark(bookmarks: readonly CommandBlock[], block: CommandBlock): CommandBlock[] {
  if (bookmarks.some((saved) => saved.id === block.id)) {
    return bookmarks.filter((saved) => saved.id !== block.id);
  }
  return block.running ? [...bookmarks] : [...bookmarks, { ...block }];
}

function timestamp(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) return "unknown";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "unknown" : date.toISOString();
}

/** Filenames intentionally omit commands, paths, hostnames and block ids. */
export function commandExportFilename(block: CommandBlock): string {
  return `blacknode-command-${timestamp(block.startedAt).replace(/[:.]/g, "-")}.txt`;
}

/** A portable basename, not a path; reject device names and misleading extensions. */
export function isSafeCommandExportFilename(name: string): boolean {
  return name.length <= 120
    && /^[a-z0-9][a-z0-9_-]*(?:\.[a-z0-9_-]+)*\.txt$/i.test(name)
    && !/^(con|prn|aux|nul|com[1-9]|lpt[1-9])\./i.test(name);
}

/** Output-only is exact, including empty output and original line endings. */
export function formatCommandExport(block: CommandBlock, content: CommandExportContent): string {
  if (block.running) throw new Error("Wait for the command block to complete before exporting.");
  if (content === "output") return block.output;
  return [
    "Blacknode command block",
    `Started: ${timestamp(block.startedAt)}`,
    `Ended: ${timestamp(block.endedAt)}`,
    `Exit code: ${block.exitCode ?? "unknown"}`,
    ...(block.cwd ? [`Directory: ${block.cwd}`] : []),
    "", "Command:", block.command, "", "Output:", block.output,
  ].join("\n");
}
