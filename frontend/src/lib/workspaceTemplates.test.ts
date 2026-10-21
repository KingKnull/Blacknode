import { test } from "node:test";
import assert from "node:assert/strict";
import { LEGACY_TEMPLATE_STORAGE_KEY, TEMPLATE_STORAGE_KEY, parseLocalTemplates, readLocalTemplates, safeTemplateConfig } from "./workspaceTemplates.ts";

const legacyFixture = JSON.stringify([{ id: "local:fixture", name: "Payments", description: "token=description-secret", config: {
  name: "Payments production", environment: "production", kubeNamespace: "payments",
  hostIDs: ["host-secret"], kubeHostID: "kube-host-secret", kubeContext: "context-secret", forwardIDs: ["tunnel-secret"], metricHostIDs: ["monitor-secret"],
  logCommand: "curl -H 'Authorization: Bearer command-secret'", dbConnectionID: "db-secret", projectID: "project-secret", stateJSON: "terminal-secret", password: "password-secret",
} }]);

function memoryStorage(initial: Record<string, string> = {}) {
  const data = new Map(Object.entries(initial));
  return { data, getItem: (key: string) => data.get(key) ?? null, setItem: (key: string, value: string) => { data.set(key, value); } };
}

test("v1 migration preserves each local template and a recovery copy without opting into sync", () => {
  const storage = memoryStorage({ [LEGACY_TEMPLATE_STORAGE_KEY]: legacyFixture });
  const templates = readLocalTemplates(storage);
  assert.deepEqual(templates, [{ id: "local:fixture", name: "Payments", description: "Local-only starting point.", config: { name: "Payments production", environment: "production", kubeNamespace: "payments" } }]);
  assert.equal(storage.getItem(LEGACY_TEMPLATE_STORAGE_KEY), legacyFixture);
  const saved = storage.getItem(TEMPLATE_STORAGE_KEY)!;
  assert.doesNotMatch(saved, /secret|hostIDs|forwardIDs|logCommand|stateJSON|password|kubeContext|projectID|dbConnectionID/);
  assert.deepEqual(readLocalTemplates(storage), templates);
  storage.setItem(TEMPLATE_STORAGE_KEY, "[]");
  assert.deepEqual(readLocalTemplates(storage), []); // never resurrect a deleted v2 catalog from v1
});

test("failed or malformed migration never overwrites either catalog", () => {
  for (const raw of ["{", "{}", "[null]", '[{"id":"builtin:injected","name":"bad","config":{}}]', JSON.stringify(Array(51).fill(JSON.parse(legacyFixture)[0])), JSON.stringify([JSON.parse(legacyFixture)[0], JSON.parse(legacyFixture)[0]]), " ".repeat(2 * 1024 * 1024 + 1)]) {
    const storage = memoryStorage({ [LEGACY_TEMPLATE_STORAGE_KEY]: raw });
    assert.throws(() => readLocalTemplates(storage));
    assert.equal(storage.getItem(LEGACY_TEMPLATE_STORAGE_KEY), raw);
    assert.equal(storage.getItem(TEMPLATE_STORAGE_KEY), null);
  }
  const storage = memoryStorage({ [LEGACY_TEMPLATE_STORAGE_KEY]: legacyFixture });
  assert.throws(() => readLocalTemplates({ getItem: storage.getItem, setItem: () => { throw new Error("quota"); } }), /quota/);
  assert.equal(storage.getItem(LEGACY_TEMPLATE_STORAGE_KEY), legacyFixture);
  assert.equal(storage.getItem(TEMPLATE_STORAGE_KEY), null);
  storage.setItem(TEMPLATE_STORAGE_KEY, "invalid");
  assert.throws(() => readLocalTemplates(storage));
  assert.equal(storage.getItem(TEMPLATE_STORAGE_KEY), "invalid");
});

test("allowlist redacts device-specific configuration from new saves and imported templates", () => {
  const untrusted = JSON.parse(legacyFixture)[0].config;
  const safe = safeTemplateConfig(untrusted);
  assert.deepEqual(safe, { name: "Payments production", environment: "production", kubeNamespace: "payments" });
  assert.deepEqual(safeTemplateConfig(safe), safe);
  const blank = { id: "", hostIDs: [], forwardIDs: [], logCommand: "", stateJSON: "", kubeContext: "", kubeHostID: "", dbConnectionID: "", projectID: "" };
  assert.deepEqual({ ...blank, ...safe }, { ...blank, name: "Payments production", environment: "production", kubeNamespace: "payments" });
});

test("bounded names, environment and DNS namespace are normalized safely", () => {
  const config = safeTemplateConfig({ name: "\u0000" + "😀".repeat(130), environment: "$(command)", kubeNamespace: "default; command" });
  assert.equal([...config.name].length, 120);
  assert.equal(config.environment, "dev");
  assert.equal(config.kubeNamespace, "default");
  for (const namespace of ["a", "a".repeat(63), "a-b", "0"]) assert.equal(safeTemplateConfig({ kubeNamespace: namespace }).kubeNamespace, namespace);
  for (const namespace of ["a".repeat(64), "-a", "a-", "UPPER", "a.b", "../secret"]) assert.equal(safeTemplateConfig({ kubeNamespace: namespace }).kubeNamespace, "default");
  assert.equal(parseLocalTemplates(JSON.stringify([{ id: "local:one", name: "X".repeat(81), config: {} }]))[0].name.length, 80);
});
