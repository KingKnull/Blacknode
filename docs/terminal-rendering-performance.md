# Terminal rendering performance

The live terminal uses a rendering adapter for Ghostty Web 0.4. The stock
renderer calls `getLine()` for each row it paints. Each call fetches and parses
the entire visible grid, then clones that row's cells. Cursor redraws repeat
this work too.

`frontend/src/lib/terminalRendering.ts` provides one viewport snapshot per
render call. Rows reuse that snapshot without cloning individual cells. The
snapshot is discarded after every render, so output, resize, and alternate
screen changes remain visible on the next frame. Search and link detection
continue to use the engine's original buffer methods. The wrapper is restored
when the terminal component is destroyed.

In a local Chrome audit at 1280×820, a full redraw read the viewport 43 times
before and once after. Twenty full redraws averaged 13.89ms before and 10.48ms
after. Individual typing measurements varied across runs; the change does not
eliminate the shell/SSH echo round trip.

Verified with the production build, app and test type checks, input queue and
render adapter tests, and browser checks for actual shell input, Unicode,
ANSI colors, alternate-screen transitions, and resizing. Measurements used a
fresh server build with isolated temporary data. Native desktop WebKit and
remote SSH latency were not measured.

## Engine decision

The production terminal remains on Ghostty Web 0.4.0. A temporary, offline
synthetic harness was used to compare Ghostty and xterm-style workloads without
starting Wails, a shell, SSH, or any saved infrastructure connection. The
current Ghostty adapter measured approximately 35 ms median for a plain-output
case, 57 ms for ANSI-heavy output, and 318 ms for a resize-stress case with
100 alternating resizes. These are harness measurements, not desktop-WebKit
measurements, and the first cross-engine run was not semantically comparable
enough to justify a production engine migration. Keep xterm as a future
benchmark candidate rather than adding it to the application until a matched
Wails-WebKit benchmark demonstrates a sustained advantage without Unicode,
alternate-screen, resize, recording, or bundle-size regressions.
