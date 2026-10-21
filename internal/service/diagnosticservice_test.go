package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticBundleAllowlist(t *testing.T) {
	var packages []string
	text, err := collectDiagnosticBundle(context.Background(), "linux", "amd64", "gc", "go1.26.0", func(ctx context.Context, pkg string) (string, string) {
		packages = append(packages, pkg)
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > diagnosticProbeTimeout {
			t.Error("package probe is not time bounded")
		}
		return "4.18.2\n", "available"
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(text) > diagnosticMaxBytes {
		t.Fatal("bundle exceeds limit")
	}
	if !reflect.DeepEqual(packages, []string{"gtk4", "webkitgtk-6.0", "gtk+-3.0", "webkit2gtk-4.1"}) {
		t.Fatalf("unexpected package probes: %v", packages)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("unexpected top-level fields: %v", got)
	}
	for _, key := range []string{"schemaVersion", "appVersion", "build", "libraries", "librarySource"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing field %s", key)
		}
	}
	var bundle diagnosticBundle
	if err := json.Unmarshal([]byte(text), &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.AppVersion != AppVersion || bundle.Build.GoVersion != "go1.26.0" {
		t.Fatal("missing build metadata")
	}
	for _, lib := range bundle.Libraries {
		if lib.Status != "available" || lib.Version != "4.18.2" || lib.Error != "" {
			t.Fatalf("bad library result: %+v", lib)
		}
	}
}

func TestDiagnosticSecretLookingOutputNeverAppears(t *testing.T) {
	// Synthetic environment/file-path fixtures only: never open a private file,
	// inspect the real environment, or run a package or network command.
	for _, secret := range []string{
		"TOKEN=sk-secret-123", "-----BEGIN PRIVATE KEY-----", "/home/private/.ssh/id_ed25519",
		`C:\Users\private\secret.key`, "private-host.internal", "PKG_CONFIG_PATH=/private/config",
		"4.18.2\nTOKEN=secret", strings.Repeat("9", 10000),
	} {
		for _, status := range []string{"available", "query_failed", secret} {
			text, err := collectDiagnosticBundle(context.Background(), "linux", "amd64", "gc", "devel "+secret, func(context.Context, string) (string, string) {
				return secret, status
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(text, secret) {
				t.Fatal("sensitive fixture appeared in bundle")
			}
			if strings.Contains(text, "devel ") {
				t.Fatal("raw development build string appeared")
			}
			if len(text) > diagnosticMaxBytes {
				t.Fatal("bundle exceeds limit")
			}
			var bundle diagnosticBundle
			if err := json.Unmarshal([]byte(text), &bundle); err != nil {
				t.Fatal(err)
			}
			if bundle.Build.Error == "" {
				t.Fatal("unsupported Go version has no explicit error")
			}
			for _, lib := range bundle.Libraries {
				if lib.Version != "" || lib.Error == "" {
					t.Fatal("unsafe output was not omitted with an explicit error")
				}
			}
		}
	}
}

func TestDiagnosticFailureStates(t *testing.T) {
	for _, status := range []string{"timeout", "cancelled", "unavailable", "output_limit", "query_failed"} {
		text, err := collectDiagnosticBundle(context.Background(), "linux", "arm64", "gc", "go1.26rc1", func(context.Context, string) (string, string) {
			return "stderr with /private/token", status
		})
		if err != nil {
			t.Fatal(err)
		}
		var bundle diagnosticBundle
		if err := json.Unmarshal([]byte(text), &bundle); err != nil {
			t.Fatal(err)
		}
		for _, lib := range bundle.Libraries {
			if lib.Status != status || lib.Error == "" || lib.Version != "" {
				t.Fatalf("missing explicit failure: %+v", lib)
			}
		}
		if strings.Contains(text, "/private/token") {
			t.Fatal("raw failure output leaked")
		}
	}
}

func TestDiagnosticNonLinuxNeverRunsCommand(t *testing.T) {
	text, err := collectDiagnosticBundle(context.Background(), "darwin", "arm64", "gc", "go1.26.0", func(context.Context, string) (string, string) {
		t.Fatal("package probe must not run on non-Linux")
		return "", ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(text, `"status": "not_applicable"`) != 4 {
		t.Fatal("non-Linux state missing")
	}
}

func TestDiagnosticCommandDoesNotInheritEnvironmentOrWorkingDirectory(t *testing.T) {
	cmd := diagnosticPackageCommand(context.Background(), "gtk4")
	if !reflect.DeepEqual(cmd.Args, []string{"/usr/bin/pkg-config", "--modversion", "gtk4"}) {
		t.Fatalf("unexpected command: %v", cmd.Args)
	}
	if !reflect.DeepEqual(cmd.Env, []string{"LC_ALL=C", "LANG=C"}) {
		t.Fatal("command must use only the literal allowlisted environment")
	}
	if cmd.Dir != "/" || cmd.Stderr != io.Discard || cmd.WaitDelay != 100*time.Millisecond {
		t.Fatal("command isolation or bounds missing")
	}
	// In particular, even if these synthetic values existed in the parent,
	// none can be inherited or included in the export.
	for _, forbidden := range []string{"TOKEN=sk-secret", "HOME=/private/user", "PKG_CONFIG_PATH=/private/file", "PATH=/private/bin"} {
		if strings.Contains(strings.Join(cmd.Env, "\n"), forbidden) {
			t.Fatal("private environment value inherited")
		}
	}
}

func TestDiagnosticOutputBufferIsBounded(t *testing.T) {
	var output diagnosticOutput
	if n, err := output.Write([]byte(strings.Repeat("x", 100000))); n != diagnosticVersionMaxBytes || !errors.Is(err, io.ErrShortBuffer) {
		t.Fatal("output was not bounded")
	}
	if len(output.data) != diagnosticVersionMaxBytes || !output.overflow {
		t.Fatal("buffer exceeded limit or overflow missing")
	}
	if n, err := output.Write([]byte("more")); n != 0 || !errors.Is(err, io.ErrShortBuffer) {
		t.Fatal("full buffer accepted more data")
	}
}

func TestDiagnosticBundleTotalSizeLimit(t *testing.T) {
	text, err := collectDiagnosticBundle(context.Background(), "darwin", strings.Repeat("a", diagnosticMaxBytes), "gc", "go1.26.0", nil)
	if err == nil || text != "" || err.Error() != "diagnostic bundle exceeds the 8192-byte limit" {
		t.Fatal("oversize bundle was not rejected")
	}
}

func TestDiagnosticCancelledProbeDoesNotStartProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	version, status := probeDiagnosticPackage(ctx, "gtk4")
	if version != "" || status != "cancelled" {
		t.Fatal("cancelled probe returned unexpected state")
	}
}
