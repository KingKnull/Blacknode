package sshconn

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// DiagnosticStage is one bounded part of an SSH connection attempt.
type DiagnosticStage struct {
	Name       string `json:"name"`
	Status     string `json:"status"` // passed, failed, skipped, or canceled
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

// DiagnosticResult is safe to return to the UI: it contains connection
// metadata and errors, never passwords, private keys, or server banners.
type DiagnosticResult struct {
	Host     string            `json:"host"`
	Port     int               `json:"port"`
	Success  bool              `json:"success"`
	Canceled bool              `json:"canceled"`
	Stages   []DiagnosticStage `json:"stages"`
}

const diagnosticStageTimeout = 5 * time.Second

// Diagnose runs a non-interactive, bounded connection probe. Authentication
// uses only credentials already available to the Dialer; a diagnostic never
// opens a password or MFA prompt. The PTY check creates and closes a session
// without starting a shell or running a command.
func (d *Dialer) Diagnose(ctx context.Context, t Target) DiagnosticResult {
	if ctx == nil {
		ctx = context.Background()
	}
	result := DiagnosticResult{Host: t.Host, Port: t.Port, Stages: make([]DiagnosticStage, 0, 5)}
	if result.Port == 0 {
		result.Port = 22
	}
	if t.Host == "" || t.User == "" {
		result.Stages = append(result.Stages, DiagnosticStage{Name: "dns", Status: "failed", Error: "host and user required"})
		return result
	}

	add := func(name string, started time.Time, status string, err error) {
		stage := DiagnosticStage{Name: name, Status: status, DurationMS: time.Since(started).Milliseconds()}
		if err != nil {
			stage.Error = diagnosticError(err)
		}
		result.Stages = append(result.Stages, stage)
	}
	stageContext := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(ctx, diagnosticStageTimeout)
	}

	// DNS
	dnsStart := time.Now()
	dnsCtx, dnsCancel := stageContext()
	addrs, err := d.lookupHost(dnsCtx, t.Host)
	dnsCancel()
	if err != nil {
		status := "failed"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = "canceled"
			result.Canceled = ctx.Err() != nil
		}
		add("dns", dnsStart, status, err)
		return result
	}
	add("dns", dnsStart, "passed", nil)

	// TCP
	tcpStart := time.Now()
	tcpCtx, tcpCancel := stageContext()
	var raw net.Conn
	var tcpErr error
	for _, resolved := range addrs {
		raw, tcpErr = (&net.Dialer{}).DialContext(tcpCtx, "tcp", net.JoinHostPort(resolved, strconv.Itoa(result.Port)))
		if tcpErr == nil {
			break
		}
		if tcpCtx.Err() != nil {
			break
		}
	}
	tcpCancel()
	if tcpErr != nil {
		status := "failed"
		if errors.Is(tcpErr, context.Canceled) || errors.Is(tcpErr, context.DeadlineExceeded) {
			status = "canceled"
			result.Canceled = ctx.Err() != nil
		}
		add("tcp", tcpStart, status, tcpErr)
		return result
	}
	add("tcp", tcpStart, "passed", nil)

	// Disable interactive prompting for diagnostics. A saved password/key can
	// still be tested, while a missing or rejected credential returns promptly.
	diagnosticDialer := *d
	diagnosticDialer.Prompter = nil
	auth, err := diagnosticDialer.authFor(t)
	if err != nil {
		_ = raw.Close()
		add("ssh-handshake", time.Now(), "skipped", nil)
		add("auth", time.Now(), "failed", err)
		return result
	}

	cfg := &ssh.ClientConfig{
		User:            t.User,
		Auth:            auth,
		HostKeyCallback: d.hostKeyCallback(),
		Timeout:         diagnosticStageTimeout,
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(result.Port))
	sshStart := time.Now()
	sshCtx, sshCancel := stageContext()
	stopClose := make(chan struct{})
	go func() {
		select {
		case <-sshCtx.Done():
			_ = raw.Close()
		case <-stopClose:
		}
	}()
	sshConn, chans, reqs, sshErr := ssh.NewClientConn(raw, addr, cfg)
	close(stopClose)
	sshCancel()
	if sshErr != nil {
		if isAuthFailure(sshErr) {
			add("ssh-handshake", sshStart, "passed", nil)
			add("auth", sshStart, diagnosticStatus(sshErr, ctx), sshErr)
		} else {
			add("ssh-handshake", sshStart, diagnosticStatus(sshErr, ctx), sshErr)
			add("auth", sshStart, "skipped", nil)
		}
		result.Canceled = ctx.Err() != nil
		return result
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()
	add("ssh-handshake", sshStart, "passed", nil)
	add("auth", sshStart, "passed", nil)

	ptyStart := time.Now()
	ptyCtx, ptyCancel := stageContext()
	type ptyResult struct {
		session *ssh.Session
		err     error
	}
	ptyDone := make(chan ptyResult, 1)
	go func() {
		sess, sessionErr := client.NewSession()
		if sessionErr == nil {
			sessionErr = sess.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{ssh.ECHO: 1})
		}
		ptyDone <- ptyResult{session: sess, err: sessionErr}
	}()
	var pty ptyResult
	select {
	case pty = <-ptyDone:
		ptyCancel()
	case <-ptyCtx.Done():
		_ = client.Close()
		pty = ptyResult{err: ptyCtx.Err()}
		ptyCancel()
	}
	if pty.session != nil {
		_ = pty.session.Close()
	}
	if pty.err != nil {
		add("pty", ptyStart, diagnosticStatus(pty.err, ctx), pty.err)
		result.Canceled = ctx.Err() != nil
		return result
	}
	add("pty", ptyStart, "passed", nil)
	result.Success = true
	return result
}

func diagnosticStatus(err error, parent context.Context) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if parent.Err() != nil {
			return "canceled"
		}
		return "failed"
	}
	return "failed"
}

func diagnosticError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "connection attempt canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "stage timed out"
	}
	return err.Error()
}

func isAuthFailure(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unable to authenticate") ||
		strings.Contains(s, "no supported methods") ||
		strings.Contains(s, "permission denied") ||
		strings.Contains(s, "authentication")
}
