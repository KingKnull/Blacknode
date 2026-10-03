export type DiffLine = { kind: "context" | "removed" | "added"; text: string };

// A bounded replacement hunk: shared prefix/suffix are context and the middle
// is a deletion followed by an insertion. Linear time even for large logs.
export function fileDiff(before: string, after: string, limit = 1200): { lines: DiffLine[]; omitted: number } {
  if (before === after) return { lines: [], omitted: 0 };
  const a = before.split("\n");
  const b = after.split("\n");
  let prefix = 0;
  while (prefix < a.length && prefix < b.length && a[prefix] === b[prefix]) prefix++;
  let suffix = 0;
  while (suffix < a.length - prefix && suffix < b.length - prefix && a[a.length - 1 - suffix] === b[b.length - 1 - suffix]) suffix++;
  const lines: DiffLine[] = [];
  let total = 0;
  function add(kind: DiffLine["kind"], text: string) { total++; if (lines.length < limit) lines.push({ kind, text }); }
  for (let i = Math.max(0, prefix - 3); i < prefix; i++) add("context", a[i]);
  for (let i = prefix; i < a.length - suffix; i++) add("removed", a[i]);
  for (let i = prefix; i < b.length - suffix; i++) add("added", b[i]);
  for (let i = b.length - suffix; i < Math.min(b.length, b.length - suffix + 3); i++) add("context", b[i]);
  return { lines, omitted: total - lines.length };
}
