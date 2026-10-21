import assert from "node:assert/strict";
import { test } from "node:test";
import { saveDiagnosticPreview, validateDiagnosticPreview } from "./diagnosticDownload.ts";

test("diagnostic download requires review and saves the exact preview", () => {
  const preview = '{\n  "appVersion": "0.1.0"\n}\n';
  const downloads: string[][] = [];
  const download = (...args: string[]) => downloads.push(args);
  validateDiagnosticPreview(preview);
  assert.equal(downloads.length, 0, "generating a preview must not save it");
  assert.equal(saveDiagnosticPreview(preview, false, download), false);
  assert.equal(saveDiagnosticPreview("", true, download), false);
  assert.equal(downloads.length, 0);
  assert.equal(saveDiagnosticPreview(preview, true, download), true);
  assert.deepEqual(downloads, [["blacknode-diagnostics.json", preview, "application/json;charset=utf-8"]]);
});

test("invalid and oversized JSON cannot be downloaded even after confirmation", () => {
  const download = () => assert.fail("invalid preview must not be downloaded");
  for (const text of ["", "not JSON", "null", "[]", '"value"', JSON.stringify({ data: "é".repeat(4096) })]) {
    assert.throws(() => validateDiagnosticPreview(text));
    if (text) assert.throws(() => saveDiagnosticPreview(text, true, download));
  }
  const exactLimit = JSON.stringify({ data: "a".repeat(8181) });
  assert.equal(new TextEncoder().encode(exactLimit).length, 8192);
  assert.doesNotThrow(() => validateDiagnosticPreview(exactLimit));
});

test("download failures are surfaced to the UI", () => {
  assert.throws(() => saveDiagnosticPreview("{}", true, () => { throw new Error("unavailable"); }), /unavailable/);
});
