import { test } from "node:test";
import assert from "node:assert/strict";
import { fileDiff } from "./fileDiff.ts";

test("shows exact removed and added lines including trailing newline changes", () => {
  const result = fileDiff("listen=80\nroot=/old\n", "listen=80\nroot=/new");
  assert.deepEqual(result.lines.filter((l) => l.kind === "removed").map((l) => l.text), ["root=/old", ""]);
  assert.deepEqual(result.lines.filter((l) => l.kind === "added").map((l) => l.text), ["root=/new"]);
  assert.equal(fileDiff("same", "same").lines.length, 0);
});

test("bounds large previews without silently omitting changed lines", () => {
  const result = fileDiff(Array(2000).fill("old").join("\n"), Array(2000).fill("new").join("\n"));
  assert.equal(result.lines.length, 1200);
  assert.equal(result.omitted, 2800);
});
