package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

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
	ID             string `json:"id"`
	Name           string `json:"name"`
	KeyType        string `json:"keyType"`
	PublicKey      string `json:"publicKey"`
	Fingerprint    string `json:"fingerprint"`
	CreatedAt      int64  `json:"createdAt"`
	HasCertificate bool   `json:"hasCertificate"`
	// CertificateInfo is a short human summary (principals + validity) when a
	// certificate is attached, otherwise empty.
	CertificateInfo string `json:"certificateInfo,omitempty"`
}

func toView(k store.Key) PublicKeyView {
	v := PublicKeyView{
		ID: k.ID, Name: k.Name, KeyType: k.KeyType,
		PublicKey: k.PublicKey, Fingerprint: k.Fingerprint, CreatedAt: k.CreatedAt,
	}
	if k.Certificate != "" {
		v.HasCertificate = true
		v.CertificateInfo = certSummary(k.Certificate)
	}
	return v
}

// certSummary renders a one-line description of an OpenSSH certificate.
func certSummary(certText string) string {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(certText))
	if err != nil {
		return "invalid certificate"
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return "not a certificate"
	}
	principals := "any"
	if len(cert.ValidPrincipals) > 0 {
		principals = strings.Join(cert.ValidPrincipals, ", ")
	}
	if cert.ValidBefore == ssh.CertTimeInfinity {
		return fmt.Sprintf("principals: %s · no expiry", principals)
	}
	return fmt.Sprintf("principals: %s · valid until %s", principals,
		time.Unix(int64(cert.ValidBefore), 0).UTC().Format("2006-01-02"))
}

func (s *KeyService) List(ctx context.Context) ([]PublicKeyView, error) {
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

func (s *KeyService) Delete(ctx context.Context, id string) error { return s.keys.Delete(id) }

// AttachCertificate validates an OpenSSH certificate, verifies it was issued
// for this key (its embedded public key must match), and stores it. Passing an
// empty certText detaches any existing certificate.
func (s *KeyService) AttachCertificate(ctx context.Context, id, certText string) (PublicKeyView, error) {
	certText = strings.TrimSpace(certText)
	if certText == "" {
		if err := s.keys.SetCertificate(id, ""); err != nil {
			return PublicKeyView{}, err
		}
		k, err := s.keys.Get(id)
		if err != nil {
			return PublicKeyView{}, err
		}
		return toView(k), nil
	}

	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(certText))
	if err != nil {
		return PublicKeyView{}, fmt.Errorf("parse certificate: %w", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return PublicKeyView{}, errors.New("that is a public key, not a certificate")
	}

	k, err := s.keys.Get(id)
	if err != nil {
		return PublicKeyView{}, err
	}
	keyPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.PublicKey))
	if err != nil {
		return PublicKeyView{}, fmt.Errorf("parse stored public key: %w", err)
	}
	if !bytes.Equal(cert.Key.Marshal(), keyPub.Marshal()) {
		return PublicKeyView{}, errors.New("certificate was not issued for this key")
	}

	if err := s.keys.SetCertificate(id, certText); err != nil {
		return PublicKeyView{}, err
	}
	k.Certificate = certText
	return toView(k), nil
}

// Generate creates a new keypair, encrypts the private half with the unlocked
// vault master key, and stores both halves.
func (s *KeyService) Generate(ctx context.Context, name, keyType string) (PublicKeyView, error) {
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
func (s *KeyService) Import(ctx context.Context, name, privatePEM, passphrase string) (PublicKeyView, error) {
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
