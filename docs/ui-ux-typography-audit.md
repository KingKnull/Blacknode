# UI, UX, and typography review

Reviewed and updated on 2026-10-21.

The existing Svelte 5 workstation design keeps its obsidian surfaces, teal
selection markers, Inter interface text, and JetBrains Mono code text. Teal
markers remain the signature navigation treatment. Existing components were
reused; no external component inspiration was needed.

## Findings and changes

- Supporting text was commonly 9–11px. The shared scale now uses 18px display,
  16px titles, 13px body, 12px captions, 11px compact labels, and 10px dense
  metadata. Operational forms and tables use the shared scale.
- An unlayered control reset overrode Tailwind font and size utilities. Moving
  it into the base layer lets compact labels and monospace inputs use their
  intended typography.
- Settings inherited monospace throughout. Descriptions now use Inter;
  code and shortcut values retain their explicit monospace styling. Section
  headings are larger, and navigation uses sentence case.
- Settings squeezed descriptions into a narrow column and clipped actions at
  an 800px window width. Its navigation moves above content when the available
  panel width is at most 640px. Content and navigation scroll independently.
- Ops Console clipped its host selector and constrained its editor at narrow
  widths. Controls wrap, and its host list moves above the editor.
- Muted text and some status colors lost contrast on raised input surfaces.
  Reviewed supporting text and status tokens now exceed 4.5:1 against
  surface-3 in both themes.
- The New menu now exposes its expanded state, focuses its items, supports
  arrows/Home/End, and restores focus on Escape. Section tabs scroll to keep
  the active view visible; the active marker sits inside the tab.
- Page headers wrap actions and provide semantic headings. Empty states have
  stronger title hierarchy. Notifications use opaque surfaces, readable titles,
  wrapping text, and larger dismiss targets. The status bar no longer dims
  its entire Sync off control.
- The scrollback field now references its own help text rather than the
  auto-lock explanation.

## Files changed by this review

- `frontend/src/app.css`
- `frontend/src/lib/SettingsPanel.svelte`
- `frontend/src/lib/OpsConsole.svelte`
- `frontend/src/lib/SectionTabs.svelte`
- `frontend/src/lib/NavRail.svelte`
- `frontend/src/lib/Workspace.svelte` (rail width)
- `frontend/src/lib/PageHeader.svelte`
- `frontend/src/lib/EmptyState.svelte`
- `frontend/src/lib/Toaster.svelte`
- `frontend/src/lib/StatusBar.svelte`

Existing unrelated workspace changes were preserved.

## Validation

- `npm run check`: zero errors and warnings.
- `npm run build`: passed.
- `node --test src/lib/envColor.test.ts`: passed.
- `git diff --check`: passed.
- Browser audit: 35 views in dark/light themes at 1280×820 and 800×500,
  totaling 140 combinations. No uncaught browser exceptions, unlabeled visible
  form controls, or controls clipped outside intentional horizontal scrollers.
- Verified New menu focus, arrow navigation, End, Escape, and focus restoration;
  Settings section navigation; computed font sizes; and Inter in Settings.
- Supporting text/status contrast on surface-3: at least 4.70:1 in dark mode
  and 4.67:1 in light mode.
- Visually inspected terminal, Settings, and Ops Console screenshots. Audit
  screenshots and browser results are stored under `/tmp/blacknode-type-*`.

The browser audit used an isolated local server with temporary data. Remote
host operations, populated fleet scenarios, mobile widths below 800px, and
native desktop webview behavior were not exercised.
The existing audit-server binary predates some current bindings, and kubectl
is unavailable; affected panels were reviewed in their error/empty states.
