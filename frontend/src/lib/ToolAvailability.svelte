<script lang="ts">
  import { InfrastructureService } from '../../bindings/github.com/blacknode/blacknode/internal/service';
  let { hostID = '', tools }: { hostID?: string; tools: string[] } = $props();
  let result = $state<Record<string, boolean | undefined> | null>(null);
  let error = $state('');
  let busy = $state(false);
  let generation = 0;
  const guidance: Record<string, string> = {
    git: 'Install Git on this machine and configure credentials for your repository.',
    helm: 'Install Helm on the selected machine and configure cluster access.',
    trivy: 'Install Trivy on the selected machine. The first scan may download its vulnerability database.',
    aws: 'Install AWS CLI and sign in to the chosen profile.',
    'session-manager-plugin': 'Install the AWS Session Manager plugin alongside AWS CLI.',
    sh: 'Install a POSIX shell on the selected machine for command execution.',
    mktemp: 'Install coreutils or an equivalent mktemp utility to prepare chart archives.',
    sha256sum: 'Install coreutils or an equivalent sha256sum utility to verify chart archives.',
  };
  $effect(() => { hostID; tools.join(','); generation++; result = null; error = ''; busy = false; });
  async function check() {
    const current = ++generation;
    busy = true; error = '';
    try {
      const next = await InfrastructureService.CheckTools(hostID, tools);
      if (current === generation) result = next;
    } catch (e) { if (current === generation) error = String(e); }
    finally { if (current === generation) busy = false; }
  }
</script>
<div class="ops-card ops-form">
  <div class="ops-actions"><span>Required tools: {tools.join(', ')}</span><button disabled={busy} onclick={check}>{busy ? 'Checking tools…' : 'Check tools'}</button></div>
  {#if result}<div role="status">{#each tools as tool}<p class={result[tool] ? 'ops-success' : 'ops-warning'}>{tool}: {result[tool] ? 'available' : 'missing'}</p>{#if !result[tool]}<p class="ops-muted">{guidance[tool]}</p>{/if}{/each}</div>{/if}
  {#if error}<p class="ops-error" role="alert">{error}</p>{/if}
</div>
