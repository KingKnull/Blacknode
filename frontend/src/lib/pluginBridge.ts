// Registry that authenticates plugin panel messages by their sending window.
//
// Plugin panels are `<iframe sandbox="allow-scripts" srcdoc=...>`, so a panel
// runs untrusted code that can postMessage the host. The bridge used to take
// the plugin id from the message body, which meant any panel could name any
// plugin — including one it isn't, whose permissions are wider, or one that
// isn't installed at all. Since the backend gate keys off that id, a
// self-asserted id reduces the whole permission model to an honour system.
//
// The fix is to derive the id from *which window* sent the message. A panel
// registers its contentWindow when it mounts; the bridge looks the
// MessageEvent's `source` up here and drops anything unregistered. A panel
// cannot forge `source` — the browser sets it — and cannot obtain another
// panel's window, because only one panel is mounted at a time and the
// sandboxed frames share no origin.
//
// Origin checking is not an option here: a sandbox without allow-same-origin
// has an opaque origin, so `event.origin` is the string "null" for every
// panel. Identity has to come from the window reference.

/**
 * WindowLike is the surface actually used, rather than `Window`: it keeps the
 * registry testable without a DOM, and documents that nothing here touches
 * the sending window beyond holding a reference for identity comparison.
 */
export type WindowLike = object;

// A WeakMap so unmounting a panel doesn't require the caller to be perfect
// about cleanup — a dropped iframe becomes unreachable and its entry goes
// with it. `unregister` still exists for the mount/unmount path, because
// until GC runs the entry is live, and a stale window must not stay valid.
const owners = new WeakMap<WindowLike, string>();

/** Associate a panel's window with the plugin that owns it. */
export function registerPanelWindow(win: WindowLike | null | undefined, pluginID: string): void {
  // An empty id would register a window that then resolves to "", which
  // reads as a plugin whose id is the empty string rather than as a refusal.
  if (!win || !pluginID) return;
  owners.set(win, pluginID);
}

/** Forget a panel's window, e.g. when the panel unmounts. */
export function unregisterPanelWindow(win: WindowLike | null | undefined): void {
  if (!win) return;
  owners.delete(win);
}

/**
 * Resolve the plugin id that owns a message's sender, or null if the sender
 * is not a registered panel window.
 *
 * Returning null for an unknown sender is the point: `window.addEventListener
 * ("message")` receives messages from every frame on the page and from any
 * window holding a reference to ours, not just from plugin panels.
 */
export function pluginIDForSource(source: unknown): string | null {
  if (!source || (typeof source !== "object" && typeof source !== "function")) {
    return null;
  }
  return owners.get(source as WindowLike) ?? null;
}

/** The message shape a panel may send. */
export type HostMessage = { type: string; title: string; body: string };

/**
 * Validate an inbound panel message, returning the normalised form or null.
 *
 * Kept separate from the event handling so the parsing rules are testable and
 * so the handler has no room to improvise: an unrecognised `type` returns
 * null rather than falling through to a default.
 */
export function parseHostMessage(data: unknown): HostMessage | null {
  if (!data || typeof data !== "object") return null;
  const d = data as Record<string, unknown>;
  if (typeof d.type !== "string" || !d.type.startsWith("host.")) return null;

  // Allow-list, not a prefix match. `host.` is how a panel addresses the host,
  // but only these methods exist; anything else is dropped so a future
  // `host.exec` cannot be reached before the host implements a gate for it.
  if (d.type !== "host.notify") return null;

  return {
    type: d.type,
    // Truncated because these render in a system notification, which is not a
    // place a plugin gets to put a megabyte of text.
    title: clamp(d.title),
    body: clamp(d.body),
  };
}

const MAX_FIELD = 500;

function clamp(v: unknown): string {
  const s = typeof v === "string" ? v : v == null ? "" : String(v);
  return s.length > MAX_FIELD ? s.slice(0, MAX_FIELD) : s;
}
