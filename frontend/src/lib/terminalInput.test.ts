import assert from "node:assert/strict";
import { test } from "node:test";
import { createTerminalInputQueue } from "./terminalInput.ts";

const microtasks = async () => { await Promise.resolve(); await Promise.resolve(); };

test("slow replies coalesce queued keystrokes without reordering stdin", async () => {
  const calls: string[] = [];
  let complete = () => {};
  const queue = createTerminalInputQueue(data => {
    calls.push(data);
    return new Promise<void>(resolve => { complete = resolve; });
  });
  queue.write("a");
  await microtasks();
  assert.deepEqual(calls, ["a"], "first keystroke needs no timer or animation frame");
  for (const key of ["b", "c", "\x7f", "d", "\r"]) {
    queue.write(key);
    await microtasks(); // Separate input events while the native call is slow.
  }
  assert.deepEqual(calls, ["a"], "only one write may be in flight");
  complete();
  await microtasks();
  assert.deepEqual(calls, ["a", "bc\x7fd\r"]);
  queue.dispose();
  complete();
});

test("paste chunks in the same turn share one write", async () => {
  const calls: string[] = [];
  const queue = createTerminalInputQueue(async data => { calls.push(data); });
  queue.write("\x1b[200~");
  queue.write("hello\n世界");
  queue.write("\x1b[201~");
  await microtasks();
  assert.deepEqual(calls, ["\x1b[200~hello\n世界\x1b[201~"]);
});

test("a large input burst keeps the bridge queue bounded to one pending batch", async () => {
  const calls: string[] = [];
  let complete = () => {};
  const queue = createTerminalInputQueue(data => {
    calls.push(data);
    return new Promise<void>(resolve => { complete = resolve; });
  });
  queue.write("ready");
  await microtasks();

  const burst = Array.from({length: 1000}, (_, i) => String(i % 10)).join("");
  for (const key of burst) queue.write(key);
  await microtasks();
  assert.deepEqual(calls, ["ready"], "a slow bridge must not start one RPC per input event");

  complete();
  await microtasks();
  assert.deepEqual(calls, ["ready", burst], "the pending batch must retain input order");
  queue.dispose();
  complete();
});

test("a failed write is not retried and does not strand later input", async () => {
  const calls: string[] = [];
  let fail = (_error: Error) => {};
  const queue = createTerminalInputQueue(data => {
    calls.push(data);
    return calls.length === 1 ? new Promise<void>((_resolve, reject) => { fail = reject; }) : Promise.resolve();
  });
  queue.write("a");
  await microtasks();
  queue.write("b");
  fail(new Error("write failed"));
  await microtasks();
  assert.deepEqual(calls, ["a", "b"]);
});

test("reset discards the old shell's backlog, disposal drops unsent input", async () => {
  const calls: string[] = [];
  let complete = () => {};
  const queue = createTerminalInputQueue(data => {
    calls.push(data);
    return new Promise<void>(resolve => { complete = resolve; });
  });
  queue.write("old");
  await microtasks();
  queue.write("old backlog");
  queue.reset();
  queue.write("new");
  complete();
  await microtasks();
  assert.deepEqual(calls, ["old", "new"]);
  queue.write("unsent");
  queue.dispose();
  complete();
  queue.write("after disposal");
  await microtasks();
  assert.deepEqual(calls, ["old", "new"]);
});
