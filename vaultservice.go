package main

import "github.com/blacknode/blacknode/internal/vault"

type VaultService struct {
	vault *vault.Vault
}

func NewVaultService(v *vault.Vault) *VaultService {
	return &VaultService{vault: v}
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
	return s.vault.Setup(passphrase)
}

func (s *VaultService) Unlock(passphrase string) error {
	return s.vault.Unlock(passphrase)
}

func (s *VaultService) Lock() error {
	s.vault.Lock()
	return nil
}
