<script lang="ts">
  import { app } from "./state.svelte";
  import ErrorBoundary from "./ErrorBoundary.svelte";

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
</script>

<div class:hidden={app.view !== 'terminals'}>
  {@render children()}
</div>

{#if app.view === 'exec'}
  <ErrorBoundary name="Multi-host">
    <ExecPanel />
  </ErrorBoundary>
{:else if app.view === 'files'}
  <ErrorBoundary name="Files">
    <SFTPPanel />
  </ErrorBoundary>
{:else if app.view === 'metrics'}
  <ErrorBoundary name="Metrics">
    <MetricsPanel />
  </ErrorBoundary>
{:else if app.view === 'logs'}
  <ErrorBoundary name="Logs">
    <LogsPanel />
  </ErrorBoundary>
{:else if app.view === 'forwards'}
  <ErrorBoundary name="Forwards">
    <ForwardsPanel />
  </ErrorBoundary>
{:else if app.view === 'recordings'}
  <ErrorBoundary name="Recordings">
    <RecordingsPanel />
  </ErrorBoundary>
{:else if app.view === 'containers'}
  <ErrorBoundary name="Containers">
    <ContainersPanel />
  </ErrorBoundary>
{:else if app.view === 'network'}
  <ErrorBoundary name="Network">
    <NetworkPanel />
  </ErrorBoundary>
{:else if app.view === 'processes'}
  <ErrorBoundary name="Processes">
    <ProcessesPanel />
  </ErrorBoundary>
{:else if app.view === 'http'}
  <ErrorBoundary name="HTTP">
    <HTTPPanel />
  </ErrorBoundary>
{:else if app.view === 'database'}
  {#await loadDBPanel() then DBPanel}
    <ErrorBoundary name="Database">
      <DBPanel />
    </ErrorBoundary>
  {/await}
{:else if app.view === 'snippets'}
  <ErrorBoundary name="Snippets">
    <SnippetsPanel />
  </ErrorBoundary>
{:else if app.view === 'history'}
  <ErrorBoundary name="History">
    <HistoryPanel />
  </ErrorBoundary>
{:else if app.view === 'topology'}
  <ErrorBoundary name="Topology">
    <TopologyPanel />
  </ErrorBoundary>
{:else if app.view === 'activity'}
  <ErrorBoundary name="Activity">
    <ActivityPanel />
  </ErrorBoundary>
{:else if app.view === 'plugins'}
  <ErrorBoundary name="Plugins">
    <PluginsPanel />
  </ErrorBoundary>
{:else if app.view === 'vault'}
  <ErrorBoundary name="Vault">
    <VaultPanel />
  </ErrorBoundary>
{:else if typeof app.view === 'string' && app.view.startsWith('plugin:')}
  {@const parts = (app.view as string).split(':')}
  {@const pluginID = parts[1]}
  {@const panelID = parts[2]}
  {@const found = app.pluginPanels.find((p) => p.pluginId === pluginID && p.id === panelID)}
  {#if found}
    <iframe title={found.title} class="h-full w-full border-0 bg-transparent" sandbox="allow-scripts" srcdoc={found.html}></iframe>
  {:else}
    <div class="flex h-full items-center justify-center text-xs text-[var(--color-text-3)]">Plugin panel not loaded.</div>
  {/if}
{:else if app.view === 'keys'}
  <ErrorBoundary name="Keys">
    <KeysPanel />
  </ErrorBoundary>
{:else if app.view === 'settings'}
  <ErrorBoundary name="Settings">
    <SettingsPanel />
  </ErrorBoundary>
{/if}
