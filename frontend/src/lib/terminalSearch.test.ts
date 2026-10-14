import assert from "node:assert/strict";
import { test } from "node:test";
import { findTerminalMatches } from "./terminalSearch.ts";

function makeBuffer(lines: string[]) {
  return {
    length: lines.length,
    getLine(row: number) {
      const text = lines[row];
      if (text === undefined) return undefined;
      return {
        length: text.length,
        getCell(column: number) {
          const char = text[column];
          if (char === undefined) return undefined;
          return {
            getChars: () => char === " " ? "" : char,
            getWidth: () => 1,
          };
        },
      };
    },
  };
}

test("search matches case-insensitively across terminal rows", () => {
  assert.deepEqual(findTerminalMatches(makeBuffer(["First prompt", "second FIRST"]), "first"), [
    { row: 0, column: 0, length: 5 },
    { row: 1, column: 7, length: 5 },
  ]);
});

test("search preserves overlapping matches and supports spaces", () => {
  assert.deepEqual(findTerminalMatches(makeBuffer(["banana"]), "ana"), [
    { row: 0, column: 1, length: 3 },
    { row: 0, column: 3, length: 3 },
  ]);
  assert.deepEqual(findTerminalMatches(makeBuffer(["a b"]), " "), [
    { row: 0, column: 1, length: 1 },
  ]);
});

test("search handles wide and multi-code-unit graphemes by terminal columns", () => {
  const buffer = {
    length: 1,
    getLine() {
      return {
        length: 4,
        getCell(column: number) {
          const cell = [
            { text: "A", width: 1 },
            { text: "界", width: 2 },
            { text: "", width: 0 },
            { text: "B", width: 1 },
          ][column];
          return cell && {
            getChars: () => cell.text,
            getWidth: () => cell.width,
          };
        },
      };
    },
  };
  assert.deepEqual(findTerminalMatches(buffer, "界b"), [
    { row: 0, column: 1, length: 3 },
  ]);
});

test("search returns no matches for an empty query or missing buffer", () => {
  assert.deepEqual(findTerminalMatches(undefined, "x"), []);
  assert.deepEqual(findTerminalMatches(makeBuffer(["text"]), ""), []);
});
