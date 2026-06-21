<script lang="ts">
  import { HostService, SnippetService, SerialService } from "../../bindings/github.com/blacknode/blacknode/internal/service";
  import type { Host, Snippet } from "../../bindings/github.com/blacknode/blacknode/internal/store/models";
  import { app } from "./state.svelte";
  import { onMount } from "svelte";
  import { Server, X, Loader2, Eye, EyeOff, ShieldCheck } from "@lucide/svelte";
  import Dialog from "./Dialog.svelte";

  const BAUD_RATES = [9600, 19200, 38400, 57600, 115200];

  type Props = {
    host?: Host | null;
    onclose: () => void;
    onsaved: () => void;
  };
  let { host, onclose, onsaved }: Props = $props();

  // svelte-ignore state_referenced_locally
  let name = $state(host?.name ?? "");
  // svelte-ignore state_referenced_locally
  let protocol = $state(host?.protocol || "ssh");
  // svelte-ignore state_referenced_locally
  let hostName = $state(host?.host ?? "");
  // svelte-ignore state_referenced_locally
  let port = $state(host?.port ?? 22);
  // svelte-ignore state_referenced_locally
  let serialDevice = $state(host?.serialDevice ?? "");
  // svelte-ignore state_referenced_locally
  let serialBaud = $state(host?.serialBaud || 115200);
  // svelte-ignore state_referenced_locally
  let serialDataBits = $state(host?.serialDataBits || 8);
  // svelte-ignore state_referenced_locally
  let serialParity = $state(host?.serialParity || "none");
  // svelte-ignore state_referenced_locally
  let serialStopBits = $state(host?.serialStopBits || "1");
  // svelte-ignore state_referenced_locally
  let username = $state(host?.username ?? "");
  // svelte-ignore state_referenced_locally
  let authMethod = $state(host?.authMethod ?? "password");
  // svelte-ignore state_referenced_locally
  let keyID = $state(host?.keyID ?? "");
  // svelte-ignore state_referenced_locally
  let group = $state(host?.group ?? "");
  // svelte-ignore state_referenced_locally
  let environment = $state(host?.environment ?? "");
  // svelte-ignore state_referenced_locally
  let proxyJump = $state(host?.proxyJump ?? "");
  // svelte-ignore state_referenced_locally
  let notes = $state(host?.notes ?? "");
  // svelte-ignore state_referenced_locally
  let startupSnippetID = $state(host?.startupSnippetID ?? "");

  // Snippets for the "run on connect" picker.
  let snippets = $state<Snippet[]>([]);
  // Serial devices detected on this machine, for the device picker.
  let serialPorts = $state<string[]>([]);
  onMount(async () => {
    try { snippets = ((await SnippetService.List()) ?? []) as Snippet[]; } catch { /* ignore */ }
    try { serialPorts = ((await SerialService.Ports()) ?? []) as string[]; } catch { /* ignore */ }
  });
  // Password: pre-fill from the in-memory cache if editing an existing host.
  // svelte-ignore state_referenced_locally
  let password = $state(host?.id ? (app.hostPasswords[host.id] ?? "") : "");
  let showPassword = $state(false);
  // Sudo password: separate from SSH auth password.
  // svelte-ignore state_referenced_locally
  let sudoPassword = $state(host?.id ? (app.hostSudoPasswords[host.id] ?? "") : "");
  let showSudoPassword = $state(false);
  let sudoSameAsSSH = $state(false);
  let busy = $state(false);
  let err = $state("");

  // When switching to telnet, nudge the port off the SSH default to telnet's.
  function onProtocolChange() {
    if (protocol === "telnet" && (port === 22 || !port)) port = 23;
    else if (protocol === "ssh" && port === 23) port = 22;
  }

  async function save() {
    err = "";
    if (!name) {
      err = "Name is required";
      return;
    }
    if (protocol === "serial") {
      if (!serialDevice) { err = "A serial device is required"; return; }
    } else if (!hostName) {
      err = protocol === "telnet" ? "Host is required" : "Name, host, and username are required";
      return;
    } else if (protocol === "ssh" && !username) {
      err = "Name, host, and username are required";
      return;
    }
    busy = true;
    try {
      // Common protocol fields persisted regardless of transport.
      const protoFields = protocol === "serial"
        ? { protocol, serialDevice, serialBaud, serialDataBits, serialParity, serialStopBits }
        : { protocol, serialDevice: "", serialBaud: 0, serialDataBits: 0, serialParity: "", serialStopBits: "" };
      let savedHost: Host;
      if (host?.id) {
        await HostService.Update({
          ...host,
          name,
          host: hostName,
          port,
          username,
          authMethod,
          keyID: authMethod === "key" ? keyID : "",
          group,
          environment,
          proxyJump,
          notes,
          startupSnippetID,
          ...protoFields,
        } as Host);
        savedHost = { ...host, name, host: hostName, port, username, authMethod, keyID, group, environment, proxyJump, notes, startupSnippetID, ...protoFields } as Host;
      } else {
        savedHost = (await HostService.Create({
          name,
          host: hostName,
          port,
          username,
          authMethod,
          keyID: authMethod === "key" ? keyID : "",
          group,
          environment,
          proxyJump,
          notes,
          startupSnippetID,
          ...protoFields,
          tags: [],
        } as unknown as Host)) as Host;
      }
      // Telnet/serial have no SSH credentials — skip password & sudo persistence.
      if (protocol !== "ssh") {
        await app.refreshHosts();
        onsaved();
        return;
      }
      // Persist the password in the vault if auth method is password.
      if (authMethod === "password" && savedHost?.id) {
        try {
          await HostService.SetPassword(savedHost.id, password);
          if (password) {
            app.setPassword(savedHost.id, password);
          }
        } catch (pe: any) {
          // Non-fatal for the host record, but the user must know the
          // credential didn't land (e.g. vault locked) so they can retry.
          app.toast('warn', 'PASSWORD NOT SAVED', String(pe?.message ?? pe));
        }
      }
      // Persist sudo password (works for any auth method).
      if (savedHost?.id) {
        const sudoPw = sudoSameAsSSH ? password : sudoPassword;
        try {
          await HostService.SetSudoPassword(savedHost.id, sudoPw);
          if (sudoPw) {
            app.setSudoPassword(savedHost.id, sudoPw);
          }
        } catch (pe: any) {
          app.toast('warn', 'SUDO PASSWORD NOT SAVED', String(pe?.message ?? pe));
        }
      }
      await app.refreshHosts();
      onsaved();
    } catch (e: any) {
      err = String(e?.message ?? e);
    } finally {
      busy = false;
    }
  }
</script>

<Dialog
  onclose={onclose}
  labelledby="host-editor-title"
  backdropClass="bg-black/[0.82] backdrop-blur-[4px]"
  panelClass="w-[520px] max-h-[85vh] overflow-y-auto overflow-x-hidden border hairline-strong surface-2 shadow-2xl fade-up"
  panelStyle="box-shadow: 0 0 0 1px var(--color-line-strong), 0 0 60px rgba(59, 130, 246,0.04), 0 40px 80px rgba(0,0,0,0.6);"
>
  {#snippet children()}
    <!-- Header -->
    <div class="flex items-center gap-2.5 border-b hairline px-5 py-3">
      <Server size="12" class="text-[var(--color-accent)]" />
      <span id="host-editor-title" class="type-body font-semibold text-[var(--color-text-1)]">
        {host ? 'Edit host' : 'New host'}
      </span>
      <button
        class="ml-auto border border-[var(--color-line)] p-1 text-[var(--color-text-4)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-danger)] transition-all"
        onclick={onclose}
        title="Close"
      >
        <X size="12" />
      </button>
    </div>

    <!-- Form -->
    <div class="space-y-3 p-5">
      <!-- Name -->
      <label class="block">
        <span class="type-caption text-[var(--color-text-4)]">Name</span>
        <input
          class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-body text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
          bind:value={name}
          placeholder="prod-web-01"
          data-autofocus
        />
      </label>

      <!-- Protocol -->
      <label class="block">
        <span class="type-caption text-[var(--color-text-4)]">Protocol</span>
        <select
          class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 type-body text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
          bind:value={protocol}
          onchange={onProtocolChange}
        >
          <option value="ssh">SSH</option>
          <option value="telnet">Telnet (unencrypted)</option>
          <option value="serial">Serial</option>
        </select>
      </label>

      {#if protocol === 'serial'}
        <!-- Serial device + Baud -->
        <div class="grid grid-cols-[1fr_120px] gap-2">
          <label class="block">
            <span class="type-caption text-[var(--color-text-4)]">Serial device</span>
            {#if serialPorts.length > 0}
              <select
                class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-body text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
                bind:value={serialDevice}
              >
                <option value="">— Select device —</option>
                {#each serialPorts as p (p)}
                  <option value={p}>{p}</option>
                {/each}
                {#if serialDevice && !serialPorts.includes(serialDevice)}
                  <option value={serialDevice}>{serialDevice}</option>
                {/if}
              </select>
            {:else}
              <input
                class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-body text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
                bind:value={serialDevice}
                placeholder="/dev/ttyUSB0"
              />
            {/if}
          </label>
          <label class="block">
            <span class="type-caption text-[var(--color-text-4)]">Baud</span>
            <select
              class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-body text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
              bind:value={serialBaud}
            >
              {#each BAUD_RATES as b (b)}
                <option value={b}>{b}</option>
              {/each}
            </select>
          </label>
        </div>
        {#if serialPorts.length === 0}
          <p class="type-caption text-[var(--color-text-4)]">No serial devices detected — type the device path manually.</p>
        {/if}
        <!-- Line settings -->
        <div class="grid grid-cols-3 gap-2">
          <label class="block">
            <span class="type-caption text-[var(--color-text-4)]">Data bits</span>
            <select
              class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-body text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
              bind:value={serialDataBits}
            >
              {#each [8, 7, 6, 5] as d (d)}
                <option value={d}>{d}</option>
              {/each}
            </select>
          </label>
          <label class="block">
            <span class="type-caption text-[var(--color-text-4)]">Parity</span>
            <select
              class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 type-body text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
              bind:value={serialParity}
            >
              <option value="none">None</option>
              <option value="odd">Odd</option>
              <option value="even">Even</option>
            </select>
          </label>
          <label class="block">
            <span class="type-caption text-[var(--color-text-4)]">Stop bits</span>
            <select
              class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-body text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
              bind:value={serialStopBits}
            >
              <option value="1">1</option>
              <option value="1.5">1.5</option>
              <option value="2">2</option>
            </select>
          </label>
        </div>
      {:else}
        <!-- Host + Port -->
        <div class="grid grid-cols-[1fr_88px] gap-2">
          <label class="block">
            <span class="type-caption text-[var(--color-text-4)]">Host</span>
            <input
              class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-body text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
              bind:value={hostName}
              placeholder="10.0.0.5"
            />
          </label>
          <label class="block">
            <span class="type-caption text-[var(--color-text-4)]">Port</span>
            <input
              type="number"
              class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-body text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
              bind:value={port}
            />
          </label>
        </div>
      {/if}

      {#if protocol === 'ssh'}
      <!-- User + Auth -->
      <div class="grid grid-cols-2 gap-2">
        <label class="block">
          <span class="type-caption text-[var(--color-text-4)]">Username</span>
          <input
            class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-body text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
            bind:value={username}
            placeholder="ubuntu"
          />
        </label>
        <label class="block">
          <span class="type-caption text-[var(--color-text-4)]">Auth method</span>
          <select
            class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 type-body text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
            bind:value={authMethod}
          >
            <option value="password">Password</option>
            <option value="key">SSH key</option>
            <option value="agent">Agent</option>
          </select>
        </label>
      </div>

      <!-- Key selector -->
      {#if authMethod === 'key'}
        <label class="block">
          <span class="type-caption text-[var(--color-text-4)]">SSH key</span>
          <select
            class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 type-body text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
            bind:value={keyID}
          >
            <option value="">— Select key —</option>
            {#each app.keys as k (k.id)}
              <option value={k.id}>{k.name} ({k.keyType})</option>
            {/each}
          </select>
        </label>
      {/if}

      <!-- Password -->
      {#if authMethod === 'password'}
        <label class="block">
          <span class="type-caption text-[var(--color-text-4)]">Password <span class="opacity-50">(vault)</span></span>
          <div class="relative mt-1">
            <input
              type={showPassword ? 'text' : 'password'}
              class="w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 pr-9 font-mono type-body text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
              bind:value={password}
              placeholder="leave blank to keep current"
              autocomplete="new-password"
            />
            <button
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 text-[var(--color-text-4)] hover:text-[var(--color-accent)] transition-colors"
              onclick={() => (showPassword = !showPassword)}
              title={showPassword ? 'Hide' : 'Show'}
              aria-label={showPassword ? 'Hide password' : 'Show password'}
              aria-pressed={showPassword}
            >
              {#if showPassword}<EyeOff size="12" />{:else}<Eye size="12" />{/if}
            </button>
          </div>
          <p class="mt-1 type-caption text-[var(--color-text-4)]">AES-256 encrypted · auto-fills at connect time</p>
        </label>
      {/if}
      {/if}

      <!-- Group + Environment -->
      <div class="grid grid-cols-2 gap-2">
        <label class="block">
          <span class="type-caption text-[var(--color-text-4)]">Group</span>
          <input
            class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 font-mono type-body text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
            bind:value={group}
            placeholder="web · db · cache"
          />
        </label>
        <label class="block">
          <span class="type-caption text-[var(--color-text-4)]">Environment</span>
          <select
            class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 type-body outline-none focus:border-[var(--color-accent)]/50 transition-colors {environment === 'production' ? 'text-[var(--color-danger)] border-[var(--color-danger)]/30' : 'text-[var(--color-text-1)]'}"
            bind:value={environment}
          >
            <option value="">— None —</option>
            <option value="dev">Dev</option>
            <option value="staging">Staging</option>
            <option value="production">Production ⚠</option>
          </select>
        </label>
      </div>

      <!-- ProxyJump -->
      {#if protocol === 'ssh'}
      <label class="block">
        <span class="type-caption text-[var(--color-text-4)]">ProxyJump (bastion)</span>
        <select
          class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 type-body text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
          bind:value={proxyJump}
        >
          <option value="">— Direct connect —</option>
          {#each app.hosts.filter((h) => h.id !== host?.id) as h (h.id)}
            <option value={h.name}>{h.name} ({h.username}@{h.host}:{h.port})</option>
          {/each}
        </select>
        <p class="mt-1 type-caption text-[var(--color-text-4)]">Tunnels through selected bastion · cycles detected at connect</p>
      </label>
      {/if}

      <!-- Startup snippet -->
      <label class="block">
        <span class="type-caption text-[var(--color-text-4)]">Startup snippet (run on connect)</span>
        <select
          class="mt-1 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 type-body text-[var(--color-text-1)] outline-none focus:border-[var(--color-accent)]/50 transition-colors"
          bind:value={startupSnippetID}
        >
          <option value="">— None —</option>
          {#each snippets as sn (sn.id)}
            <option value={sn.id}>{sn.name}</option>
          {/each}
        </select>
        <p class="mt-1 type-caption text-[var(--color-text-4)]">Sent automatically once the session connects · variable defaults are used</p>
      </label>

      <!-- Sudo password -->
      {#if protocol === 'ssh'}
      <div class="border hairline surface-3 p-3">
        <div class="flex items-center gap-2 mb-2">
          <ShieldCheck size="11" class="text-[var(--color-warn)]" />
          <span class="type-caption font-semibold text-[var(--color-text-2)]">Sudo password</span>
        </div>
        <label class="flex items-center gap-2 mb-2">
          <input type="checkbox" class="accent-[var(--color-accent)]" bind:checked={sudoSameAsSSH} />
          <span class="type-caption text-[var(--color-text-3)]">Same as SSH password</span>
        </label>
        {#if !sudoSameAsSSH}
          <div class="relative">
            <input
              type={showSudoPassword ? 'text' : 'password'}
              class="w-full border hairline bg-[var(--color-surface-2)] px-3 py-2 pr-9 font-mono type-body text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors"
              bind:value={sudoPassword}
              placeholder="sudo / root password"
              autocomplete="new-password"
            />
            <button
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 text-[var(--color-text-4)] hover:text-[var(--color-accent)] transition-colors"
              onclick={() => (showSudoPassword = !showSudoPassword)}
              title={showSudoPassword ? 'Hide' : 'Show'}
              aria-label={showSudoPassword ? 'Hide sudo password' : 'Show sudo password'}
              aria-pressed={showSudoPassword}
            >
              {#if showSudoPassword}<EyeOff size="12" />{:else}<Eye size="12" />{/if}
            </button>
          </div>
        {/if}
        <p class="mt-1 type-caption text-[var(--color-text-4)]">AES-256 encrypted · auto-fills when sudo prompt detected</p>
      </div>
      {/if}

      <!-- Notes -->
      <label class="block">
        <span class="type-caption text-[var(--color-text-4)]">Notes</span>
        <textarea
          class="mt-1 h-14 w-full border hairline bg-[var(--color-surface-3)] px-3 py-2 type-body text-[var(--color-text-1)] outline-none placeholder:text-[var(--color-text-4)] focus:border-[var(--color-accent)]/50 transition-colors resize-none"
          bind:value={notes}
        ></textarea>
      </label>

      {#if err}
        <p class="type-caption text-[var(--color-danger)]" role="alert">{err}</p>
      {/if}
    </div>

    <!-- Footer -->
    <div class="flex items-center justify-end gap-2 border-t hairline px-5 py-3">
      <button
        class="border border-[var(--color-line)] px-3 py-1.5 type-body text-[var(--color-text-3)] hover:border-[var(--color-line-strong)] hover:text-[var(--color-text-1)] transition-all"
        onclick={onclose}>Cancel</button>
      <button
        class="flex items-center gap-1.5 border border-[var(--color-accent)]/50 bg-[var(--color-accent)]/10 px-3 py-1.5 type-body font-semibold text-[var(--color-accent)] hover:bg-[var(--color-accent)]/18 hover:shadow-[0_0_16px_rgba(59, 130, 246,0.08)] disabled:opacity-30 disabled:cursor-not-allowed transition-all"
        disabled={busy}
        onclick={save}
      >
        {#if busy}<Loader2 size="12" class="animate-spin" /> Saving...{:else}Save host{/if}
      </button>
    </div>
  {/snippet}
</Dialog>
