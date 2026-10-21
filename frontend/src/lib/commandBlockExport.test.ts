import { describe, test } from "node:test";
import assert from "node:assert/strict";
import type { CommandBlock } from "./commandBlocks.ts";
import {
  commandExportFilename, formatCommandExport, isSafeCommandExportFilename,
  selectedCommandBlock, toggleCommandBookmark,
} from "./commandBlockExport.ts";

function block(overrides: Partial<CommandBlock> = {}): CommandBlock {
  return {
    id: "one", command: "printf 'hello'", output: "hello\n", running: false,
    startedAt: Date.UTC(2026, 9, 21), endedAt: Date.UTC(2026, 9, 21, 0, 0, 1),
    exitCode: 0, cwd: "/tmp/example", ...overrides,
  };
}

describe("command export text", () => {
  test("includes command, metadata and captured output in a standalone block", () => {
    assert.equal(formatCommandExport(block(), "block"), [
      "Blacknode command block", "Started: 2026-10-21T00:00:00.000Z",
      "Ended: 2026-10-21T00:00:01.000Z", "Exit code: 0", "Directory: /tmp/example",
      "", "Command:", "printf 'hello'", "", "Output:", "hello\n",
    ].join("\n"));
  });

  test("output-only preserves exact content and omits sensitive command metadata", () => {
    const output = "one\r\ntwo\n\u0000";
    assert.equal(formatCommandExport(block({ output, command: "TOKEN=secret command" }), "output"), output);
    assert.equal(formatCommandExport(block({ output: "" }), "output"), "");
  });

  test("handles missing metadata and invalid dates without inventing success", () => {
    const text = formatCommandExport(block({
      startedAt: NaN, endedAt: Infinity, exitCode: undefined, cwd: undefined, output: "",
    }), "block");
    assert.match(text, /Started: unknown\nEnded: unknown\nExit code: unknown/);
    assert.ok(!text.includes("Directory:"));
    assert.ok(text.endsWith("Output:\n"));
    assert.match(formatCommandExport(block({ exitCode: 127 }), "block"), /Exit code: 127/);
  });

  test("refuses unfinished blocks", () => {
    for (const content of ["block", "output"] as const) {
      assert.throws(() => formatCommandExport(block({ running: true }), content), /complete/);
    }
  });
});

describe("portable export filenames", () => {
  test("uses only a timestamp, never command/path/id text", () => {
    const filename = commandExportFilename(block({ command: "TOKEN=secret", id: "../secret" }));
    assert.equal(filename, "blacknode-command-2026-10-21T00-00-00-000Z.txt");
    assert.ok(isSafeCommandExportFilename(filename));
    for (const startedAt of [NaN, Infinity, 1e20]) {
      assert.ok(isSafeCommandExportFilename(commandExportFilename(block({ startedAt }))));
    }
  });

  test("allows simple custom text basenames", () => {
    for (const name of ["output.txt", "build-log_2.txt", "report.2026.TXT"]) {
      assert.ok(isSafeCommandExportFilename(name), name);
    }
  });

  test("rejects traversal, controls, hidden names, executable extensions and device names", () => {
    for (const name of [
      "", "../output.txt", "/tmp/output.txt", "C:\\output.txt", "a/b.txt", "a\\b.txt",
      ".env.txt", "a..txt", "a.txt ", "a\n.txt", "a\u0000.txt", "a\u202e.txt", "a:b.txt",
      "output.sh", "output.txt.exe", "CON.txt", "nul.more.txt", "COM1.txt", "Lpt9.TXT",
      `${"a".repeat(117)}.txt`,
    ]) assert.equal(isSafeCommandExportFilename(name), false, JSON.stringify(name));
  });
});

describe("bookmark snapshots and export selection", () => {
  test("toggles a completed snapshot without mutating the capture or saved list", () => {
    const original = block();
    const previous: CommandBlock[] = [];
    const saved = toggleCommandBookmark(previous, original);
    assert.equal(previous.length, 0);
    assert.notEqual(saved[0], original);
    original.output = "later output";
    assert.equal(saved[0].output, "hello\n");
    assert.deepEqual(toggleCommandBookmark(saved, original), []);
    assert.equal(saved.length, 1);
    assert.deepEqual(toggleCommandBookmark([], block({ running: true })), []);
  });

  test("selects exactly one completed block from its own source", () => {
    const live = block();
    const saved = block({ output: "saved output" });
    assert.equal(selectedCommandBlock([live], [saved], { source: "blocks", id: live.id }), live);
    assert.equal(selectedCommandBlock([live], [saved], { source: "bookmarks", id: saved.id }), saved);
    assert.equal(selectedCommandBlock([], [saved], { source: "blocks", id: saved.id }), undefined);
    assert.equal(selectedCommandBlock([live], [], { source: "bookmarks", id: saved.id }), undefined);
    assert.equal(selectedCommandBlock([live], [saved], null), undefined);
    assert.equal(selectedCommandBlock([live], [saved], { source: "blocks", id: "missing" }), undefined);
    assert.equal(selectedCommandBlock([block({ running: true })], [], { source: "blocks", id: live.id }), undefined);
    // A bookmarked snapshot survives the live capture's 100-block eviction.
    assert.equal(selectedCommandBlock([], [saved], { source: "bookmarks", id: saved.id }), saved);
  });
});
