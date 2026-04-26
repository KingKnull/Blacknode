package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/blacknode/blacknode/internal/vault"
	"golang.org/x/crypto/ssh"
)

type KeyService struct {
	keys  *store.Keys
	vault *vault.Vault
}

func NewKeyService(k *store.Keys, v *vault.Vault) *KeyService {
	return &KeyService{keys: k, vault: v}
}

// PublicKeyView is the safe shape returned to the frontend — never the
// private material.
type PublicKeyView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	KeyType     string `json:"keyType"`
	PublicKey   string `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   int64  `json:"createdAt"`
}

func toView(k store.Key) PublicKeyView {
	return PublicKeyView{
		ID: k.ID, Name: k.Name, KeyType: k.KeyType,
		PublicKey: k.PublicKey, Fingerprint: k.Fingerprint, CreatedAt: k.CreatedAt,
	}
}

func (s *KeyService) List() ([]PublicKeyView, error) {
	rows, err := s.keys.List()
	if err != nil {
		return nil, err
	}
	out := make([]PublicKeyView, 0, len(rows))
	for _, k := range rows {
		out = append(out, toView(k))
	}
	return out, nil
}

func (s *KeyService) Delete(id string) error { return s.keys.Delete(id) }

// Generate creates a new keypair, encrypts the private half with the unlocked
// vault master key, and stores both halves.
func (s *KeyService) Generate(name, keyType string) (PublicKeyView, error) {
	if !s.vault.IsUnlocked() {
		return PublicKeyView{}, errors.New("vault is locked")
	}
	if name == "" {
		return PublicKeyView{}, errors.New("name required")
	}
	if keyType == "" {
		keyType = "ed25519"
	}

	var (
		privPEM []byte
		pub     ssh.PublicKey
	)
	switch keyType {
	case "ed25519":
		pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return PublicKeyView{}, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(privKey)
		if err != nil {
			return PublicKeyView{}, err
		}
		privPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		pub, err = ssh.NewPublicKey(pubKey)
		if err != nil {
			return PublicKeyView{}, err
		}
	case "rsa":
		privKey, err := rsa.GenerateKey(rand.Reader, 4096)
		if err != nil {
			return PublicKeyView{}, err
		}
		der := x509.MarshalPKCS1PrivateKey(privKey)
		privPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
		pub, err = ssh.NewPublicKey(&privKey.PublicKey)
		if err != nil {
			return PublicKeyView{}, err
		}
	default:
		return PublicKeyView{}, fmt.Errorf("unsupported key type: %s", keyType)
	}

	return s.persist(name, keyType, privPEM, pub)
}

// Import takes user-supplied PEM private key bytes (optionally passphrase-
// protected), validates them, derives the public half, and stores everything.
func (s *KeyService) Import(name, privatePEM, passphrase string) (PublicKeyView, error) {
	if !s.vault.IsUnlocked() {
		return PublicKeyView{}, errors.New("vault is locked")
	}
	var (
		signer ssh.Signer
		err    error
	)
	if passphrase == "" {
		signer, err = ssh.ParsePrivateKey([]byte(privatePEM))
	} else {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(privatePEM), []byte(passphrase))
	}
	if err != nil {
		return PublicKeyView{}, fmt.Errorf("parse key: %w", err)
	}
	keyType := signer.PublicKey().Type()
	return s.persist(name, keyType, []byte(privatePEM), signer.PublicKey())
}

func (s *KeyService) persist(name, keyType string, privPEM []byte, pub ssh.PublicKey) (PublicKeyView, error) {
	ct, nonce, err := s.vault.Encrypt(privPEM)
	if err != nil {
		return PublicKeyView{}, fmt.Errorf("vault encrypt: %w", err)
	}
	authorizedLine := string(ssh.MarshalAuthorizedKey(pub))
	saved, err := s.keys.Create(store.Key{
		Name:                name,
		KeyType:             keyType,
		PublicKey:           authorizedLine,
		EncryptedPrivateKey: ct,
		Nonce:               nonce,
		Fingerprint:         store.Fingerprint(pub),
	})
	if err != nil {
		return PublicKeyView{}, err
	}
	return toView(saved), nil
}
