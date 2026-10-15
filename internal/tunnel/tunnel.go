// Package tunnel runs one openvpn process per profile and keeps it healthy.
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
	ID      int
	Dev     string
	Mark    int
	Table   int
	Profile profile.Profile

	Conns atomic.Int64
	Bytes atomic.Int64

	mu      sync.Mutex
	dialer  *routing.Dialer
	state   State
	since   time.Time
	latency time.Duration
	lastErr string
}

type Status struct {
	Profile   string `json:"profile"`
	Dev       string `json:"dev"`
	State     State  `json:"state"`
	Remote    string `json:"remote"`
	Since     string `json:"since"`
	Uptime    string `json:"uptime"`
	Latency   string `json:"latency"`
	Conns     int64  `json:"conns"`
	Bytes     int64  `json:"bytes"`
	LastError string `json:"last_error,omitempty"`
}

// NewReady returns a tunnel that is already ready and dials with d. Used by tests.
func NewReady(name string, d *routing.Dialer) *Tunnel {
	return &Tunnel{Dev: "test", dialer: d, state: StateReady, since: time.Now(),
		Profile: profile.Profile{ID: name, Name: name}}
}

func (t *Tunnel) set(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f()
}

// Dialer returns the tunnel's dialer, or nil while it is not ready.
func (t *Tunnel) Dialer() *routing.Dialer {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != StateReady {
		return nil
	}
	return t.dialer
}

func (t *Tunnel) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := Status{Profile: t.Profile.ID, Dev: t.Dev, State: t.state, Remote: t.Profile.Remote,
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
