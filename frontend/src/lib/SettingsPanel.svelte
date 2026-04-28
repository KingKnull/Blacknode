<script lang="ts">
  import { onMount } from "svelte";
  import {
    SettingsService,
    AIService,
    NotificationService,
    UpdateService,
    SyncService,
  } from "../../bindings/github.com/blacknode/blacknode";
  import type {
    NotifyConfig,
    UpdateInfo,
    SyncStatus,
  } from "../../bindings/github.com/blacknode/blacknode/models";
  import type { TeamActivity } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import {
    Settings as SettingsIcon,
    Sparkles,
    Lock,
    Activity,
    Eye,
    EyeOff,
    CheckCircle2,
    Loader2,
    Bell,
    Send,
    Palette,
    RefreshCw,
    Download,
    Info,
    Cloud,
    CloudUpload,
    CloudDownload,
    Users,
  } from "@lucide/svelte";

  let apiKeyInput = $state("");
  let showKey = $state(false);
  let savingKey = $state(false);
  let testStatus = $state<"" | "ok" | "fail">("");
  let testMessage = $state("");
  let savingLock = $state(false);
  let savingShell = $state(false);
  let savingMetrics = $state(false);
  let savingTheme = $state(false);

  let autoLockMinutes = $state(15);
  let defaultShellPath = $state("");
  let metricsIntervalSeconds = $state(5);

  let notify = $state<NotifyConfig>({
    desktopEnabled: true,
    webhookURL: "",
    longExecSeconds: 10,
  });
  let notifyBusy = $state(false);
  let notifyTested = $state<"" | "ok" | "fail">("");

  let currentVersion = $state("");
  let updateInfo = $state<UpdateInfo | null>(null);
  let checkingUpdate = $state(false);

  let syncEndpoint = $state("");
  let syncToken = $state("");
  let syncStatus = $state<SyncStatus | null>(null);
  let syncBusy = $state(false);

  async function loadSyncStatus() {
    try {
      syncStatus = (await SyncService.Status()) as SyncStatus;
      if (syncStatus.endpoint) syncEndpoint = syncStatus.endpoint;
    } catch {
      // ignore
    }
  }
  async function saveSyncConfig() {
    syncBusy = true;
    try {
      await SyncService.Configure({
        endpoint: syncEndpoint.trim(),
        token: syncToken,
      });
      await loadSyncStatus();
    } finally {
      syncBusy = false;
    }
  }
  async function syncPush() {
    syncBusy = true;
    try {
      syncStatus = (await SyncService.Push()) as SyncStatus;
    } catch (e: any) {
      syncStatus = { ...(syncStatus ?? ({} as SyncStatus)), lastError: String(e?.message ?? e) };
    } finally {
      syncBusy = false;
    }
  }
  async function syncPull() {
    syncBusy = true;
    try {
      syncStatus = (await SyncService.Pull()) as SyncStatus;
    } catch (e: any) {
      syncStatus = { ...(syncStatus ?? ({} as SyncStatus)), lastError: String(e?.message ?? e) };
    } finally {
      syncBusy = false;
    }
  }
  function fmtTime(unix: number): string {
    if (!unix) return "never";
    return new Date(unix * 1000).toLocaleString();
  }

  // Team-mode state. `teamActor` is just a UI label persisted to
  // localStorage so the publisher's name shows in the activity log.
  let teamActor = $state<string>(
    localStorage.getItem("blacknode.team.actor") ?? "",
  );
  let teamActivity = $state<TeamActivity[]>([]);
  let teamBusy = $state(false);

  async function refreshTeamActivity() {
    try {
      teamActivity = ((await SyncService.TeamActivity(50)) ?? []) as TeamActivity[];
    } catch {
      // ignore
    }
  }
  async function teamPublish() {
    teamBusy = true;
    localStorage.setItem("blacknode.team.actor", teamActor);
    try {
      syncStatus = (await SyncService.PublishTeam(teamActor || "anonymous")) as SyncStatus;
      await refreshTeamActivity();
    } catch (e: any) {
      syncStatus = { ...(syncStatus ?? ({} as SyncStatus)), lastError: String(e?.message ?? e) };
    } finally {
      teamBusy = false;
    }
  }
  async function teamSubscribe() {
    teamBusy = true;
    localStorage.setItem("blacknode.team.actor", teamActor);
    try {
      syncStatus = (await SyncService.SubscribeTeam(teamActor || "anonymous")) as SyncStatus;
      await refreshTeamActivity();
    } catch (e: any) {
      syncStatus = { ...(syncStatus ?? ({} as SyncStatus)), lastError: String(e?.message ?? e) };
    } finally {
      teamBusy = false;
    }
  }

  async function checkForUpdates() {
    checkingUpdate = true;
    try {
      updateInfo = (await UpdateService.Check()) as UpdateInfo;
    } catch (e: any) {
      updateInfo = {
        current: currentVersion,
        latest: "",
        updateAvailable: false,
        releaseUrl: "",
        notes: "",
        publishedAt: "",
        error: String(e?.message ?? e),
      };
    } finally {
      checkingUpdate = false;
    }
  }

  function openReleasePage() {
    if (updateInfo?.releaseUrl) {
      window.open(updateInfo.releaseUrl, "_blank", "noopener");
    }
  }

  onMount(async () => {
    autoLockMinutes = app.settings.autoLockMinutes;
    defaultShellPath = app.settings.defaultShellPath;
    metricsIntervalSeconds = app.settings.metricsIntervalSeconds;
    try {
      notify = (await NotificationService.Config()) as NotifyConfig;
    } catch {
      // ignore
    }
    try {
      currentVersion = await UpdateService.CurrentVersion();
    } catch {
      // ignore
    }
    await loadSyncStatus();
    await refreshTeamActivity();
  });

  async function saveNotify() {
    notifyBusy = true;
    notifyTested = "";
    try {
      await NotificationService.SetDesktopEnabled(notify.desktopEnabled);
      await NotificationService.SetWebhookURL(notify.webhookURL);
      await NotificationService.SetLongExecSeconds(notify.longExecSeconds);
    } finally {
      notifyBusy = false;
    }
  }

  async function testNotify() {
    notifyTested = "";
    try {
      await NotificationService.Test();
      notifyTested = "ok";
    } catch {
      notifyTested = "fail";
    }
  }

  async function saveAPIKey() {
    savingKey = true;
    testStatus = "";
    testMessage = "";
    try {
      await SettingsService.SetAnthropicAPIKey(apiKeyInput);
      apiKeyInput = "";
      await app.refreshSettings();
    } catch (e: any) {
      testStatus = "fail";
      testMessage = String(e?.message ?? e);
    } finally {
      savingKey = false;
    }
  }

  async function testKey() {
    testStatus = "";
    testMessage = "";
    savingKey = true;
    try {
      const out = await AIService.Translate("list files in current directory", "bash", "");
      if (typeof out === "string" && out.length > 0) {
        testStatus = "ok";
        testMessage = `→ ${out}`;
      } else {
        testStatus = "fail";
        testMessage = "Empty response";
      }
    } catch (e: any) {
      testStatus = "fail";
      testMessage = String(e?.message ?? e);
    } finally {
      savingKey = false;
    }
  }

  async function clearAPIKey() {
    if (!confirm("Remove the stored Anthropic API key?")) return;
    await SettingsService.SetAnthropicAPIKey("");
    await app.refreshSettings();
  }

  async function saveAutoLock() {
    savingLock = true;
    try {
      await SettingsService.SetAutoLockMinutes(autoLockMinutes);
      await app.refreshSettings();
    } finally {
      savingLock = false;
    }
  }

  async function saveShell() {
    savingShell = true;
    try {
      await SettingsService.SetDefaultShellPath(defaultShellPath);
      await app.refreshSettings();
    } finally {
      savingShell = false;
    }
  }

  async function saveMetrics() {
    savingMetrics = true;
    try {
      await SettingsService.SetMetricsInterval(metricsIntervalSeconds);
      await app.refreshSettings();
    } finally {
      savingMetrics = false;
    }
  }

  async function setTheme(t: "dark" | "light") {
    if (t === app.settings.theme) return;
    savingTheme = true;
    try {
      await SettingsService.SetTheme(t);
      // Optimistic update so the App.svelte $effect picks it up immediately;
      // refreshSettings round-trips and confirms.
      app.settings = { ...app.settings, theme: t };
      await app.refreshSettings();
    } finally {
      savingTheme = false;
    }
  }
</script>

<div class="flex h-full flex-col">
  <PageHeader
    icon={SettingsIcon}
    title="Settings"
    subtitle="Preferences, security, and integrations"
  />

  <div class="flex-1 overflow-y-auto p-6">
    <div class="mx-auto max-w-2xl space-y-6">
      <!-- AI / Anthropic -->
      <section class="rounded-lg border hairline surface-2 p-5">
        <div class="mb-4 flex items-center gap-2">
          <Sparkles size="14" class="text-[var(--color-accent)]" />
          <h3 class="text-sm font-semibold">AI assistant</h3>
        </div>
        <p class="text-xs text-[var(--color-text-3)]">
          Powers the side drawer (⌘I) and command-palette translations. Uses
          <span class="font-mono">claude-haiku-4-5</span> for low-latency
          suggestions and <span class="font-mono">claude-sonnet-4-6</span> for
          analyses. Your key is encrypted at rest with the vault — it never
          leaves your machine in plaintext.
        </p>

        <div class="mt-4 space-y-2">
          <label class="block">
            <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Anthropic API key</span
            >
            <div class="mt-1 flex items-center gap-2">
              <div
                class="relative flex flex-1 items-center rounded-md border hairline bg-[var(--color-surface-3)]"
              >
                <input
                  type={showKey ? "text" : "password"}
                  class="flex-1 bg-transparent px-3 py-2 font-mono text-xs outline-none"
                  placeholder={app.settings.hasAnthropicKey
                    ? "•••••• (saved)"
                    : "sk-ant-…"}
                  bind:value={apiKeyInput}
                />
                <button
                  class="px-2 text-[var(--color-text-3)] hover:text-[var(--color-text-1)]"
                  onclick={() => (showKey = !showKey)}
                  title={showKey ? "Hide" : "Show"}
                >
                  {#if showKey}<EyeOff size="12" />{:else}<Eye size="12" />{/if}
                </button>
              </div>
              <button
                class="flex items-center gap-1 rounded-md bg-[var(--color-accent)] px-3 py-2 text-xs font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
                disabled={!apiKeyInput || savingKey}
                onclick={saveAPIKey}
              >
                {#if savingKey}
                  <Loader2 size="11" class="animate-spin" />
                {:else}
                  Save
                {/if}
              </button>
            </div>
          </label>

          <div class="flex flex-wrap items-center gap-2">
            {#if app.settings.hasAnthropicKey}
              <span
                class="inline-flex items-center gap-1 rounded-full bg-[var(--color-accent)]/10 px-2 py-0.5 text-[10px] text-[var(--color-accent)]"
              >
                <CheckCircle2 size="10" /> key saved
              </span>
              <button
                class="rounded px-2 py-1 text-[10px] text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)]"
                onclick={testKey}
                disabled={savingKey}
              >
                Test
              </button>
              <button
                class="rounded px-2 py-1 text-[10px] text-[var(--color-text-3)] hover:bg-[var(--color-danger)]/15 hover:text-[var(--color-danger)]"
                onclick={clearAPIKey}
              >
                Remove
              </button>
            {/if}
            {#if testStatus === "ok"}
              <span class="font-mono text-[10px] text-[var(--color-accent)]"
                >{testMessage}</span
              >
            {:else if testStatus === "fail"}
              <span class="font-mono text-[10px] text-[var(--color-danger)]"
                >{testMessage}</span
              >
            {/if}
          </div>
        </div>
      </section>

      <!-- Vault -->
      <section class="rounded-lg border hairline surface-2 p-5">
        <div class="mb-4 flex items-center gap-2">
          <Lock size="14" class="text-[var(--color-accent)]" />
          <h3 class="text-sm font-semibold">Security</h3>
        </div>
        <label class="block">
          <div class="flex items-center justify-between">
            <span class="text-xs font-medium text-[var(--color-text-1)]"
              >Auto-lock vault after</span
            >
            <span class="text-[10px] text-[var(--color-text-3)]"
              >0 = never lock</span
            >
          </div>
          <p class="mt-0.5 text-[11px] text-[var(--color-text-3)]">
            Locks the vault when the app sees no keystroke or click for this many
            minutes. The master key is wiped from memory.
          </p>
          <div class="mt-2 flex items-center gap-2">
            <input
              type="number"
              min="0"
              class="w-24 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 text-sm outline-none"
              bind:value={autoLockMinutes}
            />
            <span class="text-xs text-[var(--color-text-3)]">minutes</span>
            <button
              class="ml-auto flex items-center gap-1 rounded-md border hairline-strong px-3 py-1.5 text-[11px] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
              disabled={savingLock || autoLockMinutes === app.settings.autoLockMinutes}
              onclick={saveAutoLock}
            >
              {#if savingLock}<Loader2 size="11" class="animate-spin" />{:else}
                Save
              {/if}
            </button>
          </div>
        </label>
      </section>

      <!-- Appearance -->
      <section class="rounded-lg border hairline surface-2 p-5">
        <div class="mb-4 flex items-center gap-2">
          <Palette size="14" class="text-[var(--color-accent)]" />
          <h3 class="text-sm font-semibold">Appearance</h3>
        </div>
        <p class="text-xs text-[var(--color-text-3)]">
          Switches every panel between the dark navy / cyan brand palette and a
          light slate variant. Active terminals and code editors keep their
          current theme until reopened.
        </p>
        <div class="mt-4 flex items-center gap-2">
          <button
            class="flex flex-1 items-center justify-center gap-2 rounded-md border px-4 py-3 text-xs transition-colors {app
              .settings.theme === 'dark'
              ? 'border-[var(--color-accent)]/40 bg-[var(--color-accent-soft)] text-[var(--color-text-1)]'
              : 'hairline text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)]'}"
            disabled={savingTheme}
            onclick={() => setTheme("dark")}
          >
            <div class="h-3 w-6 rounded border hairline-strong" style="background:#08080b"></div>
            Dark
          </button>
          <button
            class="flex flex-1 items-center justify-center gap-2 rounded-md border px-4 py-3 text-xs transition-colors {app
              .settings.theme === 'light'
              ? 'border-[var(--color-accent)]/40 bg-[var(--color-accent-soft)] text-[var(--color-text-1)]'
              : 'hairline text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)]'}"
            disabled={savingTheme}
            onclick={() => setTheme("light")}
          >
            <div class="h-3 w-6 rounded border hairline-strong" style="background:#f7f8fa"></div>
            Light
          </button>
        </div>
      </section>

      <!-- Notifications -->
      <section class="rounded-lg border hairline surface-2 p-5">
        <div class="mb-4 flex items-center gap-2">
          <Bell size="14" class="text-[var(--color-accent)]" />
          <h3 class="text-sm font-semibold">Notifications</h3>
        </div>
        <p class="text-xs text-[var(--color-text-3)]">
          Fires on long-running multi-host completions and CPU/MEM/DISK over
          90% (debounced 5 minutes per host). Desktop notifications use your
          OS notification center; webhooks POST a JSON payload.
        </p>

        <div class="mt-4 space-y-3">
          <label class="flex items-center justify-between">
            <span class="text-xs font-medium text-[var(--color-text-1)]"
              >Desktop notifications</span
            >
            <input
              type="checkbox"
              class="accent-[var(--color-accent)]"
              bind:checked={notify.desktopEnabled}
            />
          </label>

          <label class="block">
            <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Long-exec threshold (seconds)</span
            >
            <p class="mt-0.5 text-[11px] text-[var(--color-text-3)]">
              Multi-host runs that take longer than this fire a "finished"
              notification. Short runs stay silent.
            </p>
            <input
              type="number"
              min="1"
              class="mt-1 w-32 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 text-sm outline-none"
              bind:value={notify.longExecSeconds}
            />
          </label>

          <label class="block">
            <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Webhook URL</span
            >
            <p class="mt-0.5 text-[11px] text-[var(--color-text-3)]">
              POSTs a JSON body
              <span class="font-mono">{"{kind, title, body, source, hostName, timestamp}"}</span>
              to this URL on every notification. Empty = disabled.
            </p>
            <input
              class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-xs outline-none"
              placeholder="https://hooks.slack.com/services/…"
              bind:value={notify.webhookURL}
            />
          </label>

          <div class="flex flex-wrap items-center gap-2">
            <button
              class="flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
              disabled={notifyBusy}
              onclick={saveNotify}
            >
              {#if notifyBusy}<Loader2 size="11" class="animate-spin" />{:else}Save{/if}
            </button>
            <button
              class="flex items-center gap-1.5 rounded-md border hairline-strong px-3 py-1.5 text-[11px] hover:bg-[var(--color-surface-3)]"
              onclick={testNotify}
            >
              <Send size="11" /> Send test
            </button>
            {#if notifyTested === "ok"}
              <span class="text-[10px] text-[var(--color-accent)]">test fired</span>
            {:else if notifyTested === "fail"}
              <span class="text-[10px] text-[var(--color-danger)]">test failed</span>
            {/if}
          </div>
        </div>
      </section>

      <!-- Terminal / Shell -->
      <section class="rounded-lg border hairline surface-2 p-5">
        <div class="mb-4 flex items-center gap-2">
          <Activity size="14" class="text-[var(--color-accent)]" />
          <h3 class="text-sm font-semibold">Local shell & metrics</h3>
        </div>

        <label class="block">
          <span class="text-xs font-medium text-[var(--color-text-1)]"
            >Default local shell</span
          >
          <p class="mt-0.5 text-[11px] text-[var(--color-text-3)]">
            Override the shell binary used for new local terminal tabs. Empty =
            auto-detect (pwsh → powershell → cmd on Windows; <span
              class="font-mono">$SHELL</span
            > → bash → sh on Unix).
          </p>
          <div class="mt-2 flex items-center gap-2">
            <input
              class="flex-1 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-xs outline-none"
              placeholder="auto"
              bind:value={defaultShellPath}
            />
            <button
              class="rounded-md border hairline-strong px-3 py-1.5 text-[11px] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
              disabled={savingShell ||
                defaultShellPath === app.settings.defaultShellPath}
              onclick={saveShell}
            >
              Save
            </button>
          </div>
        </label>

        <label class="mt-4 block">
          <span class="text-xs font-medium text-[var(--color-text-1)]"
            >Metrics polling interval</span
          >
          <p class="mt-0.5 text-[11px] text-[var(--color-text-3)]">
            Seconds between CPU/MEM/DISK polls per host (≥ 2).
          </p>
          <div class="mt-2 flex items-center gap-2">
            <input
              type="number"
              min="2"
              class="w-24 rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 text-sm outline-none"
              bind:value={metricsIntervalSeconds}
            />
            <span class="text-xs text-[var(--color-text-3)]">seconds</span>
            <button
              class="ml-auto rounded-md border hairline-strong px-3 py-1.5 text-[11px] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
              disabled={savingMetrics ||
                metricsIntervalSeconds === app.settings.metricsIntervalSeconds}
              onclick={saveMetrics}
            >
              Save
            </button>
          </div>
        </label>
      </section>

      <!-- Cloud sync -->
      <section class="rounded-lg border hairline surface-2 p-5">
        <div class="mb-4 flex items-center gap-2">
          <Cloud size="14" class="text-[var(--color-accent)]" />
          <h3 class="text-sm font-semibold">Cloud sync</h3>
        </div>
        <p class="text-xs text-[var(--color-text-3)]">
          Push hosts, snippets, and saved HTTP requests as a single
          AES-GCM-encrypted blob to any HTTP endpoint that accepts
          PUT/GET on <span class="font-mono">/blacknode-sync.bin</span>.
          The remote never sees plaintext — encryption uses a
          vault-derived key, so the vault must be unlocked. Private SSH
          keys are NOT synced; re-import them on each device.
        </p>

        <div class="mt-4 grid grid-cols-1 gap-3">
          <label class="block">
            <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Endpoint</span
            >
            <input
              type="text"
              class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-xs outline-none"
              placeholder="https://sync.example.com/blacknode"
              bind:value={syncEndpoint}
            />
          </label>
          <label class="block">
            <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Bearer token (optional)</span
            >
            <input
              type="password"
              class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono text-xs outline-none"
              placeholder="leave blank if endpoint is public"
              bind:value={syncToken}
            />
          </label>
          <div class="flex items-center gap-2">
            <button
              class="rounded-md border hairline-strong px-3 py-1.5 text-[11px] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
              disabled={syncBusy}
              onclick={saveSyncConfig}
            >
              Save config
            </button>
            <button
              class="ml-auto flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
              disabled={syncBusy || !syncStatus?.configured}
              onclick={syncPush}
              title="Encrypt and upload current state"
            >
              {#if syncBusy}<Loader2 size="11" class="animate-spin" />{:else}<CloudUpload size="11" />{/if}
              Push
            </button>
            <button
              class="flex items-center gap-1.5 rounded-md border hairline-strong px-3 py-1.5 text-[11px] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
              disabled={syncBusy || !syncStatus?.configured}
              onclick={syncPull}
              title="Download and merge into local state"
            >
              <CloudDownload size="11" />
              Pull
            </button>
          </div>

          {#if syncStatus}
            <div class="space-y-1 rounded-md border hairline surface-3 p-2 text-[11px] text-[var(--color-text-2)]">
              <div class="flex justify-between">
                <span class="text-[var(--color-text-3)]">Last push</span>
                <span class="font-mono">{fmtTime(syncStatus.lastPushAt)}</span>
              </div>
              <div class="flex justify-between">
                <span class="text-[var(--color-text-3)]">Last pull</span>
                <span class="font-mono">{fmtTime(syncStatus.lastPullAt)}</span>
              </div>
              {#if syncStatus.lastError}
                <p class="mt-1 rounded border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-1.5 text-[var(--color-danger)]">
                  {syncStatus.lastError}
                </p>
              {/if}
            </div>
          {/if}
        </div>
      </section>

      <!-- Team -->
      <section class="rounded-lg border hairline surface-2 p-5">
        <div class="mb-4 flex items-center gap-2">
          <Users size="14" class="text-[var(--color-accent)]" />
          <h3 class="text-sm font-semibold">Team</h3>
        </div>
        <p class="text-xs text-[var(--color-text-3)]">
          Publishes a curated snapshot to a shared blob
          (<span class="font-mono">/blacknode-team.bin</span>) on the same
          endpoint as Cloud sync. Notes are stripped; password-auth hosts
          are excluded so members use their own creds. Subscribers merge
          with last-write-wins and get a row in the activity log below.
          Configure the endpoint in the section above first.
        </p>

        <div class="mt-4 grid grid-cols-1 gap-3">
          <label class="block">
            <span class="text-[10px] font-medium uppercase tracking-[0.14em] text-[var(--color-text-3)]"
              >Display name (for activity log)</span
            >
            <input
              type="text"
              class="mt-1 w-full rounded-md border hairline bg-[var(--color-surface-3)] px-3 py-2 text-xs outline-none"
              placeholder="e.g. alice"
              bind:value={teamActor}
            />
          </label>
          <div class="flex items-center gap-2">
            <button
              class="ml-auto flex items-center gap-1.5 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90 disabled:opacity-50"
              disabled={teamBusy || !syncStatus?.configured}
              onclick={teamPublish}
              title="Push a curated snapshot to the team blob"
            >
              {#if teamBusy}<Loader2 size="11" class="animate-spin" />{:else}<CloudUpload size="11" />{/if}
              Publish
            </button>
            <button
              class="flex items-center gap-1.5 rounded-md border hairline-strong px-3 py-1.5 text-[11px] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
              disabled={teamBusy || !syncStatus?.configured}
              onclick={teamSubscribe}
              title="Pull and merge the team snapshot"
            >
              <CloudDownload size="11" />
              Subscribe
            </button>
          </div>

          {#if teamActivity.length > 0}
            <div class="rounded-md border hairline surface-3 p-2">
              <h4 class="mb-2 text-[10px] uppercase tracking-[0.14em] text-[var(--color-text-3)]">
                Activity ({teamActivity.length})
              </h4>
              <ul class="space-y-1 max-h-48 overflow-y-auto text-[11px]">
                {#each teamActivity as a (a.id)}
                  <li class="flex items-baseline gap-2 text-[var(--color-text-2)]">
                    <span
                      class="font-mono text-[10px] {a.kind === 'publish'
                        ? 'text-[var(--color-accent)]'
                        : 'text-[var(--color-info)]'}"
                      style="min-width:54px"
                    >
                      {a.kind}
                    </span>
                    <span class="font-mono text-[10px] text-[var(--color-text-4)]"
                      >{a.actor}</span
                    >
                    <span class="flex-1 truncate">{a.summary}</span>
                    <span class="text-[10px] text-[var(--color-text-4)]"
                      >{fmtTime(a.at)}</span
                    >
                  </li>
                {/each}
              </ul>
            </div>
          {/if}
        </div>
      </section>

      <!-- About / Updates -->
      <section class="rounded-lg border hairline surface-2 p-5">
        <div class="mb-4 flex items-center gap-2">
          <Info size="14" class="text-[var(--color-accent)]" />
          <h3 class="text-sm font-semibold">About</h3>
        </div>
        <div class="space-y-3 text-xs text-[var(--color-text-2)]">
          <div class="flex items-center justify-between">
            <span class="text-[var(--color-text-3)]">Version</span>
            <span class="font-mono">{currentVersion || "—"}</span>
          </div>
          <div class="flex items-center justify-between gap-3">
            <span class="text-[var(--color-text-3)]">Updates</span>
            <button
              class="flex items-center gap-1.5 rounded-md border hairline-strong px-3 py-1.5 text-[11px] hover:bg-[var(--color-surface-3)] disabled:opacity-50"
              disabled={checkingUpdate}
              onclick={checkForUpdates}
            >
              {#if checkingUpdate}
                <Loader2 size="11" class="animate-spin" />
              {:else}
                <RefreshCw size="11" />
              {/if}
              Check now
            </button>
          </div>

          {#if updateInfo}
            {#if updateInfo.error}
              <p class="rounded-md border border-[var(--color-warn)]/30 bg-[var(--color-warn)]/10 p-2 text-[11px] text-[var(--color-warn)]">
                {updateInfo.error}
              </p>
            {:else if updateInfo.updateAvailable}
              <div class="rounded-md border border-[var(--color-accent)]/40 bg-[var(--color-accent-soft)] p-3">
                <div class="flex items-center gap-2 text-[12px] font-medium text-[var(--color-text-1)]">
                  <Download size="12" />
                  v{updateInfo.latest} is available
                </div>
                {#if updateInfo.notes}
                  <pre
                    class="mt-2 max-h-40 overflow-y-auto whitespace-pre-wrap text-[11px] text-[var(--color-text-2)]">{updateInfo.notes}</pre>
                {/if}
                <button
                  class="mt-3 rounded-md bg-[var(--color-accent)] px-3 py-1.5 text-[11px] font-medium text-[var(--color-surface-0)] hover:opacity-90"
                  onclick={openReleasePage}
                >
                  Open release page
                </button>
              </div>
            {:else}
              <p class="text-[11px] text-[var(--color-text-3)]">
                You're on the latest version.
              </p>
            {/if}
          {/if}
        </div>
      </section>
    </div>
  </div>
</div>
