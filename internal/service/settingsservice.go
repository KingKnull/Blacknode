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
	SettingAnthropicAPIKey    = "anthropic_api_key"
	SettingTheme              = "theme"
	SettingAutoLockMinutes    = "auto_lock_minutes"
	SettingDefaultShellPath   = "default_shell_path"
	SettingMetricsIntervalS   = "metrics_interval_seconds"
	SettingTerminalScrollback = "terminal_scrollback"
)

// Scrollback bounds. xterm.js keeps every retained line as a typed array of
// roughly 12 bytes per cell, so the cost is lines × columns and it is paid per
// pane, not per app: at 120 columns each 1,000 lines is about 1.5 MB, and a
// workspace with eight panes multiplies whatever is chosen here by eight.
//
// The maximum is therefore a real limit rather than a formality — 50,000 lines
// across eight wide panes is already most of a gigabyte, and "unlimited" is
// deliberately not offered, because the failure mode is the whole app being
// killed rather than a truncated buffer. Users who need durable history should
// reach for session recordings, which spool to disk instead of RAM.
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
	Theme              string `json:"theme"`
	AutoLockMinutes    int    `json:"autoLockMinutes"`
	DefaultShellPath   string `json:"defaultShellPath"`
	MetricsIntervalSec int    `json:"metricsIntervalSeconds"`
	TerminalScrollback int    `json:"terminalScrollback"`
	HasAnthropicKey    bool   `json:"hasAnthropicKey"`
}

func (s *SettingsService) Get(ctx context.Context) (AppSettings, error) {
	out := AppSettings{
		Theme:              "dark",
		AutoLockMinutes:    15,
		DefaultShellPath:   "",
		MetricsIntervalSec: 5,
		TerminalScrollback: ScrollbackDefault,
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
	has, err := s.settings.HasSecret(SettingAnthropicAPIKey)
	if err != nil {
		return out, err
	}
	out.HasAnthropicKey = has
	return out, nil
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

// SetAnthropicAPIKey seals the key with the vault and stores it. Empty key
// clears the setting.
func (s *SettingsService) SetAnthropicAPIKey(ctx context.Context, key string) error {
	if key == "" {
		return s.settings.Delete(SettingAnthropicAPIKey)
	}
	if !s.vault.IsUnlocked() {
		return errors.New("vault must be unlocked to save the API key")
	}
	cipher, nonce, err := s.vault.Encrypt([]byte(key))
	if err != nil {
		return err
	}
	return s.settings.SetSecret(SettingAnthropicAPIKey, cipher, nonce)
}

// AnthropicAPIKey returns the plaintext key for use by the AI service. Lives
// in main package only; never leaves Go.
func (s *SettingsService) AnthropicAPIKey(ctx context.Context) (string, error) {
	cipher, nonce, err := s.settings.GetSecret(SettingAnthropicAPIKey)
	if err != nil {
		return "", err
	}
	if len(cipher) == 0 {
		return "", nil
	}
	if !s.vault.IsUnlocked() {
		return "", errors.New("vault is locked")
	}
	plain, err := s.vault.Decrypt(cipher, nonce)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
