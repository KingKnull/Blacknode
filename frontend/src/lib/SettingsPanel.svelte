<script lang="ts">
  import { onMount } from "svelte";
  import {
    SettingsService,
    AIService,
    NotificationService,
  } from "../../bindings/github.com/blacknode/blacknode";
  import type { NotifyConfig } from "../../bindings/github.com/blacknode/blacknode/models";
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
  } from "@lucide/svelte";

  let apiKeyInput = $state("");
  let showKey = $state(false);
  let savingKey = $state(false);
  let testStatus = $state<"" | "ok" | "fail">("");
  let testMessage = $state("");
  let savingLock = $state(false);
  let savingShell = $state(false);
  let savingMetrics = $state(false);

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

  onMount(async () => {
    autoLockMinutes = app.settings.autoLockMinutes;
    defaultShellPath = app.settings.defaultShellPath;
    metricsIntervalSeconds = app.settings.metricsIntervalSeconds;
    try {
      notify = (await NotificationService.Config()) as NotifyConfig;
    } catch {
      // ignore
    }
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
    </div>
  </div>
</div>
