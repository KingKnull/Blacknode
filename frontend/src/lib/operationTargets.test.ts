import assert from "node:assert/strict";
import test from "node:test";
import { createOperationTargetInbox, operationTarget, matchingOperationID, selectableOperationRecords } from "./operationTargets.ts";

test("routes every supported result type to its own panel and consumes exactly once", () => {
  const routes = [
    ["monitor", "uptime"], ["incident", "incidents"], ["deployment", "deployments"],
    ["scan-profile", "security-scans"], ["security-scan", "security-scans"], ["runbook", "runbooks"],
  ] as const;
  for (const [kind, destination] of routes) {
    const inbox = createOperationTargetInbox();
    const view = inbox.request(kind, "record-1");
    assert.equal(view, destination);
    assert.equal(inbox.take(view === "uptime" ? "runbooks" : "uptime"), null);
    assert.deepEqual(inbox.take(destination), { kind, id: "record-1" });
    assert.equal(inbox.take(destination), null);
  }
});

test("replaces older targets, cancels abandoned navigation, and never keeps record bodies", () => {
  const inbox = createOperationTargetInbox();
  inbox.request("scan-profile", "profile");
  inbox.request("security-scan", "scan");
  assert.deepEqual(inbox.take("security-scans"), { kind: "security-scan", id: "scan" });
  inbox.request("deployment", "deleted");
  inbox.discardOutside("activity");
  assert.equal(inbox.take("deployments"), null);
  inbox.request("incident", "missing");
  assert.deepEqual(inbox.take("incidents"), { kind: "incident", id: "missing" });
  // A failed/missing record lookup cannot leave a target for a later visit.
  assert.equal(inbox.take("incidents"), null);
});

test("rejects invalid targets and unsupported navigate-only result kinds", () => {
  for (const id of [null, undefined, 42, "", " ", " padded "]) assert.equal(operationTarget("monitor", id), null);
  assert.equal(operationTarget("activity" as never, "id"), null);
  assert.equal(operationTarget("workspace" as never, "id"), null);
  assert.equal(operationTarget("__proto__" as never, "id"), null);
  const inbox = createOperationTargetInbox();
  inbox.request("runbook", "old");
  assert.equal(inbox.request("runbook", ""), null);
  assert.equal(inbox.take("runbooks"), null);
});

test("rejects malformed and mismatched persisted IDs instead of selecting the wrong body", () => {
  assert.equal(matchingOperationID("a", "b"), false);
  assert.equal(matchingOperationID("", ""), false);
  const records = [
    { id: "correct", body: JSON.stringify({ id: "correct", name: "Saved", revision: 999 }), revision: 3 },
    { id: "wrong", body: JSON.stringify({ id: "other" }), revision: 1 },
    { id: "broken", body: "{", revision: 1 },
    { id: "empty", body: "null", revision: 1 },
  ];
  assert.deepEqual(selectableOperationRecords(records), [{ id: "correct", name: "Saved", revision: 3 }]);
  assert.deepEqual(selectableOperationRecords(null), []);
});
