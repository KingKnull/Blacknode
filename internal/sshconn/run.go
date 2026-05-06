package sshconn

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// maxOutputBytes caps the output of a single-shot command to 5MB to prevent
// a runaway log or command from exhausting memory.
const maxOutputBytes = 5 * 1024 * 1024

// Run executes a single-shot command on an already-connected SSH client and
// returns the combined stdout+stderr output. The command is run in a fresh
// session that is closed when Run returns.
//
// Output is capped at 5MB; if the command produces more, the result is
// truncated and a "[output truncated at 5MB]" trailer is appended.
func Run(ctx context.Context, client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("session: %w", err)
	}
	defer sess.Close()

	var out strings.Builder
	sess.Stdout = &out
	sess.Stderr = &out

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case <-ctx.Done():
		// Wait for the session to be forcibly closed by the defer
		return out.String(), ctx.Err()
	case err := <-done:
		body := out.String()
		if len(body) > maxOutputBytes {
			body = body[:maxOutputBytes] + "\n[output truncated at 5MB]"
		}
		if err != nil {
			return body, err
		}
		return body, nil
	}
}

// RunSimple executes a single-shot command with no output cap.
// It respects the provided context for cancellation.
func RunSimple(ctx context.Context, client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("session: %w", err)
	}
	defer sess.Close()
	var out strings.Builder
	sess.Stdout = &out
	sess.Stderr = &out
	
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case <-ctx.Done():
		return out.String(), ctx.Err()
	case err := <-done:
		return out.String(), err
	}
}
