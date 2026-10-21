import { describe, test } from "node:test";
import assert from "node:assert/strict";

import {
  DEFAULT_SHORTCUTS,
  findShortcutConflicts,
  migrateShortcutStorage,
  normalizeShortcut,
  sanitizeShortcuts,
  serializeShortcuts,
  shortcutLabel,
  shortcutMatchesEvent,
  type ShortcutConfig,
} from "./shortcuts.ts";

describe("shortcut parsing and normalization", () => {
  test("parses modifier labels and canonicalizes keys", () => {
    assert.deepEqual(normalizeShortcut("ctrl + shift + T"), {
      key: "t", ctrl: true, meta: false, alt: false, shift: true,
    });
    assert.deepEqual(normalizeShortcut(["Cmd", "K"]), {
      key: "k", ctrl: false, meta: true, alt: false, shift: false,
    });
    assert.deepEqual(normalizeShortcut({ key: "escape", ctrl: true }), {
      key: "Escape", ctrl: true, meta: false, alt: false, shift: false,
    });
  });

  test("requires a command modifier and protects shell control chords", () => {
    assert.equal(normalizeShortcut("t"), null);
    assert.equal(normalizeShortcut("Shift+T"), null);
    assert.equal(normalizeShortcut("Ctrl+T"), null);
    assert.equal(normalizeShortcut("Ctrl+W"), null);
    assert.ok(normalizeShortcut("Ctrl+Shift+T"));
    assert.ok(normalizeShortcut("Ctrl+K"));
  });

  test("event matching requires the complete chord", () => {
    const shortcut = normalizeShortcut("Ctrl+Shift+T")!;
    assert.equal(shortcutMatchesEvent({ key: "T", ctrlKey: true, metaKey: false, altKey: false, shiftKey: true }, shortcut), true);
    assert.equal(shortcutMatchesEvent({ key: "t", ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }, shortcut), false);
  });
});

describe("shortcut conflicts and persistence", () => {
  test("finds duplicate normalized chords", () => {
    const config = {
      ...DEFAULT_SHORTCUTS,
      newTab: normalizeShortcut("Ctrl+Shift+Q")!,
      closeTab: normalizeShortcut("ctrl+shift+q")!,
    };
    const conflicts = findShortcutConflicts(config);
    assert.deepEqual(conflicts[0]?.actions, ["newTab", "closeTab"]);
  });

  test("migrates a legacy top-level map and sanitizes malformed values", () => {
    const migrated = migrateShortcutStorage({ newTab: ["Ctrl", "Shift", "N"], palette: { key: "K", ctrl: true }, unknown: "ignored" });
    assert.equal(migrated.version, 1);
    assert.deepEqual(migrated.shortcuts.newTab, ["Ctrl", "Shift", "N"]);
    const safe = sanitizeShortcuts({ shortcuts: { newTab: ["Ctrl", "Shift", "N"], closeTab: "Ctrl+T", palette: { nope: true } } });
    assert.equal(shortcutLabel("newTab", safe), "Ctrl+Shift+N");
    assert.equal(shortcutLabel("closeTab", safe), shortcutLabel("closeTab", DEFAULT_SHORTCUTS));
    assert.equal(shortcutLabel("palette", safe), shortcutLabel("palette", DEFAULT_SHORTCUTS));
  });

  test("serialized data has an explicit schema version", () => {
    const parsed = JSON.parse(serializeShortcuts(DEFAULT_SHORTCUTS)) as { version: number; shortcuts: ShortcutConfig };
    assert.equal(parsed.version, 1);
    assert.deepEqual(parsed.shortcuts, DEFAULT_SHORTCUTS);
  });
});
