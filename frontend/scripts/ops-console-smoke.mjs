import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";

// Run with a dedicated Chrome debugging instance on port 9333:
// node scripts/ops-console-smoke.mjs
// Mounts only OpsConsole with an in-memory transport. No backend or vault is used.
const fixtureID = "\0ops-console-fixture";
const server = await createServer({
  root: fileURLToPath(new URL("../", import.meta.url)),
  configFile: false,
  plugins: [svelte(), tailwindcss(), {
    name: "ops-console-fixture",
    resolveId(id) { if (id === "virtual:ops-console-fixture") return fixtureID; },
    load(id) {
      if (id !== fixtureID) return;
      return `
        import { mount } from 'svelte';
        import { setTransport } from '@wailsio/runtime';
        import OpsConsole from '/src/lib/OpsConsole.svelte';
        import { app } from '/src/lib/state.svelte.ts';
        import '/src/app.css';
        const fixture = window.fixture = { calls: [], unexpected: [], failOnce: false };
        setTransport({ call: async (objectID, method, windowName, args) => {
          if (objectID !== 0 || args.methodID !== 1863424458) {
            fixture.unexpected.push({ objectID, method, args });
            throw new Error('Unexpected backend call');
          }
          const [runID, command, hostIDs] = args.args;
          fixture.calls.push({ runID, command, hostIDs });
          return hostIDs.map((hostID) => {
            const failed = fixture.failOnce && runID.endsWith(':1') && hostID === 'host-b';
            if (failed) fixture.failOnce = false;
            return { hostID, exitCode: failed ? 1 : 0, error: '', stderr: '',
              stdout: 'call ' + fixture.calls.length + ': ' + command + ' on ' + hostID };
          });
        }});
        app.hosts = ['host-a', 'host-b'].map(id => ({
          id, name: 'Internet-Load-Balancer-01', host: 'fixture.invalid',
          protocol: 'ssh', environment: 'production', tags: [],
        }));
        mount(OpsConsole, { target: document.getElementById('app') });
      `;
    },
    configureServer(vite) {
      vite.middlewares.use(async (req, res, next) => {
        if (req.url !== "/ops-console-test.html") return next();
        const html = await vite.transformIndexHtml(req.url, `<!doctype html>
          <html><head><title>Ops console regression</title></head>
          <body><div id="app" style="height:100vh"></div>
          <script type="module" src="/@id/virtual:ops-console-fixture"></script></body></html>`);
        res.setHeader("Content-Type", "text/html");
        res.end(html);
      });
    },
  }],
  server: { host: "127.0.0.1", port: 0 },
});

const debugURL = process.env.BLACKNODE_CHROME_URL ?? "http://127.0.0.1:9333";
let socket;
let target;
let sequence = 0;
const pending = new Map();
const exceptions = [];

function call(method, params = {}) {
  return new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error(`Timed out: ${method}`));
    }, 20000);
    pending.set(id, { resolve, reject, timer });
    socket.send(JSON.stringify({ id, method, params }));
  });
}

async function evaluate(expression) {
  const result = await call("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
  return result.result?.value;
}

async function waitFor(expression) {
  for (let i = 0; i < 150; i++) {
    assert.deepEqual(exceptions, [], "UI raised a runtime exception");
    if (await evaluate(expression)) return;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`Condition not reached: ${expression}`);
}

async function click(label) {
  await evaluate(`(() => {
    const button = [...document.querySelectorAll('button')].find(b => b.textContent.trim() === ${JSON.stringify(label)});
    if (!button || button.disabled) throw new Error('Button unavailable: ' + ${JSON.stringify(label)});
    button.click();
  })()`);
}

async function enterCommands(value) {
  await evaluate(`(() => {
    const input = document.querySelector('textarea');
    input.value = ${JSON.stringify(value)};
    input.dispatchEvent(new Event('input', { bubbles: true }));
  })()`);
}

try {
  await server.listen();
  const baseURL = server.resolvedUrls.local[0];
  target = await (await fetch(`${debugURL}/json/new?about:blank`, { method: "PUT" })).json();
  socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", reject, { once: true });
  });
  socket.addEventListener("message", ({ data }) => {
    const message = JSON.parse(data);
    if (message.method === "Runtime.exceptionThrown") exceptions.push(message.params.exceptionDetails);
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id);
    clearTimeout(request.timer);
    if (message.error) request.reject(new Error(JSON.stringify(message.error)));
    else request.resolve(message.result);
  });
  await call("Runtime.enable");
  await call("Page.enable");
  await call("Emulation.setDeviceMetricsOverride", { width: 1280, height: 820, deviceScaleFactor: 1, mobile: false });
  await call("Page.navigate", { url: `${baseURL}ops-console-test.html` });
  await waitFor("document.querySelectorAll('tbody tr').length === 2");

  await click("Run matrix");
  await waitFor("!!document.querySelector('[role=dialog]')");
  assert.equal(await evaluate("[...document.querySelectorAll('[role=dialog] span')].filter(e => e.textContent === 'Internet-Load-Balancer-01').length"), 2);
  assert.equal(await evaluate("fixture.calls.length"), 0, "Commands ran before confirmation");
  await click("Cancel");
  await waitFor("!document.querySelector('[role=dialog]')");
  assert.equal(await evaluate("fixture.calls.length"), 0, "Cancel executed commands");
  await click("Run matrix");
  await click("Proceed");
  await waitFor("fixture.calls.length === 3 && !document.querySelector('[role=dialog]') && [...document.querySelectorAll('tbody button')].every(b => b.textContent.trim() === 'exit 0')");
  assert.deepEqual(await evaluate("fixture.calls.map(c => [c.command, c.hostIDs])"),
    ["uptime", "df -h", "free -m"].map(command => [command, ["host-a", "host-b"]]));
  console.log("Duplicate host names, cancellation and confirmed execution passed");

  await evaluate("fixture.calls = []; fixture.failOnce = true");
  await enterCommands("uptime\nuptime\nfree -m");
  await waitFor("[...document.querySelectorAll('thead th')].map(e => e.textContent).join('|') === 'Host|uptime|uptime|free -m'");
  await click("Run matrix");
  await click("Proceed");
  await waitFor("fixture.calls.length === 3 && [...document.querySelectorAll('button')].some(b => b.textContent.trim() === 'Retry failed (1)' && !b.disabled)");
  await evaluate("document.querySelectorAll('tbody tr')[0].querySelectorAll('button')[1].click()");
  await waitFor("document.querySelector('pre')?.textContent === 'call 2: uptime on host-a'");
  await click("Close");
  await click("Retry failed (1)");
  await waitFor("fixture.calls.length === 4 && [...document.querySelectorAll('tbody button')].every(b => b.textContent.trim() === 'exit 0')");
  assert.deepEqual(await evaluate("fixture.calls[3].hostIDs"), ["host-b"]);
  assert.equal(await evaluate("fixture.calls[3].command"), "uptime");
  await evaluate("document.querySelectorAll('tbody tr')[1].querySelectorAll('button')[1].click()");
  await waitFor("document.querySelector('pre')?.textContent === 'call 4: uptime on host-b'");
  await click("Close");
  console.log("Repeated command columns, distinct output and failed-cell retry passed");

  await enterCommands("mkfs /dev/fixture-only");
  await click("Run matrix");
  await waitFor("!!document.querySelector('[role=dialog] input')");
  assert.equal(await evaluate("[...document.querySelectorAll('button')].find(b => b.textContent.trim() === 'Proceed').disabled"), true);
  assert.equal(await evaluate("fixture.calls.length"), 4);
  await evaluate(`(() => {
    const input = document.querySelector('[role=dialog] input');
    input.value = 'destroy production';
    input.dispatchEvent(new Event('input', { bubbles: true }));
  })()`);
  await click("Proceed");
  await waitFor("fixture.calls.length === 5 && !document.querySelector('[role=dialog]')");
  assert.deepEqual(await evaluate("fixture.unexpected"), []);
  assert.deepEqual(exceptions, []);
  console.log("Required confirmation phrase passed; all execution used the fixture transport");
} finally {
  socket?.close();
  if (target) await fetch(`${debugURL}/json/close/${target.id}`);
  await server.close();
}
