package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/blacknode/blacknode/internal/sshconn"
	"github.com/blacknode/blacknode/internal/store"
	"golang.org/x/crypto/ssh"
)

// PortForwardService provides local/remote/dynamic SSH tunnels persisted in
// the DB. Each active forward holds a pooled SSH client for as long as it
// runs, so the connection is shared with any other operation on the same host.
type PortForwardService struct {
	pool     *sshconn.Pool
	hosts    *store.Hosts
	forwards *store.Forwards

	mu     sync.Mutex
	active map[string]*activeForward
}

type activeForward struct {
	forward    store.Forward
	listener   net.Listener // local listener for local/dynamic; nil for remote
	sshRelease func()
	done       chan struct{}
}

func NewPortForwardService(pool *sshconn.Pool, h *store.Hosts, f *store.Forwards) *PortForwardService {
	return &PortForwardService{
		pool:     pool,
		hosts:    h,
		forwards: f,
		active:   make(map[string]*activeForward),
	}
}

// CRUD on the saved forwards.
func (s *PortForwardService) List() ([]ActiveForward, error) {
	rows, err := s.forwards.List()
	if err != nil {
		return nil, err
	}
	out := make([]ActiveForward, 0, len(rows))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range rows {
		out = append(out, ActiveForward{Forward: f, Active: s.active[f.ID] != nil})
	}
	return out, nil
}

// ActiveForward bundles a saved forward with its current runtime state.
type ActiveForward struct {
	store.Forward
	Active bool `json:"active"`
}

func (s *PortForwardService) Create(f store.Forward) (store.Forward, error) {
	return s.forwards.Create(f)
}

func (s *PortForwardService) Delete(id string) error {
	_ = s.Stop(id)
	return s.forwards.Delete(id)
}

// Start opens the listener (or remote bind), grabs an SSH client from the
// pool, and begins accepting connections in the background. password is the
// runtime SSH password for password-auth hosts (transient).
func (s *PortForwardService) Start(forwardID, password string) error {
	s.mu.Lock()
	if _, ok := s.active[forwardID]; ok {
		s.mu.Unlock()
		return errors.New("forward already running")
	}
	s.mu.Unlock()

	f, err := s.forwards.Get(forwardID)
	if err != nil {
		return fmt.Errorf("load forward: %w", err)
	}
	host, err := s.hosts.Get(f.HostID)
	if err != nil {
		return fmt.Errorf("load host: %w", err)
	}
	client, release, err := s.pool.Get(sshconn.FromHost(host, password))
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	state := &activeForward{forward: f, sshRelease: release, done: make(chan struct{})}

	switch f.Kind {
	case store.ForwardLocal:
		err = s.startLocal(client, state)
	case store.ForwardRemote:
		err = s.startRemote(client, state)
	case store.ForwardDynamic:
		err = s.startDynamic(client, state)
	default:
		err = fmt.Errorf("unknown kind %q", f.Kind)
	}
	if err != nil {
		release()
		return err
	}

	s.mu.Lock()
	s.active[forwardID] = state
	s.mu.Unlock()
	return nil
}

func (s *PortForwardService) Stop(forwardID string) error {
	s.mu.Lock()
	state, ok := s.active[forwardID]
	if ok {
		delete(s.active, forwardID)
	}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	close(state.done)
	if state.listener != nil {
		_ = state.listener.Close()
	}
	state.sshRelease()
	return nil
}

func (s *PortForwardService) StopAll() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.active))
	for id := range s.active {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_ = s.Stop(id)
	}
}

// startLocal: bind on localhost:LocalPort; per-conn dial through SSH to
// RemoteAddr:RemotePort and copy bytes both ways.
func (s *PortForwardService) startLocal(client *ssh.Client, state *activeForward) error {
	addr := net.JoinHostPort(state.forward.LocalAddr, strconv.Itoa(state.forward.LocalPort))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("local listen %s: %w", addr, err)
	}
	state.listener = l
	target := net.JoinHostPort(state.forward.RemoteAddr, strconv.Itoa(state.forward.RemotePort))
	go acceptLoop(l, state.done, func(in net.Conn) {
		out, err := client.Dial("tcp", target)
		if err != nil {
			_ = in.Close()
			return
		}
		proxy(in, out)
	})
	return nil
}

// startRemote: ask the SSH server to bind a port, accept conns from there,
// dial them back to LocalAddr:LocalPort.
func (s *PortForwardService) startRemote(client *ssh.Client, state *activeForward) error {
	bind := net.JoinHostPort("0.0.0.0", strconv.Itoa(state.forward.RemotePort))
	l, err := client.Listen("tcp", bind)
	if err != nil {
		return fmt.Errorf("remote listen %s: %w", bind, err)
	}
	state.listener = l
	target := net.JoinHostPort(state.forward.LocalAddr, strconv.Itoa(state.forward.LocalPort))
	go acceptLoop(l, state.done, func(in net.Conn) {
		out, err := net.DialTimeout("tcp", target, 10*time.Second)
		if err != nil {
			_ = in.Close()
			return
		}
		proxy(in, out)
	})
	return nil
}

// startDynamic: minimal SOCKS5 (no-auth) server. Each accepted conn is
// negotiated, then dialed via the SSH client to the requested target.
func (s *PortForwardService) startDynamic(client *ssh.Client, state *activeForward) error {
	addr := net.JoinHostPort(state.forward.LocalAddr, strconv.Itoa(state.forward.LocalPort))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("socks listen %s: %w", addr, err)
	}
	state.listener = l
	go acceptLoop(l, state.done, func(in net.Conn) {
		target, err := socks5Handshake(in)
		if err != nil {
			_ = in.Close()
			return
		}
		out, err := client.Dial("tcp", target)
		if err != nil {
			_ = socks5Reply(in, 0x05) // connection refused
			_ = in.Close()
			return
		}
		if err := socks5Reply(in, 0x00); err != nil {
			_ = out.Close()
			_ = in.Close()
			return
		}
		proxy(in, out)
	})
	return nil
}

func acceptLoop(l net.Listener, done <-chan struct{}, handle func(net.Conn)) {
	for {
		c, err := l.Accept()
		if err != nil {
			select {
			case <-done:
				return
			default:
				return // listener closed for another reason
			}
		}
		go handle(c)
	}
}

func proxy(a, b io.ReadWriteCloser) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
}

// --- minimal SOCKS5 (RFC 1928) — no auth, CONNECT only --------------------
//
// This is just enough to support browser/CLI dynamic-tunnel use. Skipped:
// authentication methods, BIND, UDP ASSOCIATE.

func socks5Handshake(c net.Conn) (string, error) {
	if err := c.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return "", err
	}
	defer c.SetDeadline(time.Time{})

	buf := make([]byte, 262)
	// Greeting: VER NMETHODS METHODS...
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return "", err
	}
	if buf[0] != 0x05 {
		return "", errors.New("not socks5")
	}
	nMethods := int(buf[1])
	if _, err := io.ReadFull(c, buf[:nMethods]); err != nil {
		return "", err
	}
	// We only support no-auth (0x00).
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return "", err
	}
	// Request: VER CMD RSV ATYP DST.ADDR DST.PORT
	if _, err := io.ReadFull(c, buf[:4]); err != nil {
		return "", err
	}
	if buf[0] != 0x05 || buf[1] != 0x01 {
		return "", errors.New("only CONNECT supported")
	}
	atyp := buf[3]
	var host string
	switch atyp {
	case 0x01: // IPv4
		if _, err := io.ReadFull(c, buf[:4]); err != nil {
			return "", err
		}
		host = net.IP(buf[:4]).String()
	case 0x03: // domain
		if _, err := io.ReadFull(c, buf[:1]); err != nil {
			return "", err
		}
		n := int(buf[0])
		if _, err := io.ReadFull(c, buf[:n]); err != nil {
			return "", err
		}
		host = string(buf[:n])
	case 0x04: // IPv6
		if _, err := io.ReadFull(c, buf[:16]); err != nil {
			return "", err
		}
		host = net.IP(buf[:16]).String()
	default:
		return "", errors.New("bad atyp")
	}
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(buf[:2])
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func socks5Reply(c net.Conn, status byte) error {
	// VER REP RSV ATYP BND.ADDR(0.0.0.0) BND.PORT(0)
	_, err := c.Write([]byte{0x05, status, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return err
}
