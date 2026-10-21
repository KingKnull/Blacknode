import assert from "node:assert/strict";
import { test } from "node:test";
import type { GhosttyCell } from "ghostty-web";
import { createViewportRenderBuffer, optimizeTerminalRendering } from "./terminalRendering.ts";
import type { Terminal } from "ghostty-web";

function fixture() {
  let cols = 3;
  let cells = Array.from({ length: 9 }, (_, codepoint) => ({ codepoint }) as GhosttyCell);
  let reads = 0;
  const source = {
    getViewport() { reads++; return cells; },
    getLine(row: number) { return cells.slice(row * cols, (row + 1) * cols); },
    getDimensions: () => ({ cols, rows: cells.length / cols }),
    getCursor: () => ({ x: 1, y: 1, visible: true }),
    isRowDirty: (row: number) => row === 1,
    needsFullRedraw: () => true,
    clearDirty() {},
    getGraphemeString: () => "👩‍💻",
  };
  return {
    source,
    reads: () => reads,
    replace(newCols: number, codepoint: number) {
      cols = newCols;
      cells = Array.from({ length: newCols * 3 }, () => ({ codepoint }) as GhosttyCell);
    },
  };
}

test("drawing several rows and the cursor fetches the viewport only once", () => {
  const f = fixture();
  const frame = createViewportRenderBuffer(f.source);
  assert.equal(frame.buffer.getLine(-1), null);
  assert.equal(frame.buffer.getLine(3), null);
  assert.equal(f.reads(), 0);
  for (const row of [0, 1, 2, 1]) {
    assert.deepEqual(frame.buffer.getLine(row)?.map(c => c.codepoint), [row * 3, row * 3 + 1, row * 3 + 2]);
  }
  assert.equal(f.reads(), 1);
  assert.equal(frame.buffer.getLine(1), frame.buffer.getLine(1));
  assert.deepEqual(frame.buffer.getCursor(), f.source.getCursor());
  assert.equal(frame.buffer.needsFullRedraw?.(), true);
  assert.equal(frame.buffer.isRowDirty(1), true);
  assert.equal(frame.buffer.getGraphemeString?.(1, 1), "👩‍💻");
});

test("the next frame sees changed screen contents and resized dimensions", () => {
  const f = fixture();
  const frame = createViewportRenderBuffer(f.source);
  const old = frame.buffer.getLine(1);
  f.replace(4, 65);
  frame.reset();
  assert.deepEqual(frame.buffer.getLine(1)?.map(c => c.codepoint), [65, 65, 65, 65]);
  assert.equal(f.reads(), 2);
  assert.deepEqual(old?.map(c => c.codepoint), [3, 4, 5]);
  frame.reset();
  f.replace(2, 66);
  assert.deepEqual(frame.buffer.getLine(2)?.map(c => c.codepoint), [66, 66]);
  assert.equal(f.reads(), 3);
});

test("renderer wrapping passes other buffers through and restores on cleanup", () => {
  const source = fixture().source;
  let called = false;
  const original = function (buffer: unknown) { assert.equal(buffer, source); called = true; };
  const renderer = { render: original };
  const restore = optimizeTerminalRendering({ renderer } as unknown as Terminal);
  assert.notEqual(renderer.render, original);
  renderer.render(source);
  assert.equal(called, true);
  restore();
  assert.equal(renderer.render, original);
  assert.doesNotThrow(() => optimizeTerminalRendering({} as Terminal)());
});
