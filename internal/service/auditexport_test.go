package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestSignSSHSIGVerifiesWithOpenSSH is the test that actually matters for the
// audit export. Hand-rolling a wire format is only worth doing if the real tool
// accepts the result, so this shells out to ssh-keygen -Y verify rather than
// checking the bytes against my own reimplementation of the same spec — which
// would agree with a mistake just as happily as with a correct encoding.
func TestSignSSHSIGVerifiesWithOpenSSH(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not available")
	}

	for _, tc := range []struct {
		name  string
		build func(t *testing.T) ssh.Signer
	}{
		{"ed25519", func(t *testing.T) ssh.Signer {
			_, priv, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			s, err := ssh.NewSignerFromKey(priv)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}},
		// RSA is included specifically to cover the SHA-1 trap: ssh.Signer's
		// default RSA algorithm is ssh-rsa, which modern ssh-keygen refuses to
		// verify. If signWithBestAlgorithm regresses, this case fails.
		{"rsa", func(t *testing.T) ssh.Signer {
			priv, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatal(err)
			}
			s, err := ssh.NewSignerFromKey(priv)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signer := tc.build(t)
			dir := t.TempDir()
			msg := []byte(`{"format":"blacknode-audit-v1","entries":[]}` + "\n")

			docPath := filepath.Join(dir, "doc.json")
			if err := os.WriteFile(docPath, msg, 0o600); err != nil {
				t.Fatal(err)
			}

			sig, err := signSSHSIG(signer, msg)
			if err != nil {
				t.Fatalf("sign: %v", err)
			}
			sigPath := docPath + ".sig"
			if err := os.WriteFile(sigPath, sig, 0o600); err != nil {
				t.Fatal(err)
			}

			line, err := allowedSignersLine("auditor@example.com", signer.PublicKey())
			if err != nil {
				t.Fatalf("allowed_signers: %v", err)
			}
			signersPath := filepath.Join(dir, "allowed_signers")
			if err := os.WriteFile(signersPath, []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}

			doc, err := os.Open(docPath)
			if err != nil {
				t.Fatal(err)
			}
			defer doc.Close()

			cmd := exec.Command(keygen, "-Y", "verify",
				"-f", signersPath, "-I", "auditor@example.com",
				"-n", sshsigNamespace, "-s", sigPath)
			cmd.Stdin = doc
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("ssh-keygen -Y verify rejected the signature: %v\n%s", err, out)
			}
		})
	}
}

// TestSignSSHSIGRejectsModifiedDocument confirms the signature is over the
// document and not merely adjacent to it.
func TestSignSSHSIGRejectsModifiedDocument(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not available")
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	sig, err := signSSHSIG(signer, []byte("original\n"))
	if err != nil {
		t.Fatal(err)
	}
	sigPath := filepath.Join(dir, "doc.json.sig")
	if err := os.WriteFile(sigPath, sig, 0o600); err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(dir, "doc.json")
	if err := os.WriteFile(docPath, []byte("tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	line, _ := allowedSignersLine("auditor@example.com", signer.PublicKey())
	signersPath := filepath.Join(dir, "allowed_signers")
	if err := os.WriteFile(signersPath, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, err := os.Open(docPath)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()

	cmd := exec.Command(keygen, "-Y", "verify",
		"-f", signersPath, "-I", "auditor@example.com",
		"-n", sshsigNamespace, "-s", sigPath)
	cmd.Stdin = doc
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("ssh-keygen accepted a signature over different content:\n%s", out)
	}
}

// TestSignSSHSIGNamespaceIsBinding: the namespace stops an audit signature from
// being presented as a signature over anything else.
func TestSignSSHSIGNamespaceIsBinding(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not available")
	}

	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	msg := []byte("audit\n")
	sig, err := signSSHSIG(signer, msg)
	if err != nil {
		t.Fatal(err)
	}
	sigPath := filepath.Join(dir, "doc.sig")
	if err := os.WriteFile(sigPath, sig, 0o600); err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(dir, "doc")
	if err := os.WriteFile(docPath, msg, 0o600); err != nil {
		t.Fatal(err)
	}
	line, _ := allowedSignersLine("auditor@example.com", signer.PublicKey())
	signersPath := filepath.Join(dir, "allowed_signers")
	if err := os.WriteFile(signersPath, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	doc, err := os.Open(docPath)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()

	cmd := exec.Command(keygen, "-Y", "verify",
		"-f", signersPath, "-I", "auditor@example.com",
		"-n", "file", "-s", sigPath) // wrong namespace
	cmd.Stdin = doc
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("signature verified under the wrong namespace:\n%s", out)
	}
}
