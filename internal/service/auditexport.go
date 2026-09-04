package service

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// sshsigNamespace scopes the signature so a blob produced here can never be
// replayed as a signature over something else — ssh-keygen refuses to verify
// unless -n matches. "file" is OpenSSH's default; a distinct value means an
// audit export cannot be passed off as a signed file and vice versa.
const sshsigNamespace = "blacknode-audit"

const sshsigMagic = "SSHSIG"

// signSSHSIG produces a detached OpenSSH signature over msg.
//
// The point of implementing the SSHSIG wire format rather than inventing a
// signature envelope is verifiability by someone who doesn't run this app:
//
//	ssh-keygen -Y verify -f allowed_signers -I <identity> \
//	    -n blacknode-audit -s export.json.sig < export.json
//
// An HMAC, or a bespoke format, would have meant "trust our tool to check our
// own log" — which is worth very little in the one situation an audit log
// exists for. Format per OpenSSH's PROTOCOL.sshsig.
func signSSHSIG(signer ssh.Signer, msg []byte) ([]byte, error) {
	// OpenSSH hashes the message before signing so a verifier never has to hold
	// the whole thing in memory. sha512 is ssh-keygen's default.
	const hashAlg = "sha512"
	sum := sha512.Sum512(msg)
	h := sum[:]

	// The blob that is actually signed. MAGIC is six raw bytes, not a string.
	var signed sshWriter
	signed.raw([]byte(sshsigMagic))
	signed.str([]byte(sshsigNamespace))
	signed.str(nil) // reserved
	signed.str([]byte(hashAlg))
	signed.str(h)

	sig, err := signWithBestAlgorithm(signer, signed.b)
	if err != nil {
		return nil, err
	}

	var out sshWriter
	out.raw([]byte(sshsigMagic))
	out.u32(1) // SIG_VERSION
	out.str(signer.PublicKey().Marshal())
	out.str([]byte(sshsigNamespace))
	out.str(nil) // reserved
	out.str([]byte(hashAlg))
	out.str(ssh.Marshal(sig))

	return armorSignature(out.b), nil
}

// signWithBestAlgorithm avoids emitting an ssh-rsa (SHA-1) signature.
//
// ssh.Signer.Sign picks the key's default algorithm, which for RSA is still
// SHA-1 — and a modern ssh-keygen rejects that on verify, so the export would
// be unverifiable through no fault of the user's. Upgrade when the signer
// supports it, and leave ed25519/ecdsa alone (they have exactly one algorithm).
func signWithBestAlgorithm(signer ssh.Signer, data []byte) (*ssh.Signature, error) {
	if as, ok := signer.(ssh.AlgorithmSigner); ok && signer.PublicKey().Type() == ssh.KeyAlgoRSA {
		return as.SignWithAlgorithm(rand.Reader, data, ssh.KeyAlgoRSASHA512)
	}
	return signer.Sign(rand.Reader, data)
}

// armorSignature wraps the blob the way ssh-keygen writes it. The line width
// differs from OpenSSH's but its parser ignores wrapping.
func armorSignature(blob []byte) []byte {
	const begin = "-----BEGIN SSH SIGNATURE-----\n"
	const end = "-----END SSH SIGNATURE-----\n"
	enc := base64.StdEncoding.EncodeToString(blob)
	out := make([]byte, 0, len(begin)+len(enc)+len(enc)/70+len(end)+2)
	out = append(out, begin...)
	for len(enc) > 70 {
		out = append(out, enc[:70]...)
		out = append(out, '\n')
		enc = enc[70:]
	}
	if len(enc) > 0 {
		out = append(out, enc...)
		out = append(out, '\n')
	}
	out = append(out, end...)
	return out
}

// sshWriter builds SSH wire-format buffers: uint32 lengths, big-endian.
type sshWriter struct{ b []byte }

func (w *sshWriter) raw(p []byte) { w.b = append(w.b, p...) }

func (w *sshWriter) u32(v uint32) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], v)
	w.b = append(w.b, n[:]...)
}

func (w *sshWriter) str(p []byte) {
	w.u32(uint32(len(p)))
	w.b = append(w.b, p...)
}

// allowedSignersLine renders the entry a verifier needs in its allowed_signers
// file. Emitting it alongside the signature is deliberate: a signature whose
// public key the recipient has to go hunting for tends not to get verified.
func allowedSignersLine(identity string, pub ssh.PublicKey) (string, error) {
	if identity == "" {
		return "", errors.New("identity required for allowed_signers")
	}
	authorized := string(ssh.MarshalAuthorizedKey(pub)) // has trailing newline
	if authorized == "" {
		return "", errors.New("could not marshal public key")
	}
	return fmt.Sprintf("%s namespaces=\"%s\" %s", identity, sshsigNamespace, authorized), nil
}
