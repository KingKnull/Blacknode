// Tests for the plugin panel message bridge.
//
// What is under test is an authentication decision, not a parser: a plugin
// panel is untrusted code in a sandboxed iframe, and the backend permission
// check keys off the plugin id this module resolves. If a panel can influence
// that id, every gate behind it is decorative. So the cases below are mostly
// about what the bridge *refuses*.
//
// Run with `npm test` (node's built-in runner; no framework needed).

import { test, describe } from "node:test";
import assert from "node:assert/strict";

import {
  registerPanelWindow,
  unregisterPanelWindow,
  pluginIDForSource,
  parseHostMessage,
} from "./pluginBridge.ts";

/**
 * Stand-in for an iframe's contentWindow. The registry only ever holds the
 * reference for identity comparison, so a bare object is a faithful double —
 * and it is what lets these tests run without a DOM.
 */
function fakeWindow(): object {
  return {};
}

describe("sender identity", () => {
  test("resolves a registered window to its plugin", () => {
    const win = fakeWindow();
    registerPanelWindow(win, "acme.tools");
    assert.equal(pluginIDForSource(win), "acme.tools");
  });

  test("an unregistered sender is nobody", () => {
    // window.addEventListener("message") fires for every frame on the page and
    // for any window holding a reference to ours, so the default answer for an
    // unknown sender has to be a refusal, not a guess.
    assert.equal(pluginIDForSource(fakeWindow()), null);
  });

  test("a panel cannot claim another plugin's identity", () => {
    // The attack the window-keyed registry exists to stop: `victim` holds
    // wider permissions, and `attacker` puts victim's id in the message body.
    // The body is never consulted, so the attempt resolves to the attacker.
    const victim = fakeWindow();
    const attacker = fakeWindow();
    registerPanelWindow(victim, "privileged.plugin");
    registerPanelWindow(attacker, "sketchy.plugin");

    const forged = parseHostMessage({
      type: "host.notify",
      title: "t",
      body: "b",
      pluginId: "privileged.plugin",
    });
    assert.ok(forged, "message should still parse; the id just isn't taken from it");
    assert.equal(
      (forged as Record<string, unknown>).pluginId,
      undefined,
      "parsed message must not carry a caller-supplied id at all",
    );
    assert.equal(pluginIDForSource(attacker), "sketchy.plugin");
  });

  test("unregistering invalidates immediately, not at GC time", () => {
    // The WeakMap means a dropped iframe eventually stops being valid on its
    // own, but "eventually" is not a security property: between unmount and
    // collection a replaced panel would still speak for its plugin.
    const win = fakeWindow();
    registerPanelWindow(win, "acme.tools");
    unregisterPanelWindow(win);
    assert.equal(pluginIDForSource(win), null);
  });

  test("re-registering a window replaces the owner", () => {
    // PanelRouter's action calls unregister-then-register on update; assert the
    // second registration wins so a swapped panel can't be attributed to the
    // plugin that previously occupied the frame.
    const win = fakeWindow();
    registerPanelWindow(win, "first.plugin");
    registerPanelWindow(win, "second.plugin");
    assert.equal(pluginIDForSource(win), "second.plugin");
  });

  test("a window is never registered under an empty id", () => {
    // Otherwise the window resolves to "", which reads downstream as a plugin
    // whose id is the empty string rather than as an unregistered sender.
    const win = fakeWindow();
    registerPanelWindow(win, "");
    assert.equal(pluginIDForSource(win), null);
  });

  test("registering nothing is a no-op, not a crash", () => {
    // contentWindow is typed nullable; the action must not throw on teardown
    // of a frame that never got one.
    registerPanelWindow(null, "x");
    registerPanelWindow(undefined, "x");
    unregisterPanelWindow(null);
    unregisterPanelWindow(undefined);
  });

  test("non-object sources resolve to null", () => {
    // MessageEvent.source is typed as a union including null, and these values
    // would throw if handed straight to WeakMap.get.
    for (const source of [null, undefined, "acme.tools", 0, 1, true, Symbol("w")]) {
      assert.equal(pluginIDForSource(source), null, `source ${String(source)}`);
    }
  });

  test("a function source is looked up rather than rejected outright", () => {
    // WeakMap accepts any object *or* function as a key. Rejecting functions
    // by typeof would be a hole only if one were ever registered, but the
    // lookup path should agree with the registration path either way.
    const fn = () => {};
    registerPanelWindow(fn, "callable.plugin");
    assert.equal(pluginIDForSource(fn), "callable.plugin");
  });
});

describe("message parsing", () => {
  test("accepts the one method that exists", () => {
    const msg = parseHostMessage({ type: "host.notify", title: "Hi", body: "There" });
    assert.deepEqual(msg, { type: "host.notify", title: "Hi", body: "There" });
  });

  test("rejects an unimplemented host.* method", () => {
    // The allow-list is exact, not a `host.` prefix match: a panel must not be
    // able to reach a method before the host has written a gate for it.
    for (const type of ["host.exec", "host.read", "host.notify.extra", "host."]) {
      assert.equal(parseHostMessage({ type, title: "t", body: "b" }), null, type);
    }
  });

  test("rejects anything not addressed to the host", () => {
    for (const type of ["notify", "hostnotify", "HOST.NOTIFY", "Host.Notify", ""]) {
      assert.equal(parseHostMessage({ type, title: "t", body: "b" }), null, JSON.stringify(type));
    }
  });

  test("rejects non-objects and objects with no type", () => {
    // Vite's HMR client, browser extensions and devtools all post messages
    // through the same window; none of them look like this.
    for (const data of [null, undefined, "host.notify", 42, [], {}, { type: 7 }]) {
      assert.equal(parseHostMessage(data), null, JSON.stringify(data ?? String(data)));
    }
  });

  test("truncates fields to a length a notification can hold", () => {
    const msg = parseHostMessage({
      type: "host.notify",
      title: "a".repeat(5000),
      body: "b".repeat(5000),
    });
    assert.ok(msg);
    assert.equal(msg.title.length, 500);
    assert.equal(msg.body.length, 500);
  });

  test("coerces missing and non-string fields instead of passing them through", () => {
    // These end up in a system notification and in the activity feed. An
    // `undefined` reaching the Wails binding as a string argument is a runtime
    // error at the boundary; normalising here keeps the handler dumb.
    const msg = parseHostMessage({ type: "host.notify" });
    assert.deepEqual(msg, { type: "host.notify", title: "", body: "" });

    const coerced = parseHostMessage({ type: "host.notify", title: 42, body: { a: 1 } });
    assert.ok(coerced);
    assert.equal(typeof coerced.title, "string");
    assert.equal(typeof coerced.body, "string");
  });

  test("does not treat a prototype-polluting payload as a method", () => {
    // postMessage delivers a structured clone, so `__proto__` arrives as an
    // ordinary own property. Asserting the refusal keeps it that way if the
    // type check is ever rewritten as a lookup in an object literal.
    assert.equal(parseHostMessage({ __proto__: { type: "host.notify" } }), null);
  });
});
