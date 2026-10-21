package service

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"syscall"
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
	last   map[string]forwardStatus
}

type activeForward struct {
	forward    store.Forward
	listener   net.Listener // local listener or SSH remote listener
	sshRelease func()
	done       chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	closed     bool
	conns      map[net.Conn]struct{}
	status     string // guarded by the service mutex
	lastError  string // guarded by the service mutex
}

type forwardStatus struct {
	status string
	err    string
}

// Resources may arrive after Stop while an SSH dial is still finishing.
func (a *activeForward) attach(l net.Listener, release func()) bool {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		if l != nil {
			_ = l.Close()
		}
		if release != nil {
			release()
		}
		return false
	}
	if l != nil {
		a.listener = l
	}
	if release != nil {
		a.sshRelease = release
	}
	a.mu.Unlock()
	return true
}

func (a *activeForward) track(c net.Conn) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		_ = c.Close()
		return false
	}
	a.conns[c] = struct{}{}
	return true
}

func (a *activeForward) untrack(c net.Conn) {
	_ = c.Close()
	a.mu.Lock()
	delete(a.conns, c)
	a.mu.Unlock()
}

func (a *activeForward) close() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	close(a.done)
	a.cancel()
	l, release := a.listener, a.sshRelease
	conns := make([]net.Conn, 0, len(a.conns))
	for c := range a.conns {
		conns = append(conns, c)
	}
	a.mu.Unlock()
	if l != nil {
		_ = l.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
	if release != nil {
		release()
	}
}

func NewPortForwardService(pool *sshconn.Pool, h *store.Hosts, f *store.Forwards) *PortForwardService {
	return &PortForwardService{
		pool:     pool,
		hosts:    h,
		forwards: f,
		active:   make(map[string]*activeForward),
		last:     make(map[string]forwardStatus),
	}
}

// CRUD on the saved forwards.
func (s *PortForwardService) List(ctx context.Context) ([]ActiveForward, error) {
	rows, err := s.forwards.List()
	if err != nil {
		return nil, err
	}
	out := make([]ActiveForward, 0, len(rows))
	for _, f := range rows {
		s.mu.Lock()
		runtime := s.last[f.ID]
		if state := s.active[f.ID]; state != nil {
			runtime = forwardStatus{status: state.status, err: state.lastError}
		}
		s.mu.Unlock()
		if runtime.status == "" {
			runtime.status = "disconnected"
		}
		row := ActiveForward{Forward: f, Active: runtime.status == "active" || runtime.status == "failing", Status: runtime.status, LastError: runtime.err}
		// Bind and immediately close; never dial the listener, since doing so
		// would cause a local forward to contact its remote target.
		if !row.Active && runtime.status != "starting" && f.Kind != store.ForwardRemote {
			if err := checkLocalPort(f); err != nil {
				row.LocalPortConflict = errors.Is(err, syscall.EADDRINUSE)
				row.LocalPortError = err.Error()
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// ActiveForward bundles a saved forward with its current runtime state.
type ActiveForward struct {
	store.Forward
	Active            bool   `json:"active"`
	Status            string `json:"status"`
	LastError         string `json:"lastError"`
	LocalPortConflict bool   `json:"localPortConflict"`
	LocalPortError    string `json:"localPortError"`
}

func checkLocalPort(f store.Forward) error {
	l, err := listenLocal(f)
	if err != nil {
		return err
	}
	return l.Close()
}

func listenLocal(f store.Forward) (net.Listener, error) {
	addr := net.JoinHostPort(f.LocalAddr, strconv.Itoa(f.LocalPort))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot bind local port %s; free the port or choose another local port: %w", addr, err)
	}
	return l, nil
}

func (s *PortForwardService) Create(ctx context.Context, f store.Forward) (store.Forward, error) {
	return s.forwards.Create(f)
}

func (s *PortForwardService) Delete(ctx context.Context, id string) error {
	_ = s.Stop(id)
	s.mu.Lock()
	delete(s.last, id)
	s.mu.Unlock()
	return s.forwards.Delete(id)
}

// Start opens the listener (or remote bind), grabs an SSH client from the
// pool, and begins accepting connections in the background.
func (s *PortForwardService) Start(forwardID string) (err error) {
	ctx, cancel := context.WithCancel(context.Background())
	state := &activeForward{done: make(chan struct{}), ctx: ctx, cancel: cancel, conns: make(map[net.Conn]struct{}), status: "starting"}
	s.mu.Lock()
	if _, ok := s.active[forwardID]; ok {
		s.mu.Unlock()
		cancel()
		return errors.New("forward already running")
	}
	s.active[forwardID] = state
	delete(s.last, forwardID)
	s.mu.Unlock()
	defer func() {
		if err != nil {
			s.finish(forwardID, state, "failed", err.Error())
		}
	}()

	f, err := s.forwards.Get(forwardID)
	if err != nil {
		return fmt.Errorf("load forward: %w", err)
	}
	state.forward = f
	if f.Kind == store.ForwardLocal || f.Kind == store.ForwardDynamic {
		l, listenErr := listenLocal(f)
		if listenErr != nil {
			return listenErr
		}
		if !state.attach(l, nil) {
			return errors.New("forward start canceled")
		}
	}
	host, err := s.hosts.GetResolved(f.HostID)
	if err != nil {
		return fmt.Errorf("load host: %w", err)
	}
	if state.ctx.Err() != nil {
		return errors.New("forward start canceled")
	}
	client, release, err := s.pool.Get(sshconn.FromHost(host))
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	if !state.attach(nil, release) {
		return errors.New("forward start canceled")
	}

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
		return err
	}

	s.mu.Lock()
	if s.active[forwardID] != state {
		s.mu.Unlock()
		return errors.New("forward start canceled")
	}
	if state.status == "starting" {
		state.status = "active"
	}
	s.mu.Unlock()
	return nil
}

// An old accept loop must never tear down a newer run of the same profile.
func (s *PortForwardService) finish(id string, state *activeForward, status, message string) {
	s.mu.Lock()
	if s.active[id] == state {
		delete(s.active, id)
		s.last[id] = forwardStatus{status: status, err: message}
	}
	s.mu.Unlock()
	state.close()
}

func (s *PortForwardService) connectionResult(state *activeForward, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[state.forward.ID] != state {
		return
	}
	state.status, state.lastError = "active", ""
	if err != nil {
		state.status = "failing"
		state.lastError = fmt.Sprintf("Last forwarding attempt failed: %v", err)
	}
}

func (s *PortForwardService) Stop(forwardID string) error {
	s.mu.Lock()
	state, ok := s.active[forwardID]
	if ok {
		delete(s.active, forwardID)
	}
	s.last[forwardID] = forwardStatus{status: "disconnected"}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	state.close()
	return nil
}

func (s *PortForwardService) StopAll(ctx context.Context) {
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
	target := net.JoinHostPort(state.forward.RemoteAddr, strconv.Itoa(state.forward.RemotePort))
	s.serve(state, func(in net.Conn) {
		out, err := client.DialContext(state.ctx, "tcp", target)
		s.connectionResult(state, err)
		if err != nil {
			_ = in.Close()
			return
		}
		if !state.track(out) {
			return
		}
		defer state.untrack(out)
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
	if !state.attach(l, nil) {
		return errors.New("forward start canceled")
	}
	target := net.JoinHostPort(state.forward.LocalAddr, strconv.Itoa(state.forward.LocalPort))
	s.serve(state, func(in net.Conn) {
		out, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(state.ctx, "tcp", target)
		s.connectionResult(state, err)
		if err != nil {
			_ = in.Close()
			return
		}
		if !state.track(out) {
			return
		}
		defer state.untrack(out)
		proxy(in, out)
	})
	return nil
}

// startDynamic: minimal SOCKS5 (no-auth) server. Each accepted conn is
// negotiated, then dialed via the SSH client to the requested target.
func (s *PortForwardService) startDynamic(client *ssh.Client, state *activeForward) error {
	s.serve(state, func(in net.Conn) {
		target, err := socks5Handshake(in)
		if err != nil {
			_ = in.Close()
			return
		}
		out, err := client.DialContext(state.ctx, "tcp", target)
		s.connectionResult(state, err)
		if err != nil {
			_ = socks5Reply(in, 0x05) // connection refused
			_ = in.Close()
			return
		}
		if !state.track(out) {
			return
		}
		defer state.untrack(out)
		if err := socks5Reply(in, 0x00); err != nil {
			_ = out.Close()
			_ = in.Close()
			return
		}
		proxy(in, out)
	})
	return nil
}

func (s *PortForwardService) serve(state *activeForward, handle func(net.Conn)) {
	state.mu.Lock()
	l := state.listener
	state.mu.Unlock()
	go func() {
		err := acceptLoop(l, state.done, func(c net.Conn) {
			if !state.track(c) {
				return
			}
			defer state.untrack(c)
			handle(c)
		})
		if err != nil {
			s.finish(state.forward.ID, state, "disconnected", fmt.Sprintf("Tunnel listener closed: %v. Start the tunnel to retry.", err))
		}
	}()
}

func acceptLoop(l net.Listener, done <-chan struct{}, handle func(net.Conn)) error {
	for {
		c, err := l.Accept()
		if err != nil {
			select {
			case <-done:
				return nil
			default:
				return err
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
	// Close both ends so the second goroutine unblocks and exits promptly
	// rather than leaking until the remote side eventually closes.
	a.Close()
	b.Close()
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
	// RFC 1928 requires at least one method octet. Zero is malformed, and
	// accepting it meant replying "no-auth accepted" to a greeting that never
	// offered anything — a lax parser facing whatever can reach the local
	// SOCKS port, which is the one place worth being strict.
	if nMethods == 0 {
		return "", errors.New("socks5 greeting offered no methods")
	}
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
		// A zero-length domain used to yield host "", which JoinHostPort turns
		// into ":port" — and that dials the *remote* host's own loopback rather
		// than failing. Not a privilege gain (a client on the SOCKS port could
		// ask for localhost directly), but a silent redirect is the wrong
		// answer to a malformed request.
		if n == 0 {
			return "", errors.New("socks5 request had an empty domain")
		}
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
