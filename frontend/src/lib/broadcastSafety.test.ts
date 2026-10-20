import assert from "node:assert/strict";
import { test } from "node:test";
import { evaluateBroadcastInput } from "./broadcastSafety.ts";
import { checkCommand } from "./danger.ts";

test("broadcast holds a dangerous command before Enter is forwarded", () => {
  const staged = evaluateBroadcastInput("mkfs /dev/sda", "\r", checkCommand);
  assert.equal(staged.forward, false);
  assert.equal(staged.command, "mkfs /dev/sda");
  assert.equal(staged.danger?.level, "block-without-confirm");
  assert.equal(staged.line, "", "the held line is no longer eligible for a second submit");
});

test("safe broadcast Enter forwards and clears the staged line", () => {
  const staged = evaluateBroadcastInput("printf ok", "\n", checkCommand);
  assert.deepEqual(staged, { line: "", forward: true });
});

test("a pasted dangerous command is held before its embedded Enter", () => {
  const staged = evaluateBroadcastInput("", "mkfs /dev/sda\r", checkCommand);
  assert.equal(staged.forward, false);
  assert.equal(staged.command, "mkfs /dev/sda");
});

test("line tracking handles edits and clears shell control sequences", () => {
  let line = "rebooot";
  line = evaluateBroadcastInput(line, "\x7f", checkCommand).line;
  assert.equal(line, "rebooo");
  line = evaluateBroadcastInput(line, "\x15", checkCommand).line;
  assert.equal(line, "");
});
