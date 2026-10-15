// Package tunnel runs openvpn processes and keeps a pool of healthy tunnels.
package tunnel

import (
	"sync"
	"sync/atomic"
	"time"

	"ovpnmux/internal/profile"
	"ovpnmux/internal/routing"
)

type State string

const (
	StateConnecting State = "connecting"
	StateReady      State = "ready"
	StateDead       State = "dead"
)

type Tunnel struct {
	ID    int
	Dev   string
	Mark  int
	Table int

	Conns atomic.Int64
	Bytes atomic.Int64

	mu      sync.Mutex
	dialer  *routing.Dialer
	profile profile.Profile
	state   State
	since   time.Time
	latency time.Duration
	lastErr string
}

type Status struct {
	ID        int    `json:"id"`
	Dev       string `json:"dev"`
	State     State  `json:"state"`
	Profile   string `json:"profile"`
	Remote    string `json:"remote"`
	Since     string `json:"since"`
	Uptime    string `json:"uptime"`
	Latency   string `json:"latency"`
	Conns     int64  `json:"conns"`
	Bytes     int64  `json:"bytes"`
	LastError string `json:"last_error,omitempty"`
}

// NewReady returns a tunnel that is already ready and dials with d. Used by tests.
func NewReady(id int, d *routing.Dialer) *Tunnel {
	return &Tunnel{ID: id, Dev: "test", dialer: d, state: StateReady, since: time.Now()}
}

func (t *Tunnel) set(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f()
}

func (t *Tunnel) Dialer() *routing.Dialer {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dialer
}

func (t *Tunnel) Ready() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state == StateReady
}

func (t *Tunnel) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := Status{ID: t.ID, Dev: t.Dev, State: t.state, Profile: t.profile.ID, Remote: t.profile.Remote,
		Conns: t.Conns.Load(), Bytes: t.Bytes.Load(), LastError: t.lastErr}
	if !t.since.IsZero() {
		s.Since = t.since.Format(time.RFC3339)
		s.Uptime = time.Since(t.since).Round(time.Second).String()
	}
	if t.latency > 0 {
		s.Latency = t.latency.Round(time.Millisecond).String()
	}
	return s
}
