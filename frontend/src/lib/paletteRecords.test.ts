import assert from "node:assert/strict";
import test from "node:test";
import { boundedPaletteRecords, parsePaletteRecord, redactPaletteText } from "./paletteRecords.ts";

test("redacts credentials and bounds searchable record text", () => {
  const value = redactPaletteText("https://alice:password@example.test/api token=abc123 " + "x".repeat(200), 80);
  assert.equal(value.includes("password"), false);
  assert.equal(value.includes("abc123"), false);
  assert.equal(value.length, 80);
});

test("rejects invalid or oversized operation record bodies", () => {
  assert.deepEqual(parsePaletteRecord<{ name: string }>(JSON.stringify({ name: "Payments" })), { name: "Payments" });
  assert.equal(parsePaletteRecord("[]"), null);
  assert.equal(parsePaletteRecord("{"), null);
  assert.equal(parsePaletteRecord("x".repeat(256 * 1024 + 1)), null);
});

test("bounds persisted results before they become palette actions", () => {
  assert.deepEqual(boundedPaletteRecords([1, 2, 3], 2), [1, 2]);
  assert.deepEqual(boundedPaletteRecords(null), []);
});
