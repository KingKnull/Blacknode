# Local diagnostic bundle

Open **Settings → Diagnostics → Preview diagnostic bundle**. Review the complete
JSON, check **I reviewed this JSON and want to save it locally**, then choose
**Confirm & download JSON**. Cancel discards the preview without downloading.
The download is named `blacknode-diagnostics.json`; the browser/desktop controls
its destination. The app does not claim that a requested download was saved.

## Included metadata

- Schema version and the app's compiled-in release version.
- Compile-target OS, architecture, compiler, and a release-shaped Go runtime version.
- On Linux only, local `pkg-config --modversion` results for `gtk4`,
  `webkitgtk-6.0`, `gtk+-3.0`, and `webkit2gtk-4.1`.

Library entries describe installed development metadata, **not** which library is
loaded by the running desktop. Other platforms explicitly report `not_applicable`.
Unavailable tooling, failed queries, cancellation, timeouts, unsupported version
strings, and oversized command output are explicit statuses with fixed messages.

## Collection boundary

`DiagnosticService.Preview` has no store, SSH pool, logger, configuration reader,
environment reader, or network client. Redaction uses an allowlist: logs,
configuration, credentials, host names, private paths, raw stderr, and raw process
errors are never included. It does not install or repair anything. No inventory
counts are gathered because they would require access to application data.

Linux probes use a fixed `/usr/bin/pkg-config` executable, fixed package names,
no shell, a fixed `/` working directory, and only literal `LANG=C` / `LC_ALL=C`
environment entries. Each has a 500 ms deadline; the collection has a shared
2-second deadline and process pipe shutdown is bounded. Stdout is capped at
64 bytes while reading, stderr is discarded, and only numeric dotted versions
are accepted. Development Go version strings are omitted with an explicit error
because they can contain local paths. Build settings, module paths, and linker
flags are not inspected.

The complete JSON is capped at **8192 UTF-8 bytes**, checked again before preview
and download. Download uses the exact reviewed text without collecting again.
The backend never saves the bundle or chooses a filesystem destination.

## Local verification

The service tests use synthetic version/error/path fixtures and injected probes;
they do not read real environment values or private files, run package probes, or
contact infrastructure. Frontend tests cover confirmation gating, exact download
contents, UTF-8 size limits, invalid JSON, and surfaced download failures.

```sh
GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test -tags server ./internal/service
```

From `frontend/`:

```sh
node --test src/lib/diagnosticDownload.test.ts
npm run check
npm run build
```

A native desktop preview/download click-through and actual Linux library
availability remain manual checks. These automated checks do not certify a native
GTK build or package runtime.

In this container, untagged `go vet ./...` and `go test ./...` fail while compiling
the existing Wails Linux C bindings: GTK declarations including `GtkFileDialog`,
`gdk_monitor_get_scale`, and `gtk_css_provider_load_from_string` are unavailable.
The same failure reproduces with only the dependency selected:

```sh
GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test github.com/wailsapp/wails/v3/pkg/application -run '^$'
```

This isolates the native dependency failure from the diagnostic feature. No GTK
build configuration changes or dependency installation were made for this work.
