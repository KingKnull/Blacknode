// Tests for the command-block duration label.
//
// The label is read while a command is still running, so `now` is injected
// rather than taken from the clock — a test that called Date.now() itself would
// race the implementation and flake on the sub-millisecond boundary.

import { test, describe } from "node:test";
import assert from "node:assert/strict";

import { commandDuration, type CommandBlock } from "./commandBlocks.ts";

function block(overrides: Partial<CommandBlock> = {}): CommandBlock {
  return { id: "b", command: "ls", startedAt: 1000, running: false, output: "", ...overrides };
}

describe("commandDuration", () => {
  test("sub-second durations stay in milliseconds", () => {
    assert.equal(commandDuration(block({ endedAt: 1400 })), "400ms");
    assert.equal(commandDuration(block({ endedAt: 1999 })), "999ms");
  });

  test("a full second switches to the seconds form", () => {
    // 1000ms is the boundary, and it belongs to the seconds branch.
    assert.equal(commandDuration(block({ endedAt: 2000 })), "1.0s");
    assert.equal(commandDuration(block({ endedAt: 13500 })), "12.5s");
  });

  test("a running block measures against now", () => {
    const running = block({ running: true });
    assert.equal(commandDuration(running, 1250), "250ms");
    // Same block, later read — the label has to move.
    assert.equal(commandDuration(running, 4000), "3.0s");
  });

  test("a finished block ignores now", () => {
    assert.equal(commandDuration(block({ endedAt: 1500 }), 99999), "500ms");
  });

  test("a clock that goes backwards clamps to zero rather than printing a negative", () => {
    // endedAt is written from Date.now() on a machine whose clock can be
    // stepped by NTP mid-command, so this is reachable, not theoretical.
    assert.equal(commandDuration(block({ endedAt: 500 })), "0ms");
    assert.equal(commandDuration(block({ running: true }), 0), "0ms");
  });
});
