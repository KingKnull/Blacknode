export type ShellEvent =
  | { kind: "output"; value: string }
  | { kind: "start" | "prompt" }
  | { kind: "end"; code: number }
  | { kind: "command" | "cwd" | "branch"; value: string };

// PTY reads may split an OSC sequence at any byte. Keep incomplete sequences
// across frames instead of matching only the latest terminal:data chunk.
export class ShellIntegrationParser {
  private pending = "";
  feed(chunk: string): ShellEvent[] {
    this.pending += chunk;
    const events: ShellEvent[] = [];
    while (this.pending.length) {
      const begin = this.pending.indexOf("\x1b]");
      if (begin < 0) {
        const keep = this.pending.endsWith("\x1b") ? 1 : 0;
        const value = this.pending.slice(0, this.pending.length - keep);
        if (value) events.push({ kind: "output", value });
        this.pending = keep ? "\x1b" : "";
        break;
      }
      if (begin > 0) events.push({ kind: "output", value: this.pending.slice(0, begin) });
      this.pending = this.pending.slice(begin);
      const bell = this.pending.indexOf("\x07", 2);
      const st = this.pending.indexOf("\x1b\\", 2);
      const end = bell < 0 ? st : st < 0 ? bell : Math.min(bell, st);
      if (end < 0) { if (this.pending.length > 65536) this.pending = ""; break; }
      const payload = this.pending.slice(2, end);
      this.pending = this.pending.slice(end + (end === st ? 2 : 1));
      if (payload === "133;C") events.push({ kind: "start" });
      else if (payload === "133;A") events.push({ kind: "prompt" });
      else if (/^133;D;\d+$/.test(payload)) events.push({ kind: "end", code: Number(payload.split(";")[2]) });
      else {
        const match = /^777;blacknode;(command|cwd|branch);([A-Za-z0-9+/=]*)$/.exec(payload);
        if (match) {
          try {
            const bytes = Uint8Array.from(atob(match[2]), (c) => c.charCodeAt(0));
            const value = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
            if (!value.includes("\0")) events.push({ kind: match[1] as "command" | "cwd" | "branch", value });
          } catch { /* Malformed shell metadata never interrupts terminal output. */ }
        }
      }
    }
    return events;
  }
  reset() { this.pending = ""; }
}
