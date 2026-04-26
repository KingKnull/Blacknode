package sshconn

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Pool reuses SSH clients across non-interactive operations (exec, sftp,
// metrics, logs). Interactive shells continue to dial fresh per session —
// the lifecycle there is bound to a UI tab, not a request.
//
// Keys are derived from (host:port, user, auth-material-hash) so two callers
// that would have produced an identical client share the connection.
type Pool struct {
	dialer *Dialer

	mu      sync.Mutex
	entries map[string]*pooled
	idleTTL time.Duration
}

type pooled struct {
	client    *ssh.Client
	lastUsed  time.Time
	refs      int
	closeOnce sync.Once
}

func NewPool(d *Dialer) *Pool {
	p := &Pool{
		dialer:  d,
		entries: make(map[string]*pooled),
		idleTTL: 5 * time.Minute,
	}
	go p.reaper()
	return p
}

func keyFor(t Target) string {
	h := sha256.New()
	h.Write([]byte(t.Host))
	h.Write([]byte{0})
	h.Write([]byte(t.User))
	h.Write([]byte{0})
	h.Write([]byte(string(t.AuthMethod)))
	h.Write([]byte{0})
	h.Write([]byte(t.Password))
	h.Write([]byte{0})
	h.Write([]byte(t.KeyID))
	var port [4]byte
	port[0] = byte(t.Port)
	port[1] = byte(t.Port >> 8)
	h.Write(port[:])
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns a live ssh.Client and a release func. Always defer release().
// If the client has dropped (closed in a goroutine, network blip), the next
// caller dials a fresh one transparently.
func (p *Pool) Get(t Target) (*ssh.Client, func(), error) {
	key := keyFor(t)

	p.mu.Lock()
	entry, ok := p.entries[key]
	if ok && entry.client != nil {
		entry.refs++
		entry.lastUsed = time.Now()
		p.mu.Unlock()
		// Probe — if the underlying connection is dead, drop and re-dial.
		if _, _, err := entry.client.SendRequest("keepalive@blacknode", true, nil); err != nil {
			p.mu.Lock()
			entry.refs--
			p.discardLocked(key, entry)
			p.mu.Unlock()
		} else {
			return entry.client, p.release(key), nil
		}
	} else {
		p.mu.Unlock()
	}

	client, err := p.dialer.Dial(t)
	if err != nil {
		return nil, func() {}, err
	}
	p.mu.Lock()
	// Another caller may have populated the slot while we were dialing; drop
	// ours if so to avoid leaking the second dial.
	if existing, ok := p.entries[key]; ok && existing.client != nil {
		existing.refs++
		existing.lastUsed = time.Now()
		p.mu.Unlock()
		_ = client.Close()
		return existing.client, p.release(key), nil
	}
	entry = &pooled{client: client, lastUsed: time.Now(), refs: 1}
	p.entries[key] = entry
	p.mu.Unlock()
	return client, p.release(key), nil
}

func (p *Pool) release(key string) func() {
	return func() {
		p.mu.Lock()
		entry, ok := p.entries[key]
		if ok {
			entry.refs--
			entry.lastUsed = time.Now()
		}
		p.mu.Unlock()
	}
}

func (p *Pool) discardLocked(key string, entry *pooled) {
	delete(p.entries, key)
	go entry.closeOnce.Do(func() { _ = entry.client.Close() })
}

func (p *Pool) reaper() {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		var toClose []*pooled
		p.mu.Lock()
		for k, e := range p.entries {
			if e.refs == 0 && now.Sub(e.lastUsed) > p.idleTTL {
				toClose = append(toClose, e)
				delete(p.entries, k)
			}
		}
		p.mu.Unlock()
		for _, e := range toClose {
			e.closeOnce.Do(func() { _ = e.client.Close() })
		}
	}
}

// Close drops every pooled client; call on app shutdown.
func (p *Pool) Close() {
	p.mu.Lock()
	entries := p.entries
	p.entries = make(map[string]*pooled)
	p.mu.Unlock()
	for _, e := range entries {
		e.closeOnce.Do(func() { _ = e.client.Close() })
	}
}
