// Templates are inert starting points. Keep this allowlist aligned with the Go
// WorkspaceTemplate DTO; do not copy a ServiceWorkspace wholesale across it.
export type SafeTemplateConfig = { name: string; environment: string; kubeNamespace: string };
export type LocalWorkspaceTemplate = { id: string; name: string; description: string; config: SafeTemplateConfig };
export const TEMPLATE_STORAGE_KEY = "blacknode.service-workspace-templates.v2";
export const LEGACY_TEMPLATE_STORAGE_KEY = "blacknode.service-workspace-templates.v1";
const text = (value: unknown, limit: number, fallback = ""): string =>
  typeof value === "string" ? [...value.replace(/[\u0000-\u001f\u007f-\u009f]/g, "").trim()].slice(0, limit).join("") : fallback;
export function safeTemplateConfig(value: unknown): SafeTemplateConfig {
  const source = value && typeof value === "object" ? value as Record<string, unknown> : {};
  const environment = ["dev", "staging", "production"].includes(String(source.environment)) ? String(source.environment) : "dev";
  const namespace = text(source.kubeNamespace, 64);
  return { name: text(source.name, 120), environment, kubeNamespace: /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/.test(namespace) ? namespace : "default" };
}
export function parseLocalTemplates(raw: string): LocalWorkspaceTemplate[] {
  if (raw.length > 2 * 1024 * 1024) throw new Error("Local template data exceeds the migration limit; original data was preserved.");
  const input: unknown = JSON.parse(raw);
  if (!Array.isArray(input) || input.length > 50) throw new Error("Invalid local template catalog; original data was preserved.");
  const seen = new Set<string>();
  return input.map((value) => {
    if (!value || typeof value !== "object" || typeof value.id !== "string" || !value.id.startsWith("local:") || value.id.length > 160 || seen.has(value.id) || !value.config || typeof value.config !== "object") {
      throw new Error("Invalid local template; original data was preserved.");
    }
    const name = text(value.name, 80);
    if (!name) throw new Error("Local template name required; original data was preserved.");
    seen.add(value.id);
    return { id: value.id, name, description: "Local-only starting point.", config: safeTemplateConfig(value.config) };
  });
}
// Write v2 only after the complete legacy catalog validates. Keep v1 untouched
// as a local recovery copy, and never opt any migrated template into sync.
export function readLocalTemplates(storage: Pick<Storage, "getItem" | "setItem">): LocalWorkspaceTemplate[] {
  const current = storage.getItem(TEMPLATE_STORAGE_KEY);
  if (current !== null) return parseLocalTemplates(current);
  const legacy = storage.getItem(LEGACY_TEMPLATE_STORAGE_KEY);
  if (legacy === null) return [];
  const migrated = parseLocalTemplates(legacy);
  storage.setItem(TEMPLATE_STORAGE_KEY, JSON.stringify(migrated));
  return migrated;
}
