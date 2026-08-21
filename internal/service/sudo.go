package service

import "github.com/blacknode/blacknode/internal/store"

// localSudoHostID is the pseudo-host id the local shell's sudo password is
// stored under. Local panes have no host record, but they do hit sudo prompts.
const localSudoHostID = "local"

// secretResolver unseals a stored credential for a host. Implemented by
// sshconn.Dialer; declared as an interface here so session-owning services
// can inject a sudo password without depending on the whole dial path.
type secretResolver interface {
	ResolveSecret(kind store.Kind, hostID string) (string, error)
}

// injectSudoPassword resolves the sudo password saved for hostID and writes it
// into a PTY via `write`, followed by the newline that submits it.
//
// This exists so the plaintext stays in the backend. The UI detects the sudo
// prompt and asks for an injection by session id; it never receives the
// password itself. Reports false when nothing is stored, which is the signal
// for the UI to prompt the user inline instead.
func injectSudoPassword(r secretResolver, hostID string, write func(string) error) (bool, error) {
	if r == nil || hostID == "" {
		return false, nil
	}
	password, err := r.ResolveSecret(store.KindSudo, hostID)
	if err != nil {
		return false, err
	}
	if password == "" {
		return false, nil
	}
	if err := write(password + "\n"); err != nil {
		return false, err
	}
	return true, nil
}
