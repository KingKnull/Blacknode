package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfiguredShellUsesSavedExecutable(t *testing.T) {
	dir := t.TempDir()
	shellPath := filepath.Join(dir, "saved-shell")
	if err := os.WriteFile(shellPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, settings := newSettingsService(t)
	if err := settings.SetPlain(SettingDefaultShellPath, shellPath); err != nil {
		t.Fatal(err)
	}
	got, err := configuredShell(settings)
	if err != nil {
		t.Fatalf("configuredShell() error = %v", err)
	}
	if got != shellPath {
		t.Fatalf("configuredShell() = %q, want %q", got, shellPath)
	}
}

func TestConfiguredShellRejectsInvalidSavedPath(t *testing.T) {
	_, settings := newSettingsService(t)
	if err := settings.SetPlain(SettingDefaultShellPath, filepath.Join(t.TempDir(), "missing-shell")); err != nil {
		t.Fatal(err)
	}
	if _, err := configuredShell(settings); err == nil {
		t.Fatal("configuredShell() accepted a missing executable")
	} else if !strings.Contains(err.Error(), "configured shell") || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("configuredShell() error = %q, want an actionable invalid-path error", err)
	}
}

func TestConfiguredShellRejectsNonExecutableSavedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use Unix executable mode bits")
	}
	dir := t.TempDir()
	shellPath := filepath.Join(dir, "not-executable")
	if err := os.WriteFile(shellPath, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, settings := newSettingsService(t)
	if err := settings.SetPlain(SettingDefaultShellPath, shellPath); err != nil {
		t.Fatal(err)
	}
	if _, err := configuredShell(settings); err == nil {
		t.Fatal("configuredShell() accepted a non-executable file")
	} else if !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("configuredShell() error = %q, want a not-executable error", err)
	}
}
