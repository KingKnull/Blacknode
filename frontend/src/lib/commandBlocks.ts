// A shell command paired with its output, duration and exit code — the unit
// behind the terminal side panel's "Blocks" tab. Terminal.svelte produces
// these from the PTY stream; TerminalSidePanel.svelte renders them.

export type CommandBlock = {
  id: string;
  command: string;
  startedAt: number;
  endedAt?: number;
  exitCode?: number;
  running: boolean;
  output: string;
};

/** Elapsed time for a block. Still-running blocks measure against `now`. */
export function commandDuration(block: CommandBlock, now: number = Date.now()): string {
  const ended = block.endedAt ?? now;
  const ms = Math.max(0, ended - block.startedAt);
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
}
