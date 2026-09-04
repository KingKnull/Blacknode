<script lang="ts">
  import { app } from "./state.svelte";
  import ErrorBoundary from "./ErrorBoundary.svelte";
  import { registerPanelWindow, unregisterPanelWindow } from "./pluginBridge";

  import ExecPanel from "./ExecPanel.svelte";
  import SFTPPanel from "./SFTPPanel.svelte";
  import MetricsPanel from "./MetricsPanel.svelte";
  import KeysPanel from "./KeysPanel.svelte";
  import LogsPanel from "./LogsPanel.svelte";
  import ForwardsPanel from "./ForwardsPanel.svelte";
  import RecordingsPanel from "./RecordingsPanel.svelte";
  import ContainersPanel from "./ContainersPanel.svelte";
  import NetworkPanel from "./NetworkPanel.svelte";
  import ProcessesPanel from "./ProcessesPanel.svelte";
  import HTTPPanel from "./HTTPPanel.svelte";
  import SnippetsPanel from "./SnippetsPanel.svelte";
  import HistoryPanel from "./HistoryPanel.svelte";
  import TopologyPanel from "./TopologyPanel.svelte";
  import PluginsPanel from "./PluginsPanel.svelte";
  import ActivityPanel from "./ActivityPanel.svelte";
  import VaultPanel from "./VaultPanel.svelte";
  import SettingsPanel from "./SettingsPanel.svelte";
  import { Puzzle } from "@lucide/svelte";

  // Heavy panels lazy-loaded to keep the main bundle small.
  const loadDBPanel = () =>
    import("./DBPanel.svelte").then((m) => m.default);

  type Props = {
    /** Slot for the terminals view content (tab bar + panes). */
    children: any;
  };
  let { children }: Props = $props();

  /**
   * Bind a plugin panel iframe's window to the plugin that owns it, so the
   * workspace bridge can identify messages by their sender instead of
   * believing whatever id the panel puts in the message. See pluginBridge.ts.
   *
   * An action rather than an `onload` handler because it gives us the
   * teardown hook: the iframe is destroyed when the view changes, and a
   * window that stays registered after that would let a replaced panel keep
   * speaking for its plugin.
   */
  function panelOwner(node: HTMLIFrameElement, pluginID: string) {
    // contentWindow is available as soon as the element is in the document —
    // it does not wait for srcdoc to finish parsing — so there is no race
    // with a panel that posts a message immediately on load.
    registerPanelWindow(node.contentWindow, pluginID);
    return {
      update(nextID: string) {
        unregisterPanelWindow(node.contentWindow);
        registerPanelWindow(node.contentWindow, nextID);
      },
      destroy() {
        unregisterPanelWindow(node.contentWindow);
      },
    };
  }
</script>

<div class:hidden={app.view !== 'terminals'}>
  {@render children()}
</div>

{#if app.view === 'exec'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Multi-host"><ExecPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'files'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Files"><SFTPPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'metrics'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Metrics"><MetricsPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'logs'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Logs"><LogsPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'forwards'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Forwards"><ForwardsPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'recordings'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Recordings"><RecordingsPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'containers'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Containers"><ContainersPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'network'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Network"><NetworkPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'processes'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Processes"><ProcessesPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'http'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="HTTP"><HTTPPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'database'}
  <div class="view-enter h-full w-full">
    {#await loadDBPanel() then DBPanel}
      <ErrorBoundary name="Database"><DBPanel /></ErrorBoundary>
    {/await}
  </div>
{:else if app.view === 'snippets'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Snippets"><SnippetsPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'history'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="History"><HistoryPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'topology'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Topology"><TopologyPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'activity'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Activity"><ActivityPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'plugins'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Plugins"><PluginsPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'vault'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Vault"><VaultPanel /></ErrorBoundary>
  </div>
{:else if typeof app.view === 'string' && app.view.startsWith('plugin:')}
  {@const parts = (app.view as string).split(':')}
  {@const pluginID = parts[1]}
  {@const panelID = parts[2]}
  {@const found = app.pluginPanels.find((p) => p.pluginId === pluginID && p.id === panelID)}
  <div class="view-enter h-full w-full">
    {#if found}
      <iframe
        use:panelOwner={pluginID}
        title={found.title}
        class="h-full w-full border-0 bg-transparent"
        sandbox="allow-scripts"
        srcdoc={found.html}
      ></iframe>
    {:else}
      <div class="flex h-full items-center justify-center type-caption text-[var(--color-text-3)]">Plugin panel not loaded.</div>
    {/if}
  </div>
{:else if app.view === 'keys'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Keys"><KeysPanel /></ErrorBoundary>
  </div>
{:else if app.view === 'settings'}
  <div class="view-enter h-full w-full">
    <ErrorBoundary name="Settings"><SettingsPanel /></ErrorBoundary>
  </div>
{/if}
