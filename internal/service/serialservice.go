package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/wailsapp/wails/v3/pkg/application"
	"go.bug.st/serial"
)

// SerialService provides serial-console sessions (e.g. a switch/router console
// cable, a microcontroller, a BMC). It opens the OS serial device directly and
// pipes bytes through the same terminal:data / terminal:exit events as every
// other session type, so the frontend treats it like any terminal.
type SerialService struct {
	hosts *store.Hosts

	mu       sync.Mutex
	sessions map[string]*serialSession
}

type serialSession struct {
	port   serial.Port
	cancel chan struct{}
}

func NewSerialService(h *store.Hosts) *SerialService {
	return &SerialService{hosts: h, sessions: make(map[string]*serialSession)}
}

// Ports lists the serial devices available on this machine, for the host
// editor's device picker.
func (s *SerialService) Ports(ctx context.Context) ([]string, error) {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}
	if ports == nil {
		ports = []string{}
	}
	return ports, nil
}

// Connect opens the host's serial device at its configured baud rate. Device
// and baud come from the saved host (SerialDevice / SerialBaud); baud defaults
// to 115200 when unset.
func (s *SerialService) Connect(ctx context.Context, sessionID, hostID string) error {
	if sessionID == "" {
		return errors.New("sessionID required")
	}
	h, err := s.hosts.Get(hostID)
	if err != nil {
		return fmt.Errorf("load host: %w", err)
	}
	device := h.SerialDevice
	if device == "" {
		return errors.New("no serial device configured for this host")
	}
	baud := h.SerialBaud
	if baud <= 0 {
		baud = 115200
	}

	s.mu.Lock()
	if _, exists := s.sessions[sessionID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("session %s already open", sessionID)
	}
	s.mu.Unlock()

	port, err := serial.Open(device, &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	})
	if err != nil {
		return fmt.Errorf("open %s: %w", device, err)
	}

	sess := &serialSession{port: port, cancel: make(chan struct{})}
	s.mu.Lock()
	s.sessions[sessionID] = sess
	s.mu.Unlock()

	go s.pump(sessionID, port, sess.cancel)
	return nil
}

func (s *SerialService) Write(ctx context.Context, sessionID, data string) error {
	sess := s.get(sessionID)
	if sess == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}
	_, err := sess.port.Write([]byte(data))
	return err
}

// Resize is a no-op for serial consoles (there is no remote PTY to size), but
// the frontend calls it uniformly across session types.
func (s *SerialService) Resize(ctx context.Context, sessionID string, cols, rows int) error {
	return nil
}

func (s *SerialService) Disconnect(ctx context.Context, sessionID string) error {
	s.cleanup(sessionID, "disconnected by user")
	return nil
}

func (s *SerialService) get(sessionID string) *serialSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sessionID]
}

func (s *SerialService) pump(id string, r io.Reader, cancel <-chan struct{}) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-cancel:
			return
		default:
		}
		n, err := r.Read(buf)
		if n > 0 {
			s.emitData(id, string(buf[:n]))
		}
		if err != nil {
			s.cleanup(id, err.Error())
			return
		}
	}
}

func (s *SerialService) cleanup(sessionID, reason string) {
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	if ok {
		delete(s.sessions, sessionID)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	close(sess.cancel)
	_ = sess.port.Close()
	s.emitExit(sessionID, reason)
}

func (s *SerialService) emitData(id, chunk string) {
	if a := application.Get(); a != nil {
		a.Event.Emit("terminal:data", TerminalData{SessionID: id, Data: chunk})
	}
}

func (s *SerialService) emitExit(id, reason string) {
	if a := application.Get(); a != nil {
		a.Event.Emit("terminal:exit", TerminalExit{SessionID: id, Reason: reason})
	}
}
