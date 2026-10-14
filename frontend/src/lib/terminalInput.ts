// Keep stdin ordered, but combine input received while a bridge call is in
// flight. A slow desktop reply should cost one round trip for the accumulated
// text, not one additional round trip for every keystroke in the backlog.
export function createTerminalInputQueue(send: (data: string) => Promise<unknown>) {
  let pending = "";
  let sending = false;
  let scheduled = false;
  let disposed = false;

  async function flush() {
    if (sending || disposed || !pending) return;
    const data = pending;
    pending = "";
    sending = true;
    try {
      await send(data);
    } catch {
      // Never retry stdin: the backend may already have written it.
    } finally {
      sending = false;
      void flush();
    }
  }

  return {
    write(data: string) {
      if (disposed || !data) return;
      pending += data;
      if (sending || scheduled) return;
      scheduled = true;
      queueMicrotask(() => {
        scheduled = false;
        void flush();
      });
    },
    reset() {
      // A replacement shell must never receive input queued for the old one.
      pending = "";
    },
    dispose() {
      disposed = true;
      pending = "";
    },
  };
}
