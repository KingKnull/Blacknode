<script lang="ts">
  import { onMount } from "svelte";
  import {
    SettingsService,
    AIService,
    NotificationService,
    UpdateService,
    SyncService,
  } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type {
    NotifyConfig,
    UpdateInfo,
    SyncStatus,
  } from "../../bindings/github.com/blacknode/blacknode/internal/service/models";
  import type { TeamActivity } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import PageHeader from "./PageHeader.svelte";
  import ConfirmDanger from "./ConfirmDanger.svelte";
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

  // Confirmation dialog for removing the API key
  let confirmClearKey = $state(false);

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
    await SettingsService.SetAnthropicAPIKey("");
    await app.refreshSettings();
    confirmClearKey = false;
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
      app.settings = { ...app.settings, theme: t };
      await app.refreshSettings();
    } finally {
      savingTheme = false;
    }
  }

  let activeSection = $state("ai");
  const SECTIONS = [
    { id: "ai", label: "AI ASSISTANT", Icon: Sparkles },
    { id: "security", label: "SECURITY", Icon: Lock },
    { id: "appearance", label: "APPEARANCE", Icon: Palette },
    { id: "notifications", label: "NOTIFICATIONS", Icon: Bell },
    { id: "shell", label: "LOCAL SHELL", Icon: Activity },
    { id: "sync", label: "CLOUD SYNC", Icon: Cloud },
    { id: "team", label: "TEAM", Icon: Users },
    { id: "about", label: "ABOUT", Icon: Info },
  ];

  function scrollTo(id: string) {
    activeSection = id;
    const el = document.getElementById(`section-${id}`);
    if (el) el.scrollIntoView({ behavior: "smooth" });
  }
</script>

<div class="flex h-full flex-col font-mono">
  <PageHeader
    icon={SettingsIcon}
    title="Settings"
    subtitle="Preferences, security, and integrations"
  />

  <div class="grid flex-1 grid-cols-[200px_1fr] overflow-hidden">
    <!-- Sectioned sidebar -->
    <aside class="flex flex-col gap-px border-r hairline surface-1 py-4">
      {#each SECTIONS as s (s.id)}
        <button
          class="flex items-center gap-3 px-6 py-2.5 text-left type-caption font-bold tracking-widest transition-all {activeSection === s.id ? 'bg-[var(--color-accent)]/8 text-[var(--color-accent)]' : 'text-[var(--color-text-4)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-text-2)]'}"
          onclick={() => scrollTo(s.id)}
        >
          <s.Icon size="12" />
          {s.label}
        </button>
      {/each}
    </aside>

    <div class="flex-1 overflow-y-auto p-10">
      <div class="mx-auto max-w-2xl space-y-12 pb-20">

        <!-- AI / Anthropic -->
        <section id="section-ai" class="border hairline-strong surface-2 p-6 shadow-xl" style="backdrop-filter: blur(12px) saturate(1.2);">
          <div class="mb-4 flex items-center gap-2">
            <Sparkles size="14" class="text-[var(--color-accent)]" />
            <h3 class="type-eyebrow text-[var(--color-text-1)]">AI assistant</h3>
          </div>
          <p class="type-caption text-[var(--color-text-3)] leading-relaxed">
            Powers the side drawer (⌘I) and command-palette translations. Uses
            <span class="font-mono">claude-haiku-4-5</span> for low-latency
            suggestions and <span class="font-mono">claude-sonnet-4-6</span> for
            analyses. Your key is encrypted at rest with the vault — it never
            leaves your machine in plaintext.
          </p>

          <div class="mt-4 space-y-2">
            <label class="block">
              <span class="type-eyebrow text-[var(--color-text-3)]">Anthropic API key</span>
              <div class="mt-1 flex items-center gap-2">
                <div class="relative flex flex-1 items-center border hairline bg-[var(--color-surface-3)]">
                  <input
                    type={showKey ? "text" : "password"}
                    class="flex-1 bg-transparent px-3 py-2 font-mono type-caption outline-none"
                    placeholder={app.settings.hasAnthropicKey ? "•••••• (saved)" : "sk-ant-…"}
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
                  class="flex items-center gap-1 border border-[var(--color-accent)]/60 bg-[var(--color-accent)] px-3 py-2 type-caption font-bold text-[var(--color-surface-0)] hover:opacity-90 hover:shadow-[0_0_20px_rgba(59, 130, 246,0.15)] transition-all disabled:opacity-50"
                  disabled={!apiKeyInput || savingKey}
                  onclick={saveAPIKey}
                >
                  {#if savingKey}<Loader2 size="11" class="animate-spin" />{:else}SAVE{/if}
                </button>
              </div>
            </label>

            <div class="flex flex-wrap items-center gap-2">
              {#if app.settings.hasAnthropicKey}
                <span class="inline-flex items-center gap-1 border border-[var(--color-accent)]/30 bg-[var(--color-accent)]/10 px-2 py-0.5 type-micro text-[var(--color-accent)]">
                  <CheckCircle2 size="10" /> KEY SAVED
                </span>
                <button
                  class="border hairline px-2 py-1 type-micro text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-text-1)] transition-colors"
                  onclick={testKey}
                  disabled={savingKey}
                >
                  TEST
                </button>
                <button
                  class="border hairline px-2 py-1 type-micro text-[var(--color-text-3)] hover:border-[var(--color-danger)]/40 hover:text-[var(--color-danger)] transition-colors"
                  onclick={() => (confirmClearKey = true)}
                >
                  REMOVE
                </button>
              {/if}
              {#if testStatus === "ok"}
                <span class="font-mono type-micro text-[var(--color-accent)]">{testMessage}</span>
              {:else if testStatus === "fail"}
                <span class="font-mono type-micro text-[var(--color-danger)]">{testMessage}</span>
              {/if}
            </div>
          </div>
        </section>

        <!-- Security -->
        <section id="section-security" class="border hairline-strong surface-2 p-6 shadow-xl" style="backdrop-filter: blur(12px) saturate(1.2);">
          <div class="mb-4 flex items-center gap-2">
            <Lock size="14" class="text-[var(--color-accent)]" />
            <h3 class="type-eyebrow text-[var(--color-text-1)]">Security</h3>
          </div>
          <label class="block">
            <div class="flex items-center justify-between">
              <span class="type-caption font-bold text-[var(--color-text-1)]">Auto-lock vault after</span>
              <span class="type-micro text-[var(--color-text-3)]">0 = never lock</span>
            </div>
            <p class="mt-0.5 type-caption text-[var(--color-text-3)] leading-relaxed">
              Locks the vault when the app sees no keystroke or click for this many
              minutes. The master key is wiped from memory.
            </p>
            <div class="mt-2 flex items-center gap-2">
              <input
                type="number"
                min="0"
                class="w-24 border hairline bg-[var(--color-surface-3)] px-3 py-2 type-caption outline-none focus:border-[var(--color-accent)]/50 focus:shadow-[0_0_12px_rgba(59, 130, 246,0.06)] transition-all"
                bind:value={autoLockMinutes}
              />
              <span class="type-caption text-[var(--color-text-3)]">minutes</span>
              <button
                class="ml-auto flex items-center gap-1 border hairline-strong px-3 py-1.5 type-caption hover:bg-[var(--color-surface-3)] disabled:opacity-50 transition-colors"
                disabled={savingLock || autoLockMinutes === app.settings.autoLockMinutes}
                onclick={saveAutoLock}
              >
                {#if savingLock}<Loader2 size="11" class="animate-spin" />{:else}SAVE{/if}
              </button>
            </div>
          </label>
        </section>

        <!-- Appearance -->
        <section id="section-appearance" class="border hairline-strong surface-2 p-6 shadow-xl" style="backdrop-filter: blur(12px) saturate(1.2);">
          <div class="mb-4 flex items-center gap-2">
            <Palette size="14" class="text-[var(--color-accent)]" />
            <h3 class="type-eyebrow text-[var(--color-text-1)]">Appearance</h3>
          </div>
          <p class="type-caption text-[var(--color-text-3)] leading-relaxed">
            Switches every panel between the dark navy palette and a clean light variant.
            Active terminals keep their theme until reopened.
          </p>
          <div class="mt-4 flex items-center gap-2">
            <button
              class="flex flex-1 items-center justify-center gap-2 border px-4 py-3 type-caption transition-colors {app.settings.theme === 'dark' ? 'border-[var(--color-accent)]/40 bg-[var(--color-accent-soft)] text-[var(--color-text-1)]' : 'hairline text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)]'}"
              disabled={savingTheme}
              onclick={() => setTheme("dark")}
            >
              <div class="h-3 w-6 border hairline-strong" style="background:#0b0e14"></div>
              DARK
            </button>
            <button
              class="flex flex-1 items-center justify-center gap-2 border px-4 py-3 type-caption transition-colors {app.settings.theme === 'light' ? 'border-[var(--color-accent)]/40 bg-[var(--color-accent-soft)] text-[var(--color-text-1)]' : 'hairline text-[var(--color-text-3)] hover:bg-[var(--color-surface-3)]'}"
              disabled={savingTheme}
              onclick={() => setTheme("light")}
            >
              <div class="h-3 w-6 border hairline-strong" style="background:#ffffff"></div>
              LIGHT
            </button>
          </div>
        </section>

        <!-- Notifications -->
        <section id="section-notifications" class="border hairline-strong surface-2 p-6 shadow-xl" style="backdrop-filter: blur(12px) saturate(1.2);">
          <div class="mb-4 flex items-center gap-2">
            <Bell size="14" class="text-[var(--color-accent)]" />
            <h3 class="type-eyebrow text-[var(--color-text-1)]">Notifications</h3>
          </div>
          <p class="type-caption text-[var(--color-text-3)] leading-relaxed">
            Fires on long-running multi-host completions and CPU/MEM/DISK over
            90% (debounced 5 minutes per host). Desktop notifications use your
            OS notification center; webhooks POST a JSON payload.
          </p>

          <div class="mt-4 space-y-3">
            <label class="flex items-center justify-between">
              <span class="type-caption font-bold text-[var(--color-text-1)]">Desktop notifications</span>
              <input type="checkbox" class="accent-[var(--color-accent)]" bind:checked={notify.desktopEnabled} />
            </label>

            <label class="block">
              <span class="type-eyebrow text-[var(--color-text-3)]">Long-exec threshold (seconds)</span>
              <p class="mt-0.5 type-caption text-[var(--color-text-3)] leading-relaxed">
                Multi-host runs that take longer than this fire a "finished" notification.
              </p>
              <input
                type="number"
                min="1"
                class="mt-1 w-32 border hairline bg-[var(--color-surface-3)] px-3 py-2 type-caption outline-none focus:border-[var(--color-accent)]/50 focus:shadow-[0_0_12px_rgba(59, 130, 246,0.06)] transition-all"
                bind:value={notify.longExecSeconds}
              />
            </label>

            <label class="block">
              <span class="type-eyebrow text-[var(--color-text-3)]">Webhook URL</span>
              <p class="mt-0.5 type-caption text-[var(--color-text-3)] leading-relaxed">
                POSTs <span class="font-mono">{"{kind, title, body, source, hostName, timestamp}"}</span> on every notification.
              </p>
              <input
                class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-caption outline-none focus:border-[var(--color-accent)]/50 focus:shadow-[0_0_12px_rgba(59, 130, 246,0.06)] transition-all"
                placeholder="https://hooks.slack.com/services/…"
                bind:value={notify.webhookURL}
              />
            </label>

            <div class="flex flex-wrap items-center gap-2">
              <button
                class="flex items-center gap-1.5 border border-[var(--color-accent)]/60 bg-[var(--color-accent)] px-3 py-1.5 type-caption font-bold text-[var(--color-surface-0)] hover:opacity-90 hover:shadow-[0_0_20px_rgba(59, 130, 246,0.15)] transition-all disabled:opacity-50"
                disabled={notifyBusy}
                onclick={saveNotify}
              >
                {#if notifyBusy}<Loader2 size="11" class="animate-spin" />{:else}SAVE{/if}
              </button>
              <button
                class="flex items-center gap-1.5 border hairline-strong px-3 py-1.5 type-caption hover:bg-[var(--color-surface-3)] transition-colors"
                onclick={testNotify}
              >
                <Send size="11" /> SEND TEST
              </button>
              {#if notifyTested === "ok"}
                <span class="type-micro text-[var(--color-accent)]">TEST FIRED</span>
              {:else if notifyTested === "fail"}
                <span class="type-micro text-[var(--color-danger)]">TEST FAILED</span>
              {/if}
            </div>
          </div>
        </section>

        <!-- Terminal / Shell -->
        <section id="section-shell" class="border hairline-strong surface-2 p-6 shadow-xl" style="backdrop-filter: blur(12px) saturate(1.2);">
          <div class="mb-4 flex items-center gap-2">
            <Activity size="14" class="text-[var(--color-accent)]" />
            <h3 class="type-eyebrow text-[var(--color-text-1)]">Local shell & metrics</h3>
          </div>

          <label class="block">
            <span class="type-caption font-bold text-[var(--color-text-1)]">Default local shell</span>
            <p class="mt-0.5 type-caption text-[var(--color-text-3)] leading-relaxed">
              Override the shell binary for new local tabs. Empty = auto-detect
              (<span class="font-mono">$SHELL</span> → bash → sh on Unix; pwsh → cmd on Windows).
            </p>
            <div class="mt-2 flex items-center gap-2">
              <input
                class="flex-1 border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-caption outline-none focus:border-[var(--color-accent)]/50 focus:shadow-[0_0_12px_rgba(59, 130, 246,0.06)] transition-all"
                placeholder="auto"
                bind:value={defaultShellPath}
              />
              <button
                class="border hairline-strong px-3 py-1.5 type-caption hover:bg-[var(--color-surface-3)] disabled:opacity-50 transition-colors"
                disabled={savingShell || defaultShellPath === app.settings.defaultShellPath}
                onclick={saveShell}
              >
                SAVE
              </button>
            </div>
          </label>

          <label class="mt-4 block">
            <span class="type-caption font-bold text-[var(--color-text-1)]">Metrics polling interval</span>
            <p class="mt-0.5 type-caption text-[var(--color-text-3)] leading-relaxed">
              Seconds between CPU/MEM/DISK polls per host (≥ 2).
            </p>
            <div class="mt-2 flex items-center gap-2">
              <input
                type="number"
                min="2"
                class="w-24 border hairline bg-[var(--color-surface-3)] px-3 py-2 type-caption outline-none focus:border-[var(--color-accent)]/50 focus:shadow-[0_0_12px_rgba(59, 130, 246,0.06)] transition-all"
                bind:value={metricsIntervalSeconds}
              />
              <span class="type-caption text-[var(--color-text-3)]">seconds</span>
              <button
                class="ml-auto border hairline-strong px-3 py-1.5 type-caption hover:bg-[var(--color-surface-3)] disabled:opacity-50 transition-colors"
                disabled={savingMetrics || metricsIntervalSeconds === app.settings.metricsIntervalSeconds}
                onclick={saveMetrics}
              >
                SAVE
              </button>
            </div>
          </label>
        </section>

        <!-- Cloud sync -->
        <section id="section-sync" class="border hairline-strong surface-2 p-6 shadow-xl" style="backdrop-filter: blur(12px) saturate(1.2);">
          <div class="mb-4 flex items-center gap-2">
            <Cloud size="14" class="text-[var(--color-accent)]" />
            <h3 class="type-eyebrow text-[var(--color-text-1)]">Cloud sync</h3>
          </div>
          <p class="type-caption text-[var(--color-text-3)] leading-relaxed">
            Push hosts, snippets, and saved HTTP requests as a single AES-GCM-encrypted blob to any
            HTTP endpoint accepting PUT/GET on <span class="font-mono">/blacknode-sync.bin</span>.
            Private SSH keys are NOT synced.
          </p>

          <div class="mt-4 grid grid-cols-1 gap-3">
            <label class="block">
              <span class="type-eyebrow text-[var(--color-text-3)]">Endpoint</span>
              <input
                type="text"
                class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-caption outline-none focus:border-[var(--color-accent)]/50 focus:shadow-[0_0_12px_rgba(59, 130, 246,0.06)] transition-all"
                placeholder="https://sync.example.com/blacknode"
                bind:value={syncEndpoint}
              />
            </label>
            <label class="block">
              <span class="type-eyebrow text-[var(--color-text-3)]">Bearer token (optional)</span>
              <input
                type="password"
                class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-caption outline-none focus:border-[var(--color-accent)]/50 focus:shadow-[0_0_12px_rgba(59, 130, 246,0.06)] transition-all"
                placeholder="leave blank if endpoint is public"
                bind:value={syncToken}
              />
            </label>
            <div class="flex items-center gap-2">
              <button
                class="border hairline-strong px-3 py-1.5 type-caption hover:bg-[var(--color-surface-3)] disabled:opacity-50 transition-colors"
                disabled={syncBusy}
                onclick={saveSyncConfig}
              >
                SAVE CONFIG
              </button>
              <button
                class="ml-auto flex items-center gap-1.5 border border-[var(--color-accent)]/60 bg-[var(--color-accent)] px-3 py-1.5 type-caption font-bold text-[var(--color-surface-0)] hover:opacity-90 hover:shadow-[0_0_20px_rgba(59, 130, 246,0.15)] transition-all disabled:opacity-50"
                disabled={syncBusy || !syncStatus?.configured}
                onclick={syncPush}
                title="Encrypt and upload current state"
              >
                {#if syncBusy}<Loader2 size="11" class="animate-spin" />{:else}<CloudUpload size="11" />{/if}
                PUSH
              </button>
              <button
                class="flex items-center gap-1.5 border hairline-strong px-3 py-1.5 type-caption hover:bg-[var(--color-surface-3)] disabled:opacity-50 transition-colors"
                disabled={syncBusy || !syncStatus?.configured}
                onclick={syncPull}
                title="Download and merge into local state"
              >
                <CloudDownload size="11" /> PULL
              </button>
            </div>

            {#if syncStatus}
              <div class="space-y-1 border hairline surface-3 p-2 type-caption text-[var(--color-text-2)]">
                <div class="flex justify-between">
                  <span class="text-[var(--color-text-3)]">Last push</span>
                  <span class="font-mono">{fmtTime(syncStatus.lastPushAt)}</span>
                </div>
                <div class="flex justify-between">
                  <span class="text-[var(--color-text-3)]">Last pull</span>
                  <span class="font-mono">{fmtTime(syncStatus.lastPullAt)}</span>
                </div>
                {#if syncStatus.lastError}
                  <p class="mt-1 border border-[var(--color-danger)]/30 bg-[var(--color-danger)]/10 p-1.5 text-[var(--color-danger)]">
                    {syncStatus.lastError}
                  </p>
                {/if}
              </div>
            {/if}
          </div>
        </section>

        <!-- Team -->
        <section id="section-team" class="border hairline-strong surface-2 p-6 shadow-xl" style="backdrop-filter: blur(12px) saturate(1.2);">
          <div class="mb-4 flex items-center gap-2">
            <Users size="14" class="text-[var(--color-accent)]" />
            <h3 class="type-eyebrow text-[var(--color-text-1)]">Team</h3>
          </div>
          <p class="type-caption text-[var(--color-text-3)] leading-relaxed">
            Publishes a curated snapshot to a shared blob
            (<span class="font-mono">/blacknode-team.bin</span>) on the same endpoint as Cloud sync.
            Notes are stripped; password-auth hosts are excluded. Subscribers merge with last-write-wins.
          </p>

          <div class="mt-4 grid grid-cols-1 gap-3">
            <label class="block">
              <span class="type-eyebrow text-[var(--color-text-3)]">Display name (for activity log)</span>
              <input
                type="text"
                class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 type-caption outline-none focus:border-[var(--color-accent)]/50 focus:shadow-[0_0_12px_rgba(59, 130, 246,0.06)] transition-all"
                placeholder="e.g. alice"
                bind:value={teamActor}
              />
            </label>
            <div class="flex items-center gap-2">
              <button
                class="ml-auto flex items-center gap-1.5 border border-[var(--color-accent)]/60 bg-[var(--color-accent)] px-3 py-1.5 type-caption font-bold text-[var(--color-surface-0)] hover:opacity-90 hover:shadow-[0_0_20px_rgba(59, 130, 246,0.15)] transition-all disabled:opacity-50"
                disabled={teamBusy || !syncStatus?.configured}
                onclick={teamPublish}
                title="Push a curated snapshot to the team blob"
              >
                {#if teamBusy}<Loader2 size="11" class="animate-spin" />{:else}<CloudUpload size="11" />{/if}
                PUBLISH
              </button>
              <button
                class="flex items-center gap-1.5 border hairline-strong px-3 py-1.5 type-caption hover:bg-[var(--color-surface-3)] disabled:opacity-50 transition-colors"
                disabled={teamBusy || !syncStatus?.configured}
                onclick={teamSubscribe}
                title="Pull and merge the team snapshot"
              >
                <CloudDownload size="11" /> SUBSCRIBE
              </button>
            </div>

            {#if teamActivity.length > 0}
              <div class="border hairline surface-3 p-2">
                <h4 class="mb-2 type-eyebrow text-[var(--color-text-3)]">
                  Activity ({teamActivity.length})
                </h4>
                <ul class="max-h-48 space-y-1 overflow-y-auto type-caption">
                  {#each teamActivity as a (a.id)}
                    <li class="flex items-baseline gap-2 text-[var(--color-text-2)]">
                      <span
                        class="font-mono type-micro {a.kind === 'publish' ? 'text-[var(--color-accent)]' : 'text-[var(--color-info)]'}"
                        style="min-width:54px"
                      >{a.kind}</span>
                      <span class="font-mono type-micro text-[var(--color-text-4)]">{a.actor}</span>
                      <span class="flex-1 truncate">{a.summary}</span>
                      <span class="type-micro text-[var(--color-text-4)]">{fmtTime(a.at)}</span>
                    </li>
                  {/each}
                </ul>
              </div>
            {/if}
          </div>
        </section>

        <!-- About / Updates -->
        <section id="section-about" class="border hairline-strong surface-2 p-6 shadow-xl" style="backdrop-filter: blur(12px) saturate(1.2);">
          <div class="mb-4 flex items-center gap-2">
            <Info size="14" class="text-[var(--color-accent)]" />
            <h3 class="type-eyebrow text-[var(--color-text-1)]">About</h3>
          </div>
          <div class="space-y-3 type-caption text-[var(--color-text-2)]">
            <div class="flex items-center justify-between">
              <span class="text-[var(--color-text-3)]">Version</span>
              <span class="font-mono">{currentVersion || "—"}</span>
            </div>
            <div class="flex items-center justify-between gap-3">
              <span class="text-[var(--color-text-3)]">Updates</span>
              <button
                class="flex items-center gap-1.5 border hairline-strong px-3 py-1.5 type-caption hover:bg-[var(--color-surface-3)] disabled:opacity-50 transition-colors"
                disabled={checkingUpdate}
                onclick={checkForUpdates}
              >
                {#if checkingUpdate}<Loader2 size="11" class="animate-spin" />{:else}<RefreshCw size="11" />{/if}
                CHECK NOW
              </button>
            </div>

            {#if updateInfo}
              {#if updateInfo.error}
                <p class="border border-[var(--color-warn)]/30 bg-[var(--color-warn)]/10 p-2 type-caption text-[var(--color-warn)]">
                  {updateInfo.error}
                </p>
              {:else if updateInfo.updateAvailable}
                <div class="border border-[var(--color-accent)]/40 bg-[var(--color-accent-soft)] p-3">
                  <div class="flex items-center gap-2 type-caption font-bold text-[var(--color-text-1)]">
                    <Download size="12" /> v{updateInfo.latest} is available
                  </div>
                  {#if updateInfo.notes}
                    <pre class="mt-2 max-h-40 overflow-y-auto whitespace-pre-wrap type-caption text-[var(--color-text-2)]">{updateInfo.notes}</pre>
                  {/if}
                  <button
                    class="mt-3 border border-[var(--color-accent)]/60 bg-[var(--color-accent)] px-3 py-1.5 type-caption font-bold text-[var(--color-surface-0)] hover:opacity-90 hover:shadow-[0_0_20px_rgba(59, 130, 246,0.15)] transition-all"
                    onclick={openReleasePage}
                  >
                    OPEN RELEASE PAGE
                  </button>
                </div>
              {:else}
                <p class="type-caption text-[var(--color-text-3)]">You're on the latest version.</p>
              {/if}
            {/if}
          </div>
        </section>

      </div>
    </div>
  </div>
</div>

<!-- ConfirmDanger for API key removal -->
{#if confirmClearKey}
  <ConfirmDanger
    title="REMOVE API KEY"
    body="This will delete the stored Anthropic API key from your vault. The AI assistant and command-palette translations will stop working until a new key is saved."
    severity="warn"
    productionHosts={[]}
    onCancel={() => (confirmClearKey = false)}
    onConfirm={clearAPIKey}
  />
{/if}
