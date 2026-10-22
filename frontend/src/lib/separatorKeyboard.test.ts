import { test } from "node:test";
import assert from "node:assert/strict";
import { separatorKeyValue } from "./separatorKeyboard.ts";

const key = (key: string) => ({ key, altKey: false, ctrlKey: false, metaKey: false });

test("vertical separators move left/right; horizontal separators move up/down", () => {
  assert.equal(separatorKeyValue(key("ArrowLeft"), "vertical", 50, 5, 95, 1), 49);
  assert.equal(separatorKeyValue(key("ArrowRight"), "vertical", 50, 5, 95, 1), 51);
  assert.equal(separatorKeyValue(key("ArrowUp"), "horizontal", 50, 5, 95, 1), 49);
  assert.equal(separatorKeyValue(key("ArrowDown"), "horizontal", 50, 5, 95, 1), 51);
  assert.equal(separatorKeyValue(key("ArrowUp"), "vertical", 50, 5, 95, 1), undefined);
  assert.equal(separatorKeyValue(key("ArrowLeft"), "horizontal", 50, 5, 95, 1), undefined);
});

test("pane ratios and sidebar pixels stay bounded, including repeated arrows and Home/End", () => {
  for (const [min, max, step] of [[5, 95, 1], [160, 600, 10]]) {
    assert.equal(separatorKeyValue(key("Home"), "vertical", 252, min, max, step), min);
    assert.equal(separatorKeyValue(key("End"), "vertical", 252, min, max, step), max);
    assert.equal(separatorKeyValue(key("ArrowLeft"), "vertical", min, min, max, step), min);
    assert.equal(separatorKeyValue(key("ArrowRight"), "vertical", max - step / 2, min, max, step), max);
    let value = min;
    for (let i = 0; i < 100; i++) value = separatorKeyValue(key("ArrowRight"), "vertical", value, min, max, step)!;
    assert.equal(value, max);
  }
});

test("Tab, unrelated keys, and modified workspace shortcuts remain unhandled", () => {
  for (const name of ["Tab", "Enter", "Escape", "a"]) {
    assert.equal(separatorKeyValue(key(name), "vertical", 50, 5, 95, 1), undefined);
  }
  for (const modifier of ["altKey", "ctrlKey", "metaKey"] as const) {
    for (const name of ["ArrowRight", "Home", "End"]) {
      assert.equal(separatorKeyValue({ ...key(name), [modifier]: true }, "vertical", 50, 5, 95, 1), undefined);
    }
  }
});
