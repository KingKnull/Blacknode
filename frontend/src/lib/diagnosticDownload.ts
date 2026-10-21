export const DIAGNOSTIC_MAX_BYTES = 8192;

export function validateDiagnosticPreview(text: string): void {
  if (!text || new TextEncoder().encode(text).length > DIAGNOSTIC_MAX_BYTES) {
    throw new Error("Diagnostic preview was empty or exceeded the 8 KiB limit.");
  }
  const parsed: unknown = JSON.parse(text);
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("Diagnostic preview must be a JSON object.");
  }
}

export function saveDiagnosticPreview(
  text: string,
  reviewed: boolean,
  download: (name: string, text: string, type: string) => void,
): boolean {
  if (!reviewed || !text) return false;
  validateDiagnosticPreview(text);
  download("blacknode-diagnostics.json", text, "application/json;charset=utf-8");
  return true;
}
