package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// authPromptTimeout bounds how long a handshake waits on the user.
//
// It has to be generous — a Duo push or a hunt for a hardware token takes real
// time — but it cannot be unbounded: the SSH handshake holds a TCP connection
// and a pool slot while it waits, and a prompt the user closed by quitting the
// window would otherwise pin both forever.
const authPromptTimeout = 2 * time.Minute

// AuthQuestion is a single prompt. Echo reports whether the answer should be
// visible while typing — true for "Username:", false for a password or OTP.
type AuthQuestion struct {
	Prompt string `json:"prompt"`
	Echo   bool   `json:"echo"`
}

// AuthPromptRequest is emitted on "auth:prompt" when a handshake needs input
// that cannot be resolved from stored state.
type AuthPromptRequest struct {
	ID     string `json:"id"`
	HostID string `json:"hostId,omitempty"`
	Host   string `json:"host"`
	User   string `json:"user"`

	// Kind is "password" or "keyboard-interactive". The UI renders both as a
	// prompt but the wording differs: one is a retry of a known credential, the
	// other is a challenge only the server can explain.
	Kind string `json:"kind"`

	// Attempt is the 1-based try number for password prompts, so the UI can say
	// "Incorrect password — 2 of 3" rather than repeating a bare prompt.
	Attempt int `json:"attempt,omitempty"`

	// Name and Instruction are the server's own text for a keyboard-interactive
	// round. They are shown verbatim: this is where a server explains "Enter
	// your 6-digit code" or "Password expired, choose a new one", and
	// paraphrasing it would lose information only the server has.
	Name        string `json:"name,omitempty"`
	Instruction string `json:"instruction,omitempty"`

	Questions []AuthQuestion `json:"questions"`
}

type authReply struct {
	answers  []string
	canceled bool
}

// AuthPromptService bridges sshconn.Prompter to the UI. The dialer calls it from
// inside the SSH handshake goroutine and blocks; the frontend answers by calling
// Submit or Cancel with the request ID it received in the event.
//
// This is what makes keyboard-interactive auth possible at all: the SSH protocol
// requires answers mid-handshake, so something has to turn a synchronous
// callback into an asynchronous round trip to a UI.
type AuthPromptService struct {
	mu      sync.Mutex
	pending map[string]chan authReply
}

func NewAuthPromptService() *AuthPromptService {
	return &AuthPromptService{pending: make(map[string]chan authReply)}
}

// compile-time assertion that we still satisfy the dialer's interface.
var _ sshconn.Prompter = (*AuthPromptService)(nil)

// Password asks the user for a password mid-handshake, after a stored or typed
// one was rejected.
func (s *AuthPromptService) Password(t sshconn.Target, attempt int) (string, error) {
	answers, err := s.ask(AuthPromptRequest{
		HostID:    t.HostID,
		Host:      t.Host,
		User:      t.User,
		Kind:      "password",
		Attempt:   attempt,
		Questions: []AuthQuestion{{Prompt: "Password", Echo: false}},
	})
	if err != nil {
		return "", err
	}
	if len(answers) == 0 {
		return "", errors.New("no password provided")
	}
	return answers[0], nil
}

// KeyboardInteractive answers one round of server challenges — the path that
// carries TOTP codes, Duo confirmations and PAM password changes.
func (s *AuthPromptService) KeyboardInteractive(t sshconn.Target, c sshconn.Challenge) ([]string, error) {
	qs := make([]AuthQuestion, len(c.Questions))
	for i, q := range c.Questions {
		// Echos is server-supplied and has occasionally been seen shorter than
		// Questions in the wild; default to not echoing, which is the safe
		// direction for something that might be a secret.
		echo := false
		if i < len(c.Echos) {
			echo = c.Echos[i]
		}
		qs[i] = AuthQuestion{Prompt: q, Echo: echo}
	}
	return s.ask(AuthPromptRequest{
		HostID:      t.HostID,
		Host:        t.Host,
		User:        t.User,
		Kind:        "keyboard-interactive",
		Name:        c.Name,
		Instruction: c.Instruction,
		Questions:   qs,
	})
}

// ask emits the request and blocks until the frontend replies, the user
// cancels, or the timeout expires.
func (s *AuthPromptService) ask(req AuthPromptRequest) ([]string, error) {
	app := application.Get()
	if app == nil {
		// No UI to ask (headless test or teardown). Failing here is correct:
		// silently returning an empty answer would burn an auth attempt.
		return nil, errors.New("no UI available to prompt for credentials")
	}
	req.ID = uuid.NewString()
	// Buffered so a reply that arrives just as the timeout fires doesn't block
	// the frontend's Submit call on a receiver that has already gone away.
	ch := make(chan authReply, 1)

	s.mu.Lock()
	s.pending[req.ID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, req.ID)
		s.mu.Unlock()
	}()

	app.Event.Emit("auth:prompt", req)

	select {
	case reply := <-ch:
		if reply.canceled {
			return nil, errors.New("authentication canceled")
		}
		return reply.answers, nil
	case <-time.After(authPromptTimeout):
		// Tell the UI to drop the dialog; otherwise it sits there collecting an
		// answer for a handshake that has already given up.
		app.Event.Emit("auth:prompt:expired", req.ID)
		return nil, fmt.Errorf("timed out waiting for credentials for %s@%s", req.User, req.Host)
	}
}

// Submit delivers the user's answers for a pending prompt. Unknown IDs are not
// an error — the prompt may have timed out or the connection dropped while the
// dialog was open, and surfacing that as a failure to the UI is noise.
func (s *AuthPromptService) Submit(ctx context.Context, id string, answers []string) error {
	return s.deliver(id, authReply{answers: answers})
}

// Cancel abandons a pending prompt, failing the handshake it belongs to.
func (s *AuthPromptService) Cancel(ctx context.Context, id string) error {
	return s.deliver(id, authReply{canceled: true})
}

func (s *AuthPromptService) deliver(id string, r authReply) error {
	s.mu.Lock()
	ch, ok := s.pending[id]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	select {
	case ch <- r:
	default:
		// Already answered. Second Submit for the same ID is a UI double-fire.
	}
	return nil
}

// CancelAll abandons every pending prompt. Called when the vault locks or the
// app is shutting down, so half-finished handshakes don't linger.
func (s *AuthPromptService) CancelAll(ctx context.Context) error {
	s.mu.Lock()
	ids := make([]string, 0, len(s.pending))
	for id := range s.pending {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_ = s.deliver(id, authReply{canceled: true})
	}
	return nil
}
