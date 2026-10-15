package proxy

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ovpnmux/internal/tunnel"
)

type Pool interface {
	Ready() []*tunnel.Tunnel
}

// Selector picks a ready tunnel for a selection key:
//
//	""/"any"     any tunnel
//	"jp"         tunnels whose exit country is JP
//	"tunnel:3"   tunnel 3
//	"name:X"     tunnel running profile named X
//
// Policy "rr" round-robins, "sticky" keeps a client on the same tunnel for ttl.
type Selector struct {
	pool   Pool
	policy string
	ttl    time.Duration
	rr     atomic.Uint64
	mu     sync.Mutex
	sticky map[string]stickyEntry
}

type stickyEntry struct {
	id    int
	until time.Time
}

func NewSelector(pool Pool, policy string, ttl time.Duration) *Selector {
	return &Selector{pool: pool, policy: policy, ttl: ttl, sticky: map[string]stickyEntry{}}
}

func (s *Selector) Select(key, clientIP string) (*tunnel.Tunnel, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	cands := s.candidates(key)
	if len(cands) == 0 {
		return nil, fmt.Errorf("no ready tunnel for %q", key)
	}
	if s.policy != "sticky" {
		return cands[int(s.rr.Add(1)-1)%len(cands)], nil
	}
	sk := clientIP + "|" + key
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if e, ok := s.sticky[sk]; ok && now.Before(e.until) {
		for _, t := range cands {
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
	t := cands[int(s.rr.Add(1)-1)%len(cands)]
	s.sticky[sk] = stickyEntry{id: t.ID, until: now.Add(s.ttl)}
	return t, nil
}

func (s *Selector) candidates(key string) []*tunnel.Tunnel {
	ready := s.pool.Ready()
	var out []*tunnel.Tunnel
	switch {
	case key == "" || key == "any":
		return ready
	case strings.HasPrefix(key, "tunnel:"):
		id, _ := strconv.Atoi(strings.TrimPrefix(key, "tunnel:"))
		for _, t := range ready {
			if t.ID == id {
				out = append(out, t)
			}
		}
	case strings.HasPrefix(key, "name:"):
		name := strings.TrimPrefix(key, "name:")
		for _, t := range ready {
			if strings.EqualFold(t.Name(), name) {
				out = append(out, t)
			}
		}
	case len(key) == 2:
		for _, t := range ready {
			if strings.EqualFold(t.Country(), key) {
				out = append(out, t)
			}
		}
	}
	return out
}
