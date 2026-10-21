package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	diagnosticMaxBytes        = 8192
	diagnosticVersionMaxBytes = 64
	diagnosticProbeTimeout    = 500 * time.Millisecond
)

var (
	diagnosticPackageVersion = regexp.MustCompile(`^[0-9]{1,4}(\.[0-9]{1,4}){1,3}$`)
	diagnosticGoVersion      = regexp.MustCompile(`^go[0-9]{1,3}\.[0-9]{1,3}(\.[0-9]{1,3})?((rc|beta)[0-9]{1,2})?$`)
)

// DiagnosticService builds a local, allowlisted metadata report. It has no
// stores, connections, logger, filesystem reader, or environment reader.
// Redaction is by exclusion, never by trying to scrub logs or configuration.
type DiagnosticService struct{}

func NewDiagnosticService() *DiagnosticService { return &DiagnosticService{} }

type diagnosticBundle struct {
	SchemaVersion int                 `json:"schemaVersion"`
	AppVersion    string              `json:"appVersion"`
	Build         diagnosticBuild     `json:"build"`
	Libraries     []diagnosticLibrary `json:"libraries"`
	LibrarySource string              `json:"librarySource"`
}

type diagnosticBuild struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Compiler  string `json:"compiler"`
	GoVersion string `json:"goVersion"`
	Error     string `json:"error,omitempty"`
}

type diagnosticLibrary struct {
	Package string `json:"package"`
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

type diagnosticProbe func(context.Context, string) (string, string)

// Preview returns the complete JSON download contents, capped at 8 KiB. It
// neither saves a file nor contacts a service. Only fixed Linux package version
// queries run; no shell, install, repair, or user-supplied command is supported.
func (s *DiagnosticService) Preview(ctx context.Context) (string, error) {
	return collectDiagnosticBundle(ctx, runtime.GOOS, runtime.GOARCH, runtime.Compiler, runtime.Version(), probeDiagnosticPackage)
}

func collectDiagnosticBundle(ctx context.Context, goos, arch, compiler, goVersion string, probe diagnosticProbe) (string, error) {
	b := diagnosticBundle{
		SchemaVersion: 1,
		AppVersion:    AppVersion,
		Build:         diagnosticBuild{OS: goos, Arch: arch, Compiler: compiler},
		Libraries:     []diagnosticLibrary{},
		LibrarySource: "Local pkg-config development metadata; not a loaded-runtime check",
	}
	// Development toolchain strings can contain paths. Never return them raw.
	if len(goVersion) <= diagnosticVersionMaxBytes && diagnosticGoVersion.MatchString(goVersion) {
		b.Build.GoVersion = goVersion
	} else {
		b.Build.Error = "Go runtime version is not a supported release identifier"
	}
	ctx, cancel := context.WithTimeout(ctx, 4*diagnosticProbeTimeout)
	defer cancel()
	for _, pkg := range []string{"gtk4", "webkitgtk-6.0", "gtk+-3.0", "webkit2gtk-4.1"} {
		lib := diagnosticLibrary{Package: pkg, Status: "not_applicable"}
		if goos == "linux" {
			probeCtx, stop := context.WithTimeout(ctx, diagnosticProbeTimeout)
			version, status := probe(probeCtx, pkg)
			stop()
			lib.Status = status
			switch status {
			case "available":
				version = strings.TrimSpace(version)
				if len(version) <= diagnosticVersionMaxBytes && diagnosticPackageVersion.MatchString(version) {
					lib.Version = version
				} else {
					lib.Status = "invalid_version"
					lib.Error = "pkg-config returned an unsupported version identifier; output omitted"
				}
			case "timeout":
				lib.Error = "pkg-config query timed out"
			case "cancelled":
				lib.Error = "pkg-config query cancelled"
			case "unavailable":
				lib.Error = "System pkg-config executable is unavailable"
			case "output_limit":
				lib.Error = "pkg-config output exceeded 64 bytes; output omitted"
			default:
				// Never serialize raw errors, stderr, or an unknown runner status.
				lib.Status = "query_failed"
				lib.Error = "pkg-config query failed; package metadata may be unavailable"
			}
		} else {
			lib.Error = "Local pkg-config checks are Linux-only"
		}
		b.Libraries = append(b.Libraries, lib)
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", errors.New("diagnostic JSON encoding failed")
	}
	data = append(data, '\n')
	if len(data) > diagnosticMaxBytes {
		return "", errors.New("diagnostic bundle exceeds the 8192-byte limit")
	}
	return string(data), nil
}

func diagnosticPackageCommand(ctx context.Context, pkg string) *exec.Cmd {
	// A fixed system executable avoids PATH lookup. The child receives no
	// inherited environment, including HOME, tokens, proxies or PKG_CONFIG_PATH.
	cmd := exec.CommandContext(ctx, "/usr/bin/pkg-config", "--modversion", pkg)
	cmd.Env = []string{"LC_ALL=C", "LANG=C"}
	cmd.Dir = "/"
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd
}

func probeDiagnosticPackage(ctx context.Context, pkg string) (string, string) {
	cmd := diagnosticPackageCommand(ctx, pkg)
	output := &diagnosticOutput{}
	cmd.Stdout = output
	err := cmd.Run()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "", "timeout"
	case ctx.Err() != nil:
		return "", "cancelled"
	case output.overflow:
		return "", "output_limit"
	case errors.Is(err, fs.ErrNotExist):
		return "", "unavailable"
	case err != nil:
		return "", "query_failed"
	default:
		return string(output.data), "available"
	}
}

// Bound the stdout buffer while the process is running, not after reading it.
type diagnosticOutput struct {
	data     []byte
	overflow bool
}

func (w *diagnosticOutput) Write(p []byte) (int, error) {
	remaining := diagnosticVersionMaxBytes - len(w.data)
	if len(p) > remaining {
		w.data = append(w.data, p[:remaining]...)
		w.overflow = true
		return remaining, io.ErrShortBuffer
	}
	w.data = append(w.data, p...)
	return len(p), nil
}
