// Package console mints single-use, short-lived tokens that authorize a browser
// (noVNC) websocket to a VM's VNC port. The token is the security boundary for
// the unauthenticated websocket endpoint: it is random, single-use, and expires
// quickly.
package console

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// Ticket is the resolved target a token authorizes.
type Ticket struct {
	VMID      string
	Host      string
	Port      int
	ExpiresAt time.Time
	used      bool
}

// TokenStore holds outstanding console tokens in memory.
type TokenStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	tickets map[string]*Ticket
}

// NewTokenStore returns a store whose tokens live for ttl.
func NewTokenStore(ttl time.Duration) *TokenStore {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &TokenStore{ttl: ttl, tickets: make(map[string]*Ticket)}
}

// Mint creates a single-use token for a VNC endpoint and returns it with the
// ticket (carrying the expiry).
func (s *TokenStore) Mint(vmID, host string, port int) (string, *Ticket, error) {
	tok, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	t := &Ticket{VMID: vmID, Host: host, Port: port, ExpiresAt: time.Now().Add(s.ttl)}
	s.mu.Lock()
	s.sweep()
	s.tickets[tok] = t
	s.mu.Unlock()
	return tok, t, nil
}

// Redeem validates and consumes a token. It returns the ticket only if the
// token exists, is unexpired, and was not already used; the token is removed so
// it cannot be reused.
func (s *TokenStore) Redeem(token string) (*Ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[token]
	if !ok {
		return nil, false
	}
	delete(s.tickets, token) // single-use
	if t.used || time.Now().After(t.ExpiresAt) {
		return nil, false
	}
	t.used = true
	return t, true
}

func (s *TokenStore) sweep() {
	now := time.Now()
	for k, t := range s.tickets {
		if now.After(t.ExpiresAt) {
			delete(s.tickets, k)
		}
	}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
