package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
)

const (
	SettingTheme              = "theme"
	SettingAutoLockMinutes    = "auto_lock_minutes"
	SettingDefaultShellPath   = "default_shell_path"
	SettingMetricsIntervalS   = "metrics_interval_seconds"
	SettingTerminalScrollback = "terminal_scrollback"
)

// Scrollback bounds prevent an unbounded terminal buffer from exhausting
// memory. The limit is applied per pane, so large values across many panes can
// consume substantial memory. Users who need durable history should use
// session recordings, which spool to disk instead of RAM.
//
// The minimum is above zero so that a mistyped value cannot leave a terminal
// with no scrollback at all, which reads as a broken app rather than a setting.
const (
	ScrollbackMin     = 100
	ScrollbackMax     = 50_000
	ScrollbackDefault = 5_000
)

// clampScrollback brings a value into range rather than discarding it.
//
// Reads and writes deliberately differ: SetTerminalScrollback rejects an
// out-of-range value, because the user typed it and should be told why it did
// not take. A read has nobody to tell — the value may have been written by a
// different version of the app or edited in the database by hand — so it
// clamps, since honouring the intent approximately beats silently resetting to
// the default and losing it.
func clampScrollback(n int) int {
	if n < ScrollbackMin {
		return ScrollbackMin
	}
	if n > ScrollbackMax {
		return ScrollbackMax
	}
	return n
}

type SettingsService struct {
	settings *store.Settings
	vault    *vault.Vault
}

func NewSettingsService(s *store.Settings, v *vault.Vault) *SettingsService {
	return &SettingsService{settings: s, vault: v}
}

// AppSettings is the safe shape returned to the frontend — never includes
// raw secrets, just whether each one is set.
type AppSettings struct {
	Theme               string `json:"theme"`
	AutoLockMinutes     int    `json:"autoLockMinutes"`
	DefaultShellPath    string `json:"defaultShellPath"`
	MetricsIntervalSec  int    `json:"metricsIntervalSeconds"`
	TerminalScrollback  int    `json:"terminalScrollback"`
	TerminalSessionMode string `json:"terminalSessionMode"`
	ShellIntegration    bool   `json:"shellIntegration"`
}

func (s *SettingsService) Get(ctx context.Context) (AppSettings, error) {
	out := AppSettings{
		Theme:               "dark",
		AutoLockMinutes:     15,
		DefaultShellPath:    "",
		MetricsIntervalSec:  5,
		TerminalScrollback:  ScrollbackDefault,
		TerminalSessionMode: "auto",
		ShellIntegration:    true,
	}
	if v, err := s.settings.GetPlain(SettingTheme); err == nil && v != "" {
		out.Theme = v
	}
	if v, err := s.settings.GetPlain(SettingAutoLockMinutes); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			out.AutoLockMinutes = n
		}
	}
	if v, err := s.settings.GetPlain(SettingDefaultShellPath); err == nil {
		out.DefaultShellPath = v
	}
	if v, err := s.settings.GetPlain(SettingMetricsIntervalS); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 2 {
			out.MetricsIntervalSec = n
		}
	}
	if v, err := s.settings.GetPlain(SettingTerminalScrollback); err == nil && v != "" {
		// Unparseable falls through to the default; a number is clamped. The
		// difference matters: "" or "abc" carries no intent to honour, whereas
		// 999999 does.
		if n, err := strconv.Atoi(v); err == nil {
			out.TerminalScrollback = clampScrollback(n)
		}
	}
	if mode, err := s.settings.GetPlain("terminal_session_mode"); err == nil && mode != "" {
		out.TerminalSessionMode = mode
	}
	if enabled, err := s.settings.GetPlain("shell_integration"); err == nil {
		out.ShellIntegration = enabled != "false"
	}
	return out, nil
}

func (s *SettingsService) SetTerminalFeatures(ctx context.Context, mode string, integration bool) error {
	if mode != "auto" && mode != "off" && mode != "tmux" && mode != "zellij" {
		return errors.New("invalid session manager")
	}
	if err := s.settings.SetPlain("terminal_session_mode", mode); err != nil {
		return err
	}
	return s.settings.SetPlain("shell_integration", strconv.FormatBool(integration))
}

func (s *SettingsService) SetTheme(ctx context.Context, theme string) error {
	return s.settings.SetPlain(SettingTheme, theme)
}

func (s *SettingsService) SetAutoLockMinutes(ctx context.Context, minutes int) error {
	if minutes < 0 {
		return errors.New("minutes must be >= 0")
	}
	return s.settings.SetPlain(SettingAutoLockMinutes, strconv.Itoa(minutes))
}

func (s *SettingsService) SetDefaultShellPath(ctx context.Context, path string) error {
	return s.settings.SetPlain(SettingDefaultShellPath, path)
}

func (s *SettingsService) SetMetricsInterval(ctx context.Context, seconds int) error {
	if seconds < 2 {
		return errors.New("interval must be >= 2 seconds")
	}
	return s.settings.SetPlain(SettingMetricsIntervalS, strconv.Itoa(seconds))
}

// SetTerminalScrollback stores the per-pane scrollback line limit.
//
// Rejecting instead of clamping: the number came from a person, and a silent
// clamp would show them a saved value they did not choose with no explanation.
// The error names both bounds so the message is actionable on its own.
func (s *SettingsService) SetTerminalScrollback(ctx context.Context, lines int) error {
	if lines < ScrollbackMin || lines > ScrollbackMax {
		return fmt.Errorf("scrollback must be between %d and %d lines", ScrollbackMin, ScrollbackMax)
	}
	return s.settings.SetPlain(SettingTerminalScrollback, strconv.Itoa(lines))
}
