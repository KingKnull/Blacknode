import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import http from 'node:http';

// Run against the dev frontend + server with a dedicated /tmp XDG_DATA_HOME.
// Probes use the local HTTP fixture below. Cloud, scan, sync and Helm actions
// are intercepted before reaching the backend; Go tests exercise their CLIs.
const baseURL = process.env.BLACKNODE_AUDIT_URL ?? 'http://127.0.0.1:9247';
const debugURL = process.env.BLACKNODE_CHROME_URL ?? 'http://127.0.0.1:9333';
const probe = http.createServer((_req, res) => { res.writeHead(200); res.end('fixture healthy'); });
await new Promise(resolve => probe.listen(0, '127.0.0.1', resolve));
const probeURL = `http://127.0.0.1:${probe.address().port}/health`;
const methods = {};
const bindings = new URL('../bindings/github.com/blacknode/blacknode/internal/service/', import.meta.url);
for (const file of ['infrastructureservice.ts', 'ssmservice.ts', 'platformservice.ts']) {
  const source = await fs.readFile(new URL(file, bindings), 'utf8');
  for (const match of source.matchAll(/export function (\w+)\([^]*?\$Call.ByID\((\d+)/g)) methods[match[2]] = `${file}:${match[1]}`;
}
const targets = await (await fetch(`${debugURL}/json/list`)).json();
const target = targets.find(t => t.type === 'page' && (t.url === 'about:blank' || t.url.startsWith(baseURL)));
assert(target, 'Open a dedicated audit Chrome tab');
const socket = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
let sequence = 0;
const pending = new Map();
const exceptions = [];
socket.addEventListener('message', ({ data }) => {
  const message = JSON.parse(data);
  if (message.method === 'Runtime.exceptionThrown') exceptions.push(message.params.exceptionDetails);
  const p = pending.get(message.id);
  if (!p) return;
  pending.delete(message.id); clearTimeout(p.timer);
  if (message.error) p.reject(new Error(JSON.stringify(message.error))); else p.resolve(message.result);
});
function call(method, params = {}) {
  return new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`Timeout: ${method}`)); }, 20000);
    pending.set(id, { resolve, reject, timer }); socket.send(JSON.stringify({ id, method, params }));
  });
}
async function evaluate(expression) {
  const result = await call('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
  return result.result?.value;
}
async function waitFor(expression) {
  for (let i = 0; i < 100; i++) {
    if (await evaluate(expression)) return;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`Condition not reached: ${expression}\n${await evaluate('document.body.innerText.slice(-1800)')}`);
}
async function click(text) {
  await evaluate(`(() => { const b = [...document.querySelectorAll('button')].find(b => b.textContent.trim() === ${JSON.stringify(text)}); if (!b || b.disabled) throw new Error('Button unavailable: ' + ${JSON.stringify(text)}); b.click(); })()`);
}
async function field(label, value) {
  await evaluate(`(() => { const label = [...document.querySelectorAll('label')].find(l => l.firstChild?.textContent.trim() === ${JSON.stringify(label)}); const el = label?.querySelector('input,textarea,select'); if (!el) throw new Error('Missing field: ' + ${JSON.stringify(label)}); if (el.type === 'checkbox') el.checked = ${JSON.stringify(value)}; else el.value = ${JSON.stringify(value)}; el.dispatchEvent(new Event('input', {bubbles:true})); el.dispatchEvent(new Event('change', {bubbles:true})); })()`);
}
async function view(name) {
  await evaluate(`window.auditApp.view = ${JSON.stringify(name)}`);
  await waitFor(name === 'runbooks' ? "document.body.innerText.includes('New runbook')" : "document.querySelector('.ops-body') && !document.body.innerText.includes('Loading…')");
  await new Promise(resolve => setTimeout(resolve, 200));
}

try {
  await call('Runtime.enable'); await call('Page.enable');
  await call('Emulation.setDeviceMetricsOverride', { width: 1280, height: 820, deviceScaleFactor: 1, mobile: false });
  await call('Page.navigate', { url: baseURL });
  await waitFor("document.querySelector('input[type=password]') || document.querySelector('nav')");
  await evaluate("import('/src/lib/state.svelte.ts').then(async m => { window.auditApp = m.app; await m.app.refreshVault(); })");
  await evaluate(`(() => {
    const inputs = [...document.querySelectorAll('input[type=password]')];
    if (!inputs.length) return;
    for (const input of inputs) { input.value = 'blacknode-infrastructure-audit-only'; input.dispatchEvent(new Event('input', {bubbles:true})); }
    const remember = document.querySelector('#rememberMe'); if (remember?.checked) remember.click();
    [...document.querySelectorAll('button')].find(b => /^(Create vault|Unlock)$/.test(b.textContent.trim()))?.click();
  })()`);
  await waitFor("!!document.querySelector('nav')");
  await evaluate(String.raw`(async () => {
    window.auditApp = (await import('/src/lib/state.svelte.ts')).app;
    window.infraAPI = await import('/bindings/github.com/blacknode/blacknode/internal/service/index.ts');
    window.auditMethods = ${JSON.stringify(methods)};
    window.auditMutations = [];
    window.auditOriginalFetch = window.fetch;
    const reply = value => new Response(JSON.stringify(value), {headers:{'Content-Type':'application/json'}});
    window.fetch = async (url, options) => {
      if (String(url).endsWith('/wails/runtime') && options?.body) {
        const body = JSON.parse(options.body);
        const name = window.auditMethods[body.args?.methodID];
        const args = body.args?.args ?? [];
        if (name === 'infrastructureservice.ts:CheckTools') return reply(Object.fromEntries(args[1].map(n => [n, n !== 'trivy'])));
        if (name === 'infrastructureservice.ts:PreviewDirectorySync') return reply({id:'fixture-sync-plan',profile:window.fixtureSync,expiresAt:Date.now()+60000,entries:[{path:'config.yaml',action:'conflict',localHash:'local',remoteHash:'remote',size:20}]});
        if (name === 'infrastructureservice.ts:ApplyDirectorySync') { window.auditMutations.push({name,args}); return reply([{path:'config.yaml',action:'upload',error:''}]); }
        if (name === 'infrastructureservice.ts:RunSecurityScan') { window.auditMutations.push({name,args}); return reply(window.fixtureScan); }
        if (name === 'infrastructureservice.ts:HelmReleases') return reply([{name:'web',namespace:'app',revision:'2',status:'deployed',chart:'web-1.0.0',updated:'',app_version:'1'}]);
        if (name === 'infrastructureservice.ts:HelmValues') return reply('replicas: 2\n');
        if (name === 'infrastructureservice.ts:HelmHistory') return reply([{revision:2,status:'deployed',chart:'web-1.0.0',updated:'',description:''},{revision:1,status:'superseded',chart:'web-0.9.0',updated:'',description:''}]);
        if (name?.startsWith('infrastructureservice.ts:PreviewHelm')) return reply({id:'fixture-helm-plan',hostID:'',scope:'fixture',title:'Review Helm fixture',command:'helm --kube-context=fixture rollback web 1',input:'',preview:'Fixture dry run; no cluster contacted',expiresAt:Date.now()+60000});
        if (name === 'platformservice.ts:ExecuteChange') { window.auditMutations.push({name,args}); return reply({hostID:'',hostName:'Fixture',exitCode:0,stdout:'Fixture applied',stderr:'',error:'',durationMs:1,attempts:1}); }
        if (name === 'platformservice.ts:DiscardChange') return reply(null);
        if (name === 'ssmservice.ts:Available') return reply({aws:true,plugin:true});
        if (name === 'ssmservice.ts:Connect' || name === 'ssmservice.ts:Write' || name === 'ssmservice.ts:Disconnect' || name === 'ssmservice.ts:Resize') { window.auditMutations.push({name,args}); return reply(null); }
      }
      return window.auditOriginalFetch(url,options);
    };
    for (const m of await window.infraAPI.InfrastructureService.Monitors()) if (m.monitor.id === 'fixture-monitor') await window.infraAPI.InfrastructureService.DeleteMonitor(m.monitor.id,m.revision);
    for (const kind of ['service-graph','sync-profile','scan-profile','ssm-profile']) for (const d of await window.infraAPI.PlatformService.ListRecords(kind)) if (d.id.startsWith('fixture-')) await window.infraAPI.PlatformService.DeleteRecord(kind,d.id,d.revision);
    const host = await window.infraAPI.HostService.Create({name:'Infrastructure UI fixture',host:'127.0.0.1',port:9,username:'fixture',authMethod:'agent',protocol:'ssh',environment:'dev'});
    window.auditApp.hosts = [host]; window.fixtureHost = host;
    const m = {id:'fixture-monitor',name:'Fixture web health',kind:'http',target:${JSON.stringify(probeURL)},hostID:host.id,intervalSeconds:60,timeoutSeconds:2,expectedStatus:200,expectedDNS:'',certificateDays:14,failureThreshold:1,enabled:false,maintenanceStart:0,maintenanceEnd:0};
    await window.infraAPI.InfrastructureService.SaveMonitor(m,0);
    await window.infraAPI.InfrastructureService.SaveServiceGraph({id:'fixture-map',name:'Fixture services',nodes:[{id:'web',name:'Web',kind:'service',hostID:host.id,monitorID:m.id},{id:'db',name:'Database',kind:'database',hostID:host.id,monitorID:''}],edges:[{from:'web',to:'db'}]},0);
    window.fixtureSync = {id:'fixture-sync',name:'Fixture directories',hostID:host.id,localPath:'/tmp/local-fixture',remotePath:'/tmp/remote-fixture',direction:'both',excludes:[],mirror:false};
    await window.infraAPI.InfrastructureService.SaveSyncProfile(window.fixtureSync,0);
    const profile = {id:'fixture-scan',name:'Fixture image',hostID:'',kind:'image',target:'fixture:1'};
    await window.infraAPI.InfrastructureService.SaveScanProfile(profile,0);
    window.fixtureScan = {id:'fixture-result',profile,at:Date.now(),findings:[{key:'fixture-cve',kind:'vulnerability',id:'CVE-fixture',target:'fixture:1',package:'libfixture',installed:'1',fixed:'2',severity:'HIGH',title:'Fixture finding',url:''}]};
    await window.infraAPI.SSMService.Save({id:'fixture-ssm',name:'Fixture AWS',target:'i-0123456789abcdef0',region:'us-east-1',awsProfile:'fixture',environment:'dev'},0);
    // Keep mock reports in the UI history, without executing a real scan.
    const fetchBeforeReports = window.fetch;
    window.fetch = async (url, options) => {
      if (String(url).endsWith('/wails/runtime') && options?.body) {
        const b = JSON.parse(options.body);
        if (window.auditMethods[b.args?.methodID] === 'platformservice.ts:ListRecords' && b.args.args[0] === 'vulnerability-scan') return reply([{id:'fixture-result',kind:'vulnerability-scan',revision:1,body:JSON.stringify(window.fixtureScan),createdAt:Date.now(),updatedAt:Date.now()}]);
      }
      return fetchBeforeReports(url,options);
    };
  })()`);
  await waitFor('!window.auditApp.loading');

  const views = ['uptime','directory-sync','service-map','security-scans','git-runbooks','helm','ssm','team','runbooks'];
  const panels = [];
  for (const theme of ['dark','light']) {
    await evaluate(`window.auditApp.settings.theme = '${theme}'`);
    for (const width of [1280,800]) {
      await call('Emulation.setDeviceMetricsOverride',{width,height:820,deviceScaleFactor:1,mobile:false});
      for (const name of views) {
        await view(name);
        const state = await evaluate(`({text:document.body.innerText.slice(-1000),overflow:document.documentElement.scrollWidth>innerWidth,unlabelled:[...document.querySelectorAll('input,select,textarea')].filter(e=>e.getClientRects().length && e.type!=='hidden' && !e.labels?.length && !e.getAttribute('aria-label') && !e.getAttribute('aria-labelledby')).map(e=>e.outerHTML)})`);
        assert(!/Something went wrong|Could not render/.test(state.text), name);
        assert(!state.overflow, `${name} overflow at ${width}`);
        assert.deepEqual(state.unlabelled,[],`${name} labels`);
        panels.push({theme,width,name});
      }
    }
  }
  await call('Emulation.setDeviceMetricsOverride',{width:1280,height:820,deviceScaleFactor:1,mobile:false});
  await view('uptime'); await click('Check now');
  await waitFor("document.body.innerText.includes('100.00%')");
  await click('Fixture web health'); await waitFor("document.querySelector('svg[aria-label=\"Recent response times\"]')");
  await view('directory-sync'); await field('Saved profile','fixture-sync'); await click('Compare saved profile');
  await waitFor("document.querySelector('[role=dialog]')");
  assert(await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='Apply selected changes').disabled"),'unresolved conflict must block apply');
  await evaluate(`(() => {const el=document.querySelector('select[aria-label="Action for config.yaml"]');el.value='upload';el.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  await click('Apply selected changes'); await waitFor("document.body.innerText.includes('1 file changes completed')");
  await view('service-map');
  await evaluate(`(() => {const el=document.querySelector('select[aria-label="Service map"]');el.value='fixture-map';el.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  await waitFor("document.querySelector('[aria-label=\"Inspect Database\"]')");
  await evaluate("document.querySelector('[aria-label=\"Inspect Database\"]').dispatchEvent(new MouseEvent('click',{bubbles:true}))");
  await waitFor("document.body.innerText.includes('Affected consumers:')");
  await view('security-scans'); await click('Check tools'); await waitFor("document.body.innerText.includes('trivy: missing')");
  await field('Scan profile','fixture-scan'); await click('Run saved scan');
  await waitFor("document.body.innerText.includes('CVE-fixture')");
  await view('helm'); await field('Kubernetes context','fixture'); await click('Load releases'); await click('web');
  await waitFor("document.body.innerText.includes('Review rollback')"); await click('Review rollback');
  await waitFor("document.body.innerText.includes('Fixture dry run')");
  assert.equal(await evaluate("window.auditMutations.filter(m=>m.name==='platformservice.ts:ExecuteChange').length"),0,'preview must not execute');
  await click('Execute reviewed change'); await waitFor("document.body.innerText.includes('Fixture applied')"); await click('Close');
  await view('ssm'); await field('Session profile','fixture-ssm'); await click('Connect saved profile');
  await waitFor("document.body.innerText.includes('Connected') && document.querySelector('textarea[aria-label=\"Terminal input\"]')");
  await evaluate("document.querySelector('textarea[aria-label=\"Terminal input\"]').focus()"); await call('Input.insertText',{text:'fixture input'});
  await waitFor("window.auditMutations.some(m=>m.name==='ssmservice.ts:Write' && m.args[1]==='fixture input')");
  await click('Disconnect');
  assert.deepEqual(exceptions,[],'browser exceptions');
  await fs.writeFile('/tmp/blacknode-infrastructure-ui.json',JSON.stringify({panels,mutations:await evaluate('window.auditMutations'),exceptions},null,2));
  const screenshot = await call('Page.captureScreenshot',{format:'png'});
  await fs.writeFile('/tmp/blacknode-infrastructure-ui.png',Buffer.from(screenshot.data,'base64'));
  console.log(`Passed ${panels.length} panel/theme/viewport checks and monitor, sync, graph, scan, Helm, and AWS session workflows.`);
} finally {
  socket.close();
  for (const p of pending.values()) clearTimeout(p.timer);
  probe.closeAllConnections(); probe.close();
}
