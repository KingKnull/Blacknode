import type { Danger } from "./danger";

export type BroadcastInputDecision = {
  line: string;
  forward: boolean;
  command?: string;
  danger?: Danger;
};

/**
 * Reconstruct the line behind a broadcast source and decide whether this input
 * may leave the source pane. Enter is deliberately decided before it is
 * forwarded, so a dangerous command can be held without submitting it on any
 * pane (including the pane where it was typed).
 */
export function evaluateBroadcastInput(
  line: string,
  data: string,
  check: (command: string) => Danger | null,
): BroadcastInputDecision {
  // A bracketed paste or IME can deliver text and Enter in one input event.
  // Inspect each submitted segment before allowing the whole event through;
  // otherwise a dangerous pasted command could bypass the single-key path.
  const parts = data.split(/\r\n|\r|\n/);
  let nextLine = line;
  for (let i = 0; i < parts.length; i++) {
    const text = parts[i];
    if (text) nextLine = trackText(nextLine, text);
    if (i === parts.length - 1) continue;
    const command = nextLine;
    const danger = check(command);
    if (danger) return { line: "", forward: false, command, danger };
    nextLine = "";
  }
  return { line: nextLine, forward: true };
}

function trackText(line: string, data: string): string {
  if (data === "\x7f" || data === "\b") return line.slice(0, -1);
  // Ctrl-C, Ctrl-U, Ctrl-D and Escape clear the shell's current line. Other
  // control bytes and escape sequences are navigation rather than text.
  if (data === "\x03" || data === "\x15" || data === "\x04" || data === "\x1b") return "";
  if ((data.length === 1 && data.charCodeAt(0) < 0x20) || data.startsWith("\x1b")) return line;
  return (line + data).slice(-4096);
}
