import assert from "node:assert/strict";
import fs from "node:fs/promises";

// Uses Chrome's built-in debugging protocol; no browser-test dependency.
// Run only against an audit instance with a temporary XDG_DATA_HOME.
const baseURL = process.env.BLACKNODE_AUDIT_URL ?? "http://127.0.0.1:9247";
const debugURL = process.env.BLACKNODE_CHROME_URL ?? "http://127.0.0.1:9333";
const targets = await (await fetch(`${debugURL}/json/list`)).json();
const target = targets.find((t) => t.type === "page" && (t.url.startsWith(baseURL) || t.url === "about:blank"));
assert(target, "Open a dedicated audit Chrome tab first");
const socket = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((resolve, reject) => {
  socket.addEventListener("open", resolve, { once: true });
  socket.addEventListener("error", reject, { once: true });
});
let sequence = 0;
const pending = new Map();
const exceptions = [];
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
function call(method, params = {}) {
  return new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`Timed out: ${method}`)); }, 20000);
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
  for (let i = 0; i < 100; i++) {
    if (await evaluate(expression)) return;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Condition not reached: ${expression}`);
}

try {
  await call("Runtime.enable");
  await call("Page.enable");
  await call("Emulation.setDeviceMetricsOverride", { width: 1280, height: 820, deviceScaleFactor: 1, mobile: false });
  await call("Page.navigate", { url: baseURL });
  await waitFor("document.querySelector('input[type=password]') || document.querySelector('nav')");
  await evaluate(`(async () => {
    const inputs = [...document.querySelectorAll('input[type=password]')];
    if (!inputs.length) return;
    for (const input of inputs) { input.value = 'blacknode-local-audit-only'; input.dispatchEvent(new Event('input', { bubbles: true })); }
    const remember = document.querySelector('#rememberMe');
    if (remember?.checked) remember.click();
    [...document.querySelectorAll('button')].find(b => /^(Create vault|Unlock)$/.test(b.textContent.trim()))?.click();
  })()`);
  await waitFor("!!document.querySelector('nav')");
  await evaluate("import('/src/lib/state.svelte.ts').then(m => { window.auditApp = m.app; })");
  await waitFor("!window.auditApp.loading");

  const views = ['terminals', 'files', 'ops', 'exec', 'runbooks', 'service-workspaces', 'kubernetes', 'fleet', 'incidents', 'deployments', 'investigations', 'team', 'metrics', 'logs', 'processes', 'activity', 'forwards', 'network', 'topology', 'containers', 'database', 'http', 'snippets', 'recordings', 'history', 'plugins', 'keys', 'vault', 'settings'];
  const panels = [];
  for (const theme of ['dark', 'light']) {
  await evaluate(`window.auditApp.settings.theme = ${JSON.stringify(theme)}`);
  for (const width of [1280, 800]) {
    await call('Emulation.setDeviceMetricsOverride', { width, height: width === 800 ? 500 : 820, deviceScaleFactor: 1, mobile: false });
    for (const view of views) {
      await evaluate(`window.auditApp.view = ${JSON.stringify(view)}`);
      await new Promise(resolve => setTimeout(resolve, 140));
      await waitFor("!document.body.innerText.includes('Loading…')");
      const state = await evaluate(`({view:window.auditApp.view,text:document.body.innerText.slice(-250),alerts:[...document.querySelectorAll('[role=alert]')].map(e=>e.textContent.trim()),overflow:document.documentElement.scrollWidth>innerWidth,unlabelled:[...document.querySelectorAll('input,select,textarea')].filter(e=>e.getClientRects().length && e.type!=='hidden' && !e.labels?.length && !e.getAttribute('aria-label') && !e.getAttribute('aria-labelledby')).map(e=>({tag:e.tagName,placeholder:e.placeholder,type:e.type}))})`);
      panels.push({ theme, width, ...state });
      assert(!/Something went wrong|Could not render/.test(state.text), `${view} crashed`);
      assert(!state.overflow, `${view} overflows at ${width}px`);
      assert.deepEqual(state.unlabelled, [], `${view} has unlabeled form controls`);
    }
  }
  }
  await fs.writeFile('/tmp/blacknode-audit-panels.json', JSON.stringify(panels, null, 2));
  console.log(`Rendered ${panels.length} panel/viewport combinations`);

  await evaluate(`(() => {
    window.auditFetch = window.fetch;
    window.auditRequests = [];
    window.auditMutations = [];
    window.auditDeferred = {};
    window.fetch = async (url, options) => {
      if (String(url).endsWith('/wails/runtime') && options?.body) {
        const body = JSON.parse(options.body);
        const reply = (value) => new Response(JSON.stringify(value), {headers:{'Content-Type':'application/json'}});
        if (body.object === 0 && body.args?.methodID === 223589213) {
          window.auditMutations.push(body.args.args);
          await new Promise(resolve => window.auditDeferred.remove = resolve);
          return new Response(JSON.stringify({kind:'RuntimeError',message:'Permission denied by audit fixture'}), {status:500,headers:{'Content-Type':'application/json'}});
        }
        if (body.object === 0 && body.args?.methodID === 1807919904) {
          await new Promise(resolve => window.auditDeferred.pick = resolve);
          return reply(['/tmp/audit-upload.txt']);
        }
        if (body.object === 0 && body.args?.methodID === 3936390275) {
          window.auditEnqueued = body.args.args[0];
          return reply({id:'audit-transfer',request:window.auditEnqueued,status:'queued',total:-1,transferred:0,updatedAt:Date.now()});
        }
        if (body.object === 0 && body.args?.methodID === 353307361) {
          const [host, path] = body.args.args;
          window.auditRequests.push({host,path});
          if (path === '/slow') await new Promise(resolve => window.auditDeferred.slow = resolve);
          const entries = path ? [{name:path === '/slow' ? 'stale.txt' : 'current.txt',isDir:false,size:42,mode:'-rw-------',modTime:0}] : [{name:'folder',isDir:true,size:0,mode:'drwx------',modTime:0}];
          return new Response(JSON.stringify(entries), {headers:{'Content-Type':'application/json'}});
        }
      }
      return window.auditFetch(url,options);
    };
    window.auditApp.hosts = [{id:'audit-host',name:'Audit host',host:'fixture.invalid',username:'ops',protocol:'ssh',tags:[],environment:'dev'}];
    window.auditApp.selectedHostID = 'audit-host';
    window.auditApp.view = 'files';
  })()`);
  await waitFor("!!document.querySelector('button[aria-label=\"Download folder\"]')");
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='folder').click()");
  await waitFor("document.querySelector('[aria-label=\"Remote directory\"]')?.value === 'folder' && document.body.innerText.includes('current.txt')");
  assert.equal(await evaluate("window.auditRequests.length"), 2, 'Directory navigation reset or made duplicate requests');
  async function navigateDirectory(path) {
    await evaluate(`(() => { const input=document.querySelector('[aria-label="Remote directory"]'); input.value=${JSON.stringify(path)}; input.dispatchEvent(new Event('input',{bubbles:true})); input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true})); })()`);
  }
  await navigateDirectory('/slow');
  await waitFor('!!window.auditDeferred.slow');
  await navigateDirectory('/fast');
  await waitFor("document.body.innerText.includes('current.txt')");
  await evaluate('window.auditDeferred.slow()');
  await new Promise(resolve => setTimeout(resolve, 100));
  assert.equal(await evaluate("document.body.innerText.includes('stale.txt')"), false, 'Stale directory response replaced current listing');
  console.log('File navigation and stale-response regressions passed');
  await evaluate(`document.querySelector('button[aria-label="Delete current.txt"]').click()`);
  await waitFor("!!document.querySelector('[role=dialog]')");
  await evaluate(`(() => { const button=[...document.querySelectorAll('[role=dialog] button')].find(b=>b.textContent.includes('Proceed')); button.click(); button.click(); })()`);
  await waitFor('!!window.auditDeferred.remove');
  assert.equal(await evaluate('window.auditMutations.length'), 1, 'Double submit executed a destructive action twice');
  await evaluate('window.auditDeferred.remove()');
  await waitFor(`document.querySelector('[role=dialog] [role=alert]')?.textContent.includes('Permission denied')`);
  await evaluate(`document.querySelector('[role=dialog] button[title=Cancel]').click()`);
  await waitFor("!document.querySelector('[role=dialog]')");
  assert.equal(await evaluate("document.activeElement?.getAttribute('aria-label')"), 'Delete current.txt', 'Dialog failed to restore focus');
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='upload').click()");
  await waitFor('!!window.auditDeferred.pick');
  await navigateDirectory('/changed-while-picking');
  await evaluate('window.auditDeferred.pick()');
  await waitFor('!!window.auditEnqueued');
  assert.equal(await evaluate('window.auditEnqueued.remotePath'), '/fast/audit-upload.txt', 'File picker changed the selected upload destination');
  console.log('Destructive dialog and file-picker destination regressions passed');
  await evaluate("window.auditApp.settings.theme = 'dark'");
  const screenshot = await call("Page.captureScreenshot", { format: "png" });
  await fs.writeFile("/tmp/blacknode-audit-ui.png", Buffer.from(screenshot.data, "base64"));
  assert.deepEqual(exceptions, [], "Uncaught browser exceptions");
} finally { socket.close(); }
