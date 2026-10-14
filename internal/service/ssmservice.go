package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aymanbagabas/go-pty"
	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type SSMProfile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Target      string `json:"target"`
	Region      string `json:"region"`
	AWSProfile  string `json:"awsProfile"`
	Environment string `json:"environment"`
}
type SSMAvailability struct {
	AWS    bool `json:"aws"`
	Plugin bool `json:"plugin"`
}
type ssmSession struct {
	pty pty.Pty
	cmd *pty.Cmd
}
type SSMService struct {
	store    *store.Operations
	mu       sync.Mutex
	wg       sync.WaitGroup
	stopped  bool
	sessions map[string]*ssmSession
}

func NewSSMService(documents *store.Operations) *SSMService {
	return &SSMService{store: documents, sessions: map[string]*ssmSession{}}
}

var ssmTarget = regexp.MustCompile(`^(i-([0-9a-f]{8}|[0-9a-f]{17})|mi-[0-9a-f]{17})$`)
var awsRegion = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)

func validateSSM(p SSMProfile) error {
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 120 || !ssmTarget.MatchString(p.Target) || !awsRegion.MatchString(p.Region) || len(p.AWSProfile) > 120 || strings.ContainsAny(p.AWSProfile, "\x00\r\n") {
		return errors.New("name, instance/managed-node ID and AWS region required")
	}
	return nil
}
func (s *SSMService) Save(ctx context.Context, p SSMProfile, revision int64) (store.OperationDocument, error) {
	if err := validateSSM(p); err != nil {
		return store.OperationDocument{}, err
	}
	if revision < 0 {
		return store.OperationDocument{}, errors.New("invalid revision")
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	return s.store.Put("ssm-profile", p.ID, revision, p)
}
func (s *SSMService) Profiles(ctx context.Context) ([]store.OperationDocument, error) {
	return s.store.List("ssm-profile", 500)
}
func (s *SSMService) Available(ctx context.Context) SSMAvailability {
	_, a := exec.LookPath("aws")
	_, b := exec.LookPath("session-manager-plugin")
	return SSMAvailability{AWS: a == nil, Plugin: b == nil}
}
func ssmArguments(p SSMProfile) []string {
	args := []string{"--region", p.Region}
	if p.AWSProfile != "" {
		args = append(args, "--profile", p.AWSProfile)
	}
	return append(args, "ssm", "start-session", "--target", p.Target, "--reason", "Blacknode interactive session")
}
func (s *SSMService) Connect(ctx context.Context, id, profileID string, cols, rows int) error {
	if id == "" || len(id) > 160 || cols < 1 || cols > 1000 || rows < 1 || rows > 1000 {
		return errors.New("invalid terminal identity or dimensions")
	}
	var p SSMProfile
	if err := s.store.Read("ssm-profile", profileID, &p); err != nil {
		return err
	}
	if err := validateSSM(p); err != nil {
		return err
	}
	available := s.Available(ctx)
	if !available.AWS || !available.Plugin {
		return errors.New("install AWS CLI and the Session Manager plugin, then sign in to the selected AWS profile")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return errors.New("session service is stopping")
	}
	if len(s.sessions) >= 32 {
		return errors.New("too many AWS sessions")
	}
	if _, ok := s.sessions[id]; ok {
		return errors.New("session already connected")
	}
	terminal, err := pty.New()
	if err != nil {
		return err
	}
	if err = terminal.Resize(cols, rows); err != nil {
		terminal.Close()
		return err
	}
	command := terminal.Command("aws", ssmArguments(p)...)
	command.Env = append(ptyEnv(), "AWS_PAGER=")
	if err = command.Start(); err != nil {
		terminal.Close()
		return err
	}
	session := &ssmSession{pty: terminal, cmd: command}
	s.sessions[id] = session
	if _, err = s.store.Append("ssm:"+profileID, "session.started", p.Name, map[string]string{"sessionID": id, "target": p.Target}); err != nil {
		terminal.Close()
		if command.Process != nil {
			command.Process.Kill()
		}
		command.Wait()
		delete(s.sessions, id)
		return err
	}
	readDone := make(chan struct{})
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		defer close(readDone)
		buf := make([]byte, 32768)
		for {
			n, e := terminal.Read(buf)
			if n > 0 {
				if a := application.Get(); a != nil {
					a.Event.Emit("terminal:data", TerminalData{SessionID: id, Data: string(buf[:n])})
				}
			}
			if e != nil {
				return
			}
		}
	}()
	go func() {
		defer s.wg.Done()
		err := command.Wait()
		select {
		case <-readDone:
		case <-time.After(500 * time.Millisecond):
		}
		terminal.Close()
		<-readDone
		reason := "Session ended"
		if err != nil {
			reason = err.Error()
		}
		s.mu.Lock()
		if s.sessions[id] == session {
			delete(s.sessions, id)
		}
		s.mu.Unlock()
		terminal.Close()
		s.store.Append("ssm:"+profileID, "session.ended", p.Name, map[string]string{"sessionID": id, "reason": reason})
		if a := application.Get(); a != nil {
			a.Event.Emit("terminal:exit", TerminalExit{SessionID: id, Reason: reason})
		}
	}()
	return nil
}
func (s *SSMService) Write(ctx context.Context, id, data string) error {
	s.mu.Lock()
	session := s.sessions[id]
	s.mu.Unlock()
	if session == nil {
		return errors.New("session is disconnected")
	}
	if len(data) > 1<<20 {
		return errors.New("input too large")
	}
	_, err := session.pty.Write([]byte(data))
	return err
}
func (s *SSMService) Resize(ctx context.Context, id string, cols, rows int) error {
	if cols < 1 || cols > 1000 || rows < 1 || rows > 1000 {
		return errors.New("invalid terminal dimensions")
	}
	s.mu.Lock()
	session := s.sessions[id]
	s.mu.Unlock()
	if session == nil {
		return errors.New("session is disconnected")
	}
	return session.pty.Resize(cols, rows)
}
func (s *SSMService) Disconnect(ctx context.Context, id string) error {
	s.mu.Lock()
	session := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	if session != nil {
		session.pty.Close()
		if session.cmd.Process != nil {
			session.cmd.Process.Kill()
		}
	}
	return nil
}
func (s *SSMService) Stop() {
	s.mu.Lock()
	s.stopped = true
	ids := []string{}
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.Disconnect(context.Background(), id)
	}
	s.wg.Wait()
}

// AWS CLI credentials remain in the user's existing AWS provider chain.
func (s *SSMService) Identity(ctx context.Context, profileID string) (string, error) {
	var p SSMProfile
	if err := s.store.Read("ssm-profile", profileID, &p); err != nil {
		return "", err
	}
	if err := validateSSM(p); err != nil {
		return "", err
	}
	args := []string{"--region", p.Region}
	if p.AWSProfile != "" {
		args = append(args, "--profile", p.AWSProfile)
	}
	args = append(args, "sts", "get-caller-identity", "--output", "json", "--no-cli-pager")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "aws", args...)
	command.WaitDelay = 2 * time.Second
	var out, stderr cappedOutput
	command.Stdout = &out
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("AWS identity: %s: %w", stderr.String(), err)
	}
	var value any
	if err := json.Unmarshal(out.Bytes(), &value); err != nil {
		return "", err
	}
	pretty, err := json.MarshalIndent(value, "", "  ")
	return string(pretty), err
}
