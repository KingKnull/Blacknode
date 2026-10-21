export const PALETTE_RECORD_LIMIT = 50;
export const PALETTE_ACTIVITY_LIMIT = 100;
const MAX_RECORD_BODY_BYTES = 256 * 1024;

/** Keep searchable labels useful without exposing credentials or unbounded text. */
export function redactPaletteText(value: unknown, limit = 120): string {
  if (value === null || value === undefined) return "";
  const text = String(value)
    .replace(/(https?:\/\/)([^/\s:@]+):([^@\s/]+)@/gi, "$1[redacted]@")
    .replace(/\bBearer\s+[^\s]+/gi, "Bearer [redacted]")
    .replace(
      /\b(password|passwd|token|secret|api[_-]?key|authorization)\b(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;]+)/gi,
      "$1$2[redacted]",
    )
    .replace(/\s+/g, " ")
    .trim();
  return text.slice(0, limit);
}

/** Parse only reasonably-sized object records returned by PlatformService. */
export function parsePaletteRecord<T extends Record<string, unknown>>(body: string): T | null {
  if (typeof body !== "string" || body.length > MAX_RECORD_BODY_BYTES) return null;
  try {
    const parsed: unknown = JSON.parse(body);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed)
      ? (parsed as T)
      : null;
  } catch {
    return null;
  }
}

export function boundedPaletteRecords<T>(records: readonly T[] | null | undefined, limit = PALETTE_RECORD_LIMIT): T[] {
  return Array.isArray(records) ? records.slice(0, limit) : [];
}
