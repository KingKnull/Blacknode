package main

import (
	"errors"
	"strconv"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
)

const (
	SettingAnthropicAPIKey   = "anthropic_api_key"
	SettingTheme             = "theme"
	SettingAutoLockMinutes   = "auto_lock_minutes"
	SettingDefaultShellPath  = "default_shell_path"
	SettingMetricsIntervalS  = "metrics_interval_seconds"
)

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
	HasAnthropicKey    bool   `json:"hasAnthropicKey"`
}

func (s *SettingsService) Get() (AppSettings, error) {
	out := AppSettings{
		Theme:              "dark",
		AutoLockMinutes:    15,
		DefaultShellPath:   "",
		MetricsIntervalSec: 5,
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
	has, err := s.settings.HasSecret(SettingAnthropicAPIKey)
	if err != nil {
		return out, err
	}
	out.HasAnthropicKey = has
	return out, nil
}

func (s *SettingsService) SetTheme(theme string) error {
	return s.settings.SetPlain(SettingTheme, theme)
}

func (s *SettingsService) SetAutoLockMinutes(minutes int) error {
	if minutes < 0 {
		return errors.New("minutes must be >= 0")
	}
	return s.settings.SetPlain(SettingAutoLockMinutes, strconv.Itoa(minutes))
}

func (s *SettingsService) SetDefaultShellPath(path string) error {
	return s.settings.SetPlain(SettingDefaultShellPath, path)
}

func (s *SettingsService) SetMetricsInterval(seconds int) error {
	if seconds < 2 {
		return errors.New("interval must be >= 2 seconds")
	}
	return s.settings.SetPlain(SettingMetricsIntervalS, strconv.Itoa(seconds))
}

// SetAnthropicAPIKey seals the key with the vault and stores it. Empty key
// clears the setting.
func (s *SettingsService) SetAnthropicAPIKey(key string) error {
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
func (s *SettingsService) AnthropicAPIKey() (string, error) {
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
