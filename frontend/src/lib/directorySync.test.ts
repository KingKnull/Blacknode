import assert from "node:assert/strict";
import test from "node:test";
import { filterSyncEntries, summarizeSyncEntries, syncActionLabel } from "./directorySync.ts";

const entries = [
  { path: "changed.txt", action: "upload", localHash: "a", remoteHash: "b", size: 2048 },
  { path: "new/config.yml", action: "download", localHash: "", remoteHash: "c", size: 10 },
  { path: "old.log", action: "delete-remote", localHash: "d", remoteHash: "", size: 4 },
  { path: "both.txt", action: "conflict", localHash: "e", remoteHash: "f", size: 12 },
];

test("directory sync summary counts operations and reviewed bytes", () => {
  assert.deepEqual(summarizeSyncEntries(entries), {
    changes: 4, conflicts: 1, uploads: 1, downloads: 1, deletes: 1, bytes: 2074,
  });
});

test("directory sync review filters preserve path matching", () => {
  assert.deepEqual(filterSyncEntries(entries, "deletes").map((entry) => entry.path), ["old.log"]);
  assert.deepEqual(filterSyncEntries(entries, "all", "CONFIG").map((entry) => entry.path), ["new/config.yml"]);
  assert.deepEqual(filterSyncEntries(entries, "conflicts").map((entry) => entry.path), ["both.txt"]);
});

test("directory sync labels make destructive and directional actions explicit", () => {
  assert.equal(syncActionLabel("delete-remote"), "Delete remote");
  assert.equal(syncActionLabel("upload"), "Use local / upload");
});
