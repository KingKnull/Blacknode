import { GhosttyTerminal, type GhosttyCell, type IRenderable, type Terminal } from "ghostty-web";

type ViewportBuffer = IRenderable & { getViewport(): GhosttyCell[] };

// Ghostty 0.4's getLine reads the entire viewport and clones its cells for
// every requested row. CanvasRenderer only needs one snapshot per frame.
// Keep this adapter local to rendering: search/link callers retain the
// engine's normal snapshot semantics, and cells never survive into a frame
// after output, resize, or an alternate-screen switch.
export function createViewportRenderBuffer(source: ViewportBuffer) {
  let viewport: GhosttyCell[] | undefined;
  const lines = new Map<number, GhosttyCell[]>();
  const buffer: IRenderable = {
    getLine(row) {
      const { cols, rows } = source.getDimensions();
      if (row < 0 || row >= rows) return null;
      let line = lines.get(row);
      if (!line) {
        viewport ??= source.getViewport();
        line = viewport.slice(row * cols, (row + 1) * cols);
        lines.set(row, line);
      }
      return line;
    },
    getCursor: () => source.getCursor(),
    getDimensions: () => source.getDimensions(),
    isRowDirty: (row) => source.isRowDirty(row),
    needsFullRedraw: () => source.needsFullRedraw?.() ?? false,
    clearDirty: () => source.clearDirty(),
    getGraphemeString: (row, col) => source.getGraphemeString?.(row, col) ?? "",
  };
  function reset() { viewport = undefined; lines.clear(); }
  return { buffer, reset };
}

export function optimizeTerminalRendering(terminal: Terminal): () => void {
  const renderer = terminal.renderer;
  if (!renderer) return () => {};
  const original = renderer.render;
  let source: GhosttyTerminal | undefined;
  let frame: ReturnType<typeof createViewportRenderBuffer> | undefined;
  renderer.render = function (buffer, ...args) {
    if (!(buffer instanceof GhosttyTerminal)) return original.call(this, buffer, ...args);
    if (source !== buffer) {
      source = buffer;
      frame = createViewportRenderBuffer(buffer);
    }
    frame!.reset();
    try {
      return original.call(this, frame!.buffer, ...args);
    } finally {
      frame!.reset();
    }
  };
  return () => { renderer.render = original; frame?.reset(); };
}
