package proxy

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ovpnmux/internal/tunnel"
)

type Pool interface {
	Ready() []*tunnel.Tunnel
}

// Selector picks a ready tunnel. Key "" or "any" means any tunnel, anything
// else names a profile (file name without .ovpn). With ttl > 0 a client IP
// stays on the same automatically chosen tunnel for ttl.
type Selector struct {
	pool   Pool
	ttl    time.Duration
	rr     atomic.Uint64
	mu     sync.Mutex
	sticky map[string]stickyEntry
}

type stickyEntry struct {
	id    int
	until time.Time
}

func NewSelector(pool Pool, ttl time.Duration) *Selector {
	return &Selector{pool: pool, ttl: ttl, sticky: map[string]stickyEntry{}}
}

func (s *Selector) Select(key, clientIP string) (*tunnel.Tunnel, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	ready := s.pool.Ready()
	if key != "" && key != "any" {
		for _, t := range ready {
			if t.Matches(key) {
				return t, nil
			}
		}
		return nil, fmt.Errorf("no ready tunnel for profile %q", key)
	}
	if len(ready) == 0 {
		return nil, fmt.Errorf("no ready tunnel")
	}
	if s.ttl <= 0 {
		return ready[int(s.rr.Add(1)-1)%len(ready)], nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if e, ok := s.sticky[clientIP]; ok && now.Before(e.until) {
		for _, t := range ready {
			if t.ID == e.id {
				return t, nil
			}
		}
	}
	if len(s.sticky) > 10000 {
		for k, e := range s.sticky {
			if now.After(e.until) {
				delete(s.sticky, k)
			}
		}
	}
	t := ready[int(s.rr.Add(1)-1)%len(ready)]
	s.sticky[clientIP] = stickyEntry{id: t.ID, until: now.Add(s.ttl)}
	return t, nil
}
