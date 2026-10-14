<script lang="ts">
  import { onMount } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import { Events } from '@wailsio/runtime';
  import '@xterm/xterm/css/xterm.css';
  type Output = {seq:number;data:string;truncated:boolean;closed:boolean};
  let { sessionID, readonly = false, connect, write, resize, disconnect, read }: {
    sessionID:string;readonly?:boolean;connect:(cols:number,rows:number)=>Promise<unknown>;write:(data:string)=>Promise<unknown>;resize:(cols:number,rows:number)=>Promise<unknown>;disconnect?:()=>Promise<unknown>;read?:(after:number)=>Promise<Output>;
  } = $props();
  let container: HTMLDivElement; let error = $state(''); let status = $state('Connecting…');
  onMount(() => {
    let disposed = false; let ready = false; let ended = false; let sequence = 0; let timer: ReturnType<typeof setTimeout>; let writes = Promise.resolve();
    const terminal = new Terminal({fontFamily:'JetBrains Mono Variable, monospace',fontSize:13,convertEol:false,disableStdin:readonly,theme:{background:'#0b0e14',foreground:'#d8dee9'}});
    const fit = new FitAddon();terminal.loadAddon(fit);terminal.open(container);fit.fit();
    const dataOff = Events.On('terminal:data',(e: any) => {if (!read && e.data?.sessionID === sessionID) terminal.write(e.data.data);});
    const exitOff = Events.On('terminal:exit',(e: any) => {if (!read && e.data?.sessionID === sessionID){ended = true;ready = false;status = e.data.reason || 'Session ended';}});
    const input = terminal.onData(data => {if(!ready || readonly)return;writes = writes.then(async () => {if(!disposed) await write(data);}).catch(e => {if(!disposed)error = String(e);});});
    const observer = new ResizeObserver(() => {fit.fit();if(ready && !readonly)void resize(terminal.cols,terminal.rows).catch(e => {if(!disposed)error = String(e);});});observer.observe(container);
    async function poll() {if(disposed || !read)return;try{const output = await read(sequence);if(disposed)return;sequence = output.seq;if(output.truncated)terminal.writeln('\r\n[Earlier output is no longer in the live buffer]\r\n');if(output.data)terminal.write(Uint8Array.from(atob(output.data),c => c.charCodeAt(0)));if(output.closed){ready = false;status = 'Session ended';return;}}catch(e){if(!disposed){error = String(e);ready = false;status = 'Disconnected';}return;}timer = setTimeout(poll,250);}
    void connect(terminal.cols,terminal.rows).then(async () => {if(disposed){await disconnect?.();return;}if(ended)return;ready = true;status = readonly ? 'Observer · read only' : 'Connected';terminal.focus();if(read)void poll();}).catch(e => {if(!disposed){error = String(e);status = 'Connection failed';}});
    return () => {disposed = true;clearTimeout(timer);observer.disconnect();dataOff();exitOff();input.dispose();terminal.dispose();void disconnect?.().catch(() => {});};
  });
</script>
<div class="ops-card ops-form"><p class="ops-muted" role="status">{status}</p>{#if error}<p class="ops-error" role="alert">{error}</p>{/if}<div bind:this={container} class="h-[430px] min-w-0 overflow-hidden" aria-label="Remote terminal"></div></div>
