package main

import (
	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
)

type VaultService struct {
	vault    *vault.Vault
	activity *activityRecorder
}

func NewVaultService(v *vault.Vault, activity *activityRecorder) *VaultService {
	return &VaultService{vault: v, activity: activity}
}

type VaultStatus struct {
	Initialized bool `json:"initialized"`
	Unlocked    bool `json:"unlocked"`
}

func (s *VaultService) Status() (VaultStatus, error) {
	init, err := s.vault.IsInitialized()
	if err != nil {
		return VaultStatus{}, err
	}
	return VaultStatus{Initialized: init, Unlocked: s.vault.IsUnlocked()}, nil
}

func (s *VaultService) Setup(passphrase string) error {
	if err := s.vault.Setup(passphrase); err != nil {
		return err
	}
	s.activity.Record(store.Activity{
		Source: "vault",
		Kind:   "vault.setup",
		Title:  "Vault initialized",
	})
	return nil
}

func (s *VaultService) Unlock(passphrase string) error {
	if err := s.vault.Unlock(passphrase); err != nil {
		s.activity.Record(store.Activity{
			Source: "vault",
			Kind:   "vault.unlock.failed",
			Level:  "warn",
			Title:  "Vault unlock failed",
			Body:   err.Error(),
		})
		return err
	}
	s.activity.Record(store.Activity{
		Source: "vault",
		Kind:   "vault.unlock",
		Title:  "Vault unlocked",
	})
	return nil
}

func (s *VaultService) Lock() error {
	s.vault.Lock()
	s.activity.Record(store.Activity{
		Source: "vault",
		Kind:   "vault.lock",
		Title:  "Vault locked",
	})
	return nil
}
