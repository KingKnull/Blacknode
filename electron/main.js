const { app, BrowserWindow, ipcMain } = require('electron');
const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');

// Suppress benign VSync warning logs on Linux GPU drivers
if (process.platform === 'linux') {
  app.commandLine.appendSwitch('disable-gpu-vsync');
  app.commandLine.appendSwitch('ignore-gpu-blocklist');
}


let mainWindow;
let backendProcess;

function getBinaryPath() {
  const binaryName = process.platform === 'win32' ? 'blacknode-server.exe' : 'blacknode-server';
  if (app.isPackaged) {
    return path.join(process.resourcesPath, 'backend', binaryName);
  }
  return path.join(__dirname, '..', 'bin', binaryName);
}

function startBackendServer() {
  const binPath = getBinaryPath();
  console.log(`[Electron] Resolving Go backend binary: ${binPath}`);

  if (!fs.existsSync(binPath)) {
    console.warn(`[Electron] Go server binary not found at ${binPath}. Ensure 'task build:server' was executed.`);
    return;
  }

  try {
    backendProcess = spawn(binPath, [], {
      env: {
        ...process.env,
        PORT: '9245',
        WAILS_SERVER_PORT: '9245',
        WAILS_SERVER_HOST: '127.0.0.1'
      },
      stdio: ['ignore', 'pipe', 'pipe']
    });

    backendProcess.stdout.on('data', (data) => {
      console.log(`[Go Server]: ${data.toString().trim()}`);
    });

    backendProcess.stderr.on('data', (data) => {
      console.error(`[Go Server ERR]: ${data.toString().trim()}`);
    });

    backendProcess.on('exit', (code, signal) => {
      console.log(`[Electron] Backend process exited with code ${code}, signal ${signal}`);
    });
  } catch (err) {
    console.error('[Electron] Failed to start Go backend server:', err);
  }
}

function waitForServer(url, timeoutMs = 10000) {
  const http = require('http');
  const start = Date.now();
  return new Promise((resolve) => {
    const check = () => {
      if (Date.now() - start > timeoutMs) {
        return resolve(false);
      }
      const req = http.get(url, (res) => {
        if (res.statusCode === 200) {
          resolve(true);
        } else {
          setTimeout(check, 100);
        }
      });
      req.on('error', () => {
        setTimeout(check, 100);
      });
      req.setTimeout(500, () => {
        req.destroy();
        setTimeout(check, 100);
      });
    };
    check();
  });
}

const SERVER_URL = process.env.VITE_DEV_SERVER_URL || 'http://127.0.0.1:9245';
const HEALTH_URL = 'http://127.0.0.1:9245/health';

// Shown when the backend never answers. The old behaviour loaded the static
// bundle from file:// as a "fallback", but without the backend there is no
// Wails bridge — bindings, events and terminal streams all fail — so the UI
// just looked frozen. Surfacing the failure (with a Retry) is honest and
// diagnosable instead.
function loadErrorPage(detail) {
  if (!mainWindow) return;
  const html = `<!doctype html><html><head><meta charset="utf-8">
<title>Blacknode — backend unavailable</title>
<style>
  :root { color-scheme: dark; }
  body { margin:0; height:100vh; display:flex; align-items:center; justify-content:center;
         background:#08080b; color:#e5e7eb; font:14px/1.6 system-ui,-apple-system,sans-serif; }
  .card { max-width:520px; padding:32px; text-align:center; }
  h1 { font-size:15px; margin:0 0 14px; color:#f87171; letter-spacing:.04em; text-transform:uppercase; }
  p { margin:8px 0; color:#9ca3af; }
  code { background:#15151c; padding:2px 6px; border-radius:4px; color:#cbd5e1; }
  button { margin-top:22px; padding:10px 22px; background:#1e40af; color:#fff; border:0;
           border-radius:6px; font:inherit; cursor:pointer; }
  button:hover { background:#2563eb; }
</style></head>
<body><div class="card">
  <h1>Backend didn't start</h1>
  <p>${detail}</p>
  <p>The server (<code>blacknode-server</code>) isn't responding on <code>127.0.0.1:9245</code>.</p>
  <button onclick="window.electronAPI.invoke('backend-retry')">Retry</button>
</div></body></html>`;
  mainWindow.loadURL('data:text/html;charset=utf-8,' + encodeURIComponent(html));
}

// Health-check the backend, then load the app from the server (which also
// serves the Wails WS bridge). On timeout, show the error screen rather than a
// dead static shell.
async function loadApp() {
  if (!mainWindow) return;
  const serverReady = await waitForServer(HEALTH_URL, 10000);
  if (serverReady) {
    console.log('[Electron] Go backend health check passed. Loading app...');
    mainWindow.loadURL(SERVER_URL);
    return;
  }
  console.warn('[Electron] Backend health check timed out; showing error screen.');
  const binExists = fs.existsSync(getBinaryPath());
  loadErrorPage(binExists
    ? 'It built but did not respond in time — check the logs, then retry.'
    : 'The server binary is missing — run <code>task build:server</code>, then retry.');
}

// Retry from the error screen: restart the backend if it isn't alive, then reload.
ipcMain.handle('backend-retry', async () => {
  const alive = backendProcess && backendProcess.exitCode === null && backendProcess.signalCode === null;
  if (!alive) startBackendServer();
  await loadApp();
});

async function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1280,
    height: 820,
    minWidth: 800,
    minHeight: 500,
    backgroundColor: '#08080b',
    title: 'Blacknode',
    show: false,
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      nodeIntegration: false,
      contextIsolation: true
    }
  });

  mainWindow.once('ready-to-show', () => {
    mainWindow.show();
  });

  await loadApp();

  mainWindow.on('closed', () => {
    mainWindow = null;
  });
}

app.whenReady().then(() => {
  startBackendServer();
  createWindow();

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    }
  });
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') {
    app.quit();
  }
});

app.on('will-quit', () => {
  if (backendProcess) {
    console.log('[Electron] Terminating Go backend process...');
    backendProcess.kill('SIGTERM');
  }
});
