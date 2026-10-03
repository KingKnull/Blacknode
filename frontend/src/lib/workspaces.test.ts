import { test } from "node:test";
import assert from "node:assert/strict";
import { captureWorkspace, parseWorkspace, readWorkspace, readSavedWorkspaces, restoreWorkspace, type WorkspaceSnapshot } from "./workspaces.ts";

const example: WorkspaceSnapshot = {
  version: 2,
  tabs: [
    { root: { kind: "split", direction: "horizontal", ratio: 0.7, a: { kind: "leaf", target: { hostID: "web", via: "ssh" } }, b: { kind: "split", direction: "vertical", ratio: 0.3, a: { kind: "leaf", target: null }, b: { kind: "leaf", target: { hostID: "db", via: "mosh" } } } }, activeLeaf: 2 },
    { root: { kind: "leaf", target: null }, activeLeaf: 0 },
  ],
  activeTab: 1, view: "files", sidebarWidth: 290, selectedHostID: "web", forwardIDs: ["postgres"],
};

test("round trips nested layouts, hosts, selection and tunnel presets with fresh runtime IDs", () => {
  let id = 0;
  const restored = restoreWorkspace(example, () => `runtime-${++id}`);
  assert.equal(restored.activeTabID, restored.tabs[1].id);
  const captured = captureWorkspace(restored.tabs, restored.activeTabID, restored.targets, example);
  assert.deepEqual(captured, example);
  assert.ok(!JSON.stringify(captured).includes("runtime-"));
  const another = restoreWorkspace(captured, () => `runtime-${++id}`);
  assert.notEqual(another.tabs[0].id, restored.tabs[0].id);
  assert.deepEqual(Object.values(another.targets), Object.values(restored.targets));
});

test("captures intended hosts while disconnected and clears explicit local-shell choices", () => {
  let id = 0;
  const restored = restoreWorkspace(example, () => String(++id));
  const session = Object.keys(restored.targets).find((key) => restored.targets[key]?.hostID === "web")!;
  restored.targets[session] = null;
  const captured = captureWorkspace(restored.tabs, restored.activeTabID, restored.targets, example);
  assert.ok(!JSON.stringify(captured.tabs).includes('"web"'));
});

test("migrates the old session and bounds tab counts", () => {
  const migrated = parseWorkspace({ tabCount: 99999, view: "logs", sidebarWidth: 99999 });
  assert.equal(migrated?.tabs.length, 32);
  assert.equal(migrated?.sidebarWidth, 600);
  assert.equal(migrated?.view, "logs");
  assert.equal(parseWorkspace({ version: 99, tabCount: 3 }), null);
});

test("rejects malformed, excessive and deeply nested layouts", () => {
  assert.equal(parseWorkspace({ ...example, tabs: [] }), null);
  assert.equal(parseWorkspace({ ...example, tabs: Array(33).fill(example.tabs[0]) }), null);
  assert.equal(parseWorkspace({ ...example, tabs: [{ root: { kind: "unknown" } }] }), null);
  let node: any = { kind: "leaf", target: null };
  for (let i = 0; i < 14; i++) node = { kind: "split", direction: "horizontal", a: node, b: { kind: "leaf" } };
  assert.equal(parseWorkspace({ ...example, tabs: [{ root: node }] }), null);
  assert.equal(readWorkspace({ getItem: () => "{" }), null);
  assert.equal(readWorkspace({ getItem: () => { throw new Error("Storage unavailable"); } }), null);
});

test("normalizes untrusted persisted values and deduplicates saved workspaces", () => {
  const parsed = parseWorkspace({ ...example, activeTab: -4, sidebarWidth: NaN, view: "unknown", forwardIDs: ["a", "a", 3, null] });
  assert.equal(parsed?.activeTab, 0);
  assert.equal(parsed?.sidebarWidth, 252);
  assert.equal(parsed?.view, "terminals");
  assert.deepEqual(parsed?.forwardIDs, ["a"]);
  const entry = { id: "one", name: " Dev ", snapshot: example };
  assert.equal(readSavedWorkspaces({ getItem: () => JSON.stringify([entry, entry, null]) }).length, 1);
});
