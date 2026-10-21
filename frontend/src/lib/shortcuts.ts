export const SHORTCUT_STORAGE_KEY = "blacknode.shortcuts.v1";
export const SHORTCUT_SCHEMA_VERSION = 1;

export const SHORTCUT_ACTIONS = ["newTab", "closeTab", "palette", "focus", "sidebar"] as const;
export type ShortcutAction = (typeof SHORTCUT_ACTIONS)[number];

export type ShortcutChord = {
  key: string;
  ctrl: boolean;
  meta: boolean;
  alt: boolean;
  shift: boolean;
};

export type ShortcutConfig = Record<ShortcutAction, ShortcutChord>;

type StorageLike = Pick<Storage, "getItem" | "setItem" | "removeItem">;
type StoredShortcutPayload = {
  version: number;
  shortcuts: Partial<Record<ShortcutAction, unknown>>;
};

const mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

const MODIFIER_NAMES = new Set(["ctrl", "control", "cmd", "command", "meta", "alt", "option", "shift"]);
const KEY_ALIASES: Record<string, string> = {
  esc: "Escape",
  escape: "Escape",
  enter: "Enter",
  return: "Enter",
  tab: "Tab",
  space: "Space",
  " ": "Space",
  backspace: "Backspace",
  delete: "Delete",
  del: "Delete",
  insert: "Insert",
  home: "Home",
  end: "End",
  pageup: "PageUp",
  pagedown: "PageDown",
  arrowup: "ArrowUp",
  arrowdown: "ArrowDown",
  arrowleft: "ArrowLeft",
  arrowright: "ArrowRight",
};

// Ctrl+T and Ctrl+W send shell control characters (transpose and erase-word).
// The app has always used shifted forms on non-macOS so changing a binding to
// either bare control chord would silently eat terminal input.
const UNSAFE_CTRL_KEYS = new Set(["t", "w"]);

function cloneChord(chord: ShortcutChord): ShortcutChord {
  return { ...chord };
}

function cloneConfig(config: ShortcutConfig): ShortcutConfig {
  return Object.fromEntries(SHORTCUT_ACTIONS.map((action) => [action, cloneChord(config[action])])) as ShortcutConfig;
}

export function normalizeShortcutKey(value: unknown): string | null {
  if (typeof value !== "string") return null;
  const raw = value.trim();
  if (!raw) return null;
  const alias = KEY_ALIASES[raw.toLowerCase()];
  if (alias) return alias;
  if (MODIFIER_NAMES.has(raw.toLowerCase())) return null;
  if (raw.length === 1) return /[A-Z]/.test(raw) ? raw.toLowerCase() : raw;
  if (/^F(?:[1-9]|1[0-2])$/i.test(raw)) return raw.toUpperCase();
  if (/^[A-Za-z][A-Za-z0-9]*$/.test(raw)) {
    const lower = raw.toLowerCase();
    if (/^(?:audio|browser|media|launch|print|scroll|pause|caps|num|context|meta)$/.test(lower)) return null;
    return raw[0].toUpperCase() + raw.slice(1).toLowerCase();
  }
  return raw;
}

function hasPrimaryModifier(chord: ShortcutChord): boolean {
  return chord.ctrl || chord.meta || chord.alt;
}

export function normalizeShortcut(value: unknown): ShortcutChord | null {
  if (typeof value === "string") {
    const parts = value.split("+").map((part) => part.trim()).filter(Boolean);
    if (parts.length < 2) return null;
    const keyPart = parts.pop();
    if (!keyPart) return null;
    let ctrl = false;
    let meta = false;
    let alt = false;
    let shift = false;
    for (const part of parts) {
      switch (part.toLowerCase()) {
        case "ctrl":
        case "control": ctrl = true; break;
        case "cmd":
        case "command":
        case "meta": meta = true; break;
        case "alt":
        case "option": alt = true; break;
        case "shift": shift = true; break;
        default: return null;
      }
    }
    return normalizeShortcut({ key: keyPart, ctrl, meta, alt, shift });
  }

  if (Array.isArray(value)) {
    return normalizeShortcut(value.filter((part): part is string => typeof part === "string").join("+"));
  }

  if (!value || typeof value !== "object") return null;
  const record = value as Record<string, unknown>;
  const key = normalizeShortcutKey(record.key);
  if (!key) return null;
  const chord: ShortcutChord = {
    key,
    ctrl: record.ctrl === true,
    meta: record.meta === true,
    alt: record.alt === true,
    shift: record.shift === true,
  };
  if (!hasPrimaryModifier(chord)) return null;
  if (!chord.meta && chord.ctrl && !chord.shift && UNSAFE_CTRL_KEYS.has(key.toLowerCase())) return null;
  return chord;
}

export function shortcutFromKeyboardEvent(event: Pick<KeyboardEvent, "key" | "ctrlKey" | "metaKey" | "altKey" | "shiftKey">): ShortcutChord | null {
  return normalizeShortcut({
    key: event.key,
    ctrl: event.ctrlKey,
    meta: event.metaKey,
    alt: event.altKey,
    shift: event.shiftKey,
  });
}

export function shortcutMatchesEvent(
  event: Pick<KeyboardEvent, "key" | "ctrlKey" | "metaKey" | "altKey" | "shiftKey">,
  shortcut: ShortcutChord,
): boolean {
  const key = normalizeShortcutKey(event.key);
  return key === shortcut.key &&
    event.ctrlKey === shortcut.ctrl &&
    event.metaKey === shortcut.meta &&
    event.altKey === shortcut.alt &&
    event.shiftKey === shortcut.shift;
}

export function shortcutSignature(shortcut: ShortcutChord): string {
  return [shortcut.ctrl ? "C" : "", shortcut.meta ? "M" : "", shortcut.alt ? "A" : "", shortcut.shift ? "S" : "", shortcut.key].join("+");
}

export type ShortcutConflict = { actions: ShortcutAction[]; shortcut: ShortcutChord };

export function findShortcutConflicts(config: Partial<ShortcutConfig>): ShortcutConflict[] {
  const bySignature = new Map<string, { actions: ShortcutAction[]; shortcut: ShortcutChord }>();
  for (const action of SHORTCUT_ACTIONS) {
    const shortcut = config[action];
    if (!shortcut) continue;
    const normalized = normalizeShortcut(shortcut);
    if (!normalized) continue;
    const signature = shortcutSignature(normalized);
    const found = bySignature.get(signature);
    if (found) found.actions.push(action);
    else bySignature.set(signature, { actions: [action], shortcut: normalized });
  }
  return [...bySignature.values()].filter((conflict) => conflict.actions.length > 1);
}

const defaultConfig = (): ShortcutConfig => ({
  newTab: normalizeShortcut(mac ? "Cmd+T" : "Ctrl+Shift+T")!,
  closeTab: normalizeShortcut(mac ? "Cmd+W" : "Ctrl+Shift+W")!,
  palette: normalizeShortcut(mac ? "Cmd+K" : "Ctrl+K")!,
  focus: normalizeShortcut("Ctrl+Shift+Z")!,
  sidebar: normalizeShortcut("Ctrl+Shift+H")!,
});

export const DEFAULT_SHORTCUTS: ShortcutConfig = defaultConfig();

// Kept in the original array form for callers that used the old static
// constant. Live UI reads getShortcuts() so customized values still update.
export const APP_SHORTCUTS: Record<ShortcutAction, string[]> = {
  newTab: mac ? ["⌘", "T"] : ["Ctrl", "Shift", "T"],
  closeTab: mac ? ["⌘", "W"] : ["Ctrl", "Shift", "W"],
  palette: [mac ? "⌘" : "Ctrl", "K"],
  focus: ["Ctrl", "Shift", "Z"],
  sidebar: ["Ctrl", "Shift", "H"],
};

function parseStoredShortcut(value: unknown): ShortcutChord | null {
  return normalizeShortcut(value);
}

export function migrateShortcutStorage(value: unknown): StoredShortcutPayload {
  if (!value || typeof value !== "object") return { version: SHORTCUT_SCHEMA_VERSION, shortcuts: {} };
  const record = value as Record<string, unknown>;
  const source = record.shortcuts && typeof record.shortcuts === "object" ? record.shortcuts : record;
  const shortcuts: Partial<Record<ShortcutAction, unknown>> = {};
  for (const action of SHORTCUT_ACTIONS) {
    if (Object.prototype.hasOwnProperty.call(source, action)) shortcuts[action] = (source as Record<string, unknown>)[action];
  }
  return { version: SHORTCUT_SCHEMA_VERSION, shortcuts };
}

export function sanitizeShortcuts(value: unknown, defaults: ShortcutConfig = DEFAULT_SHORTCUTS): ShortcutConfig {
  const migrated = migrateShortcutStorage(value);
  const result = cloneConfig(defaults);
  const used = new Set(SHORTCUT_ACTIONS.map((action) => shortcutSignature(result[action])));
  for (const action of SHORTCUT_ACTIONS) {
    const candidate = parseStoredShortcut(migrated.shortcuts[action]);
    if (!candidate) continue;
    const signature = shortcutSignature(candidate);
    const defaultSignature = shortcutSignature(defaults[action]);
    if (signature !== defaultSignature && used.has(signature)) continue;
    used.delete(defaultSignature);
    used.add(signature);
    result[action] = candidate;
  }
  return result;
}

export function serializeShortcuts(config: ShortcutConfig): string {
  return JSON.stringify({ version: SHORTCUT_SCHEMA_VERSION, shortcuts: config });
}

function browserStorage(): StorageLike | null {
  try {
    return typeof localStorage === "undefined" ? null : localStorage;
  } catch {
    return null;
  }
}

export function loadShortcuts(storage: StorageLike | null = browserStorage()): ShortcutConfig {
  if (!storage) return cloneConfig(DEFAULT_SHORTCUTS);
  try {
    const raw = storage.getItem(SHORTCUT_STORAGE_KEY);
    let parsed: unknown = null;
    try { parsed = raw ? JSON.parse(raw) : null; } catch { parsed = null; }
    const sanitized = sanitizeShortcuts(parsed);
    if (raw !== serializeShortcuts(sanitized)) storage.setItem(SHORTCUT_STORAGE_KEY, serializeShortcuts(sanitized));
    return sanitized;
  } catch {
    return cloneConfig(DEFAULT_SHORTCUTS);
  }
}

let currentShortcuts = loadShortcuts();
const listeners = new Set<(shortcuts: ShortcutConfig) => void>();

export function getShortcuts(): ShortcutConfig {
  return cloneConfig(currentShortcuts);
}

export function subscribeShortcuts(listener: (shortcuts: ShortcutConfig) => void): () => void {
  listeners.add(listener);
  listener(getShortcuts());
  return () => listeners.delete(listener);
}

function publishShortcuts(next: ShortcutConfig): void {
  currentShortcuts = cloneConfig(next);
  try { browserStorage()?.setItem(SHORTCUT_STORAGE_KEY, serializeShortcuts(currentShortcuts)); } catch { /* storage is optional */ }
  const snapshot = getShortcuts();
  for (const listener of listeners) listener(snapshot);
}

export type ShortcutUpdateResult =
  | { ok: true; shortcuts: ShortcutConfig }
  | { ok: false; error: "invalid" | "conflict"; conflicts?: ShortcutConflict[] };

export function setShortcut(action: ShortcutAction, value: unknown): ShortcutUpdateResult {
  const shortcut = normalizeShortcut(value);
  if (!shortcut) return { ok: false, error: "invalid" };
  const next = { ...currentShortcuts, [action]: shortcut } as ShortcutConfig;
  const conflicts = findShortcutConflicts(next);
  if (conflicts.length) return { ok: false, error: "conflict", conflicts };
  publishShortcuts(next);
  return { ok: true, shortcuts: getShortcuts() };
}

export function resetShortcuts(): ShortcutConfig {
  const defaults = cloneConfig(DEFAULT_SHORTCUTS);
  publishShortcuts(defaults);
  return getShortcuts();
}

export function shortcutLabel(action: ShortcutAction, config: ShortcutConfig = currentShortcuts): string {
  const shortcut = config[action];
  const parts: string[] = [];
  if (shortcut.meta) parts.push("⌘");
  if (shortcut.ctrl) parts.push("Ctrl");
  if (shortcut.alt) parts.push(mac ? "⌥" : "Alt");
  if (shortcut.shift) parts.push("Shift");
  const displayKey = shortcut.key === "Space"
    ? "Space"
    : shortcut.key.length === 1 && /[a-z]/.test(shortcut.key)
      ? shortcut.key.toUpperCase()
      : shortcut.key;
  parts.push(displayKey);
  return parts.join("+");
}
