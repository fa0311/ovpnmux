package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"ovpnmux/internal/config"
	"ovpnmux/internal/profile"
	"ovpnmux/internal/routing"
)

type Manager struct {
	cfg     *config.Config
	tunnels []*Tunnel

	mu       sync.Mutex
	inUse    map[string]bool
	cooldown map[string]time.Time
}

func New(cfg *config.Config) *Manager {
	m := &Manager{cfg: cfg, inUse: map[string]bool{}, cooldown: map[string]time.Time{}}
	for id := 1; id <= cfg.Tunnels; id++ {
		m.tunnels = append(m.tunnels, &Tunnel{ID: id, Dev: fmt.Sprintf("tun%d", id),
			Mark: cfg.TableBase + id, Table: cfg.TableBase + id, state: StateDead})
	}
	return m
}

// Run keeps every tunnel slot busy until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	if err := os.MkdirAll(m.cfg.DataDir, 0o755); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, t := range m.tunnels {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.slotLoop(ctx, t)
		}()
	}
	wg.Wait()
	return nil
}

func (m *Manager) Ready() []*Tunnel {
	var out []*Tunnel
	for _, t := range m.tunnels {
		if t.Ready() {
			out = append(out, t)
		}
	}
	return out
}

func (m *Manager) Status() []Status {
	out := make([]Status, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		out = append(out, t.Status())
	}
	return out
}

func (m *Manager) slotLoop(ctx context.Context, t *Tunnel) {
	for ctx.Err() == nil {
		p, ok := m.pick()
		if !ok {
			slog.Info("no candidate profile, waiting", "tunnel", t.ID)
			sleep(ctx, 30*time.Second)
			continue
		}
		start := time.Now()
		err := m.run(ctx, t, p)
		m.mu.Lock()
		delete(m.inUse, p.ID)
		m.cooldown[p.ID] = time.Now().Add(m.cfg.Cooldown)
		m.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		slog.Warn("tunnel ended", "tunnel", t.ID, "profile", p.ID, "after", time.Since(start).Round(time.Second), "err", err)
		sleep(ctx, 2*time.Second)
	}
}

// pick returns a random profile that is neither in use nor cooling down.
func (m *Manager) pick() (profile.Profile, bool) {
	profiles, err := profile.Load(m.cfg.ProfilesDir)
	if err != nil {
		slog.Error("load profiles", "dir", m.cfg.ProfilesDir, "err", err)
		return profile.Profile{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var cands []profile.Profile
	for _, p := range profiles {
		if !m.inUse[p.ID] && !m.cooldown[p.ID].After(now) {
			cands = append(cands, p)
		}
	}
	if len(cands) == 0 {
		return profile.Profile{}, false
	}
	p := cands[rand.IntN(len(cands))]
	m.inUse[p.ID] = true
	return p, true
}

// run drives one openvpn process from start to exit and returns why it ended.
func (m *Manager) run(ctx context.Context, t *Tunnel, p profile.Profile) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	t.set(func() {
		t.profile, t.state, t.since, t.latency, t.lastErr, t.dialer = p, StateConnecting, time.Now(), 0, "", nil
	})
	defer t.set(func() { t.state = StateDead })
	defer routing.Teardown(t.Table, t.Mark)

	upFile := filepath.Join(m.cfg.DataDir, t.Dev+".up.json")
	_ = os.Remove(upFile)
	cfgPath := filepath.Join(m.cfg.DataDir, t.Dev+".ovpn")
	if err := os.WriteFile(cfgPath, []byte(p.Body+directives(t.Dev, upFile)), 0o600); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, m.cfg.OpenVPNBin, "--config", cfgPath)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	go logOutput(t, pr)
	if err := cmd.Start(); err != nil {
		pw.Close()
		return fmt.Errorf("start openvpn: %w", err)
	}
	done := make(chan struct{})
	var exitErr error
	go func() {
		exitErr = cmd.Wait()
		pw.Close()
		close(done)
	}()
	defer func() { cancel(); <-done }()

	info, err := waitUp(ctx, upFile, done, 60*time.Second)
	if err != nil {
		if exitErr != nil {
			return fmt.Errorf("%w (openvpn: %v)", err, exitErr)
		}
		return err
	}
	if err := routing.Setup(info, t.Table, t.Mark); err != nil {
		return fmt.Errorf("routing: %w", err)
	}
	dns := info.DNS
	if len(dns) == 0 {
		dns = m.cfg.FallbackDNS
	}
	d := &routing.Dialer{Mark: t.Mark, DNS: dns}
	latency, err := m.health(ctx, d)
	if err != nil {
		return fmt.Errorf("first health check: %w", err)
	}
	t.set(func() { t.dialer, t.state, t.latency = d, StateReady, latency })
	slog.Info("tunnel ready", "tunnel", t.ID, "profile", p.ID, "latency", latency.Round(time.Millisecond))

	ticker := time.NewTicker(m.cfg.HealthInterval)
	defer ticker.Stop()
	fails := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return fmt.Errorf("openvpn exited: %v", exitErr)
		case <-ticker.C:
			lat, err := m.health(ctx, d)
			if err != nil {
				fails++
				slog.Warn("health check failed", "tunnel", t.ID, "fails", fails, "err", err)
				if fails >= 3 {
					return fmt.Errorf("health check failed %d times: %w", fails, err)
				}
				continue
			}
			fails = 0
			t.set(func() { t.latency = lat })
		}
	}
}

func directives(dev, upFile string) string {
	self, err := os.Executable()
	if err != nil {
		self = "ovpnmux"
	}
	return fmt.Sprintf(`
# --- added by ovpnmux ---
dev %s
dev-type tun
ifconfig-noexec
route-noexec
pull-filter ignore "redirect-gateway"
script-security 2
setenv OVPNMUX_UP_FILE %s
up "%s _up"
connect-timeout 15
connect-retry-max 1
remap-usr1 SIGTERM
auth-nocache
nobind
verb 3
`, dev, upFile, self)
}

func logOutput(t *Tunnel, r io.Reader) {
	var line strings.Builder
	b := make([]byte, 4096)
	for {
		n, err := r.Read(b)
		for _, c := range b[:n] {
			if c != '\n' {
				line.WriteByte(c)
				continue
			}
			s := line.String()
			line.Reset()
			slog.Debug("openvpn", "tunnel", t.ID, "msg", s)
			if strings.Contains(s, "ERROR") || strings.Contains(s, "AUTH_FAILED") || strings.Contains(s, "TLS Error") || strings.Contains(s, "fatal error") {
				t.set(func() { t.lastErr = s })
			}
		}
		if err != nil {
			return
		}
	}
}

func waitUp(ctx context.Context, path string, done <-chan struct{}, timeout time.Duration) (routing.UpInfo, error) {
	var info routing.UpInfo
	deadline := time.After(timeout)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return info, ctx.Err()
		case <-done:
			return info, errors.New("openvpn exited before the tunnel came up")
		case <-deadline:
			return info, fmt.Errorf("tunnel did not come up within %s", timeout)
		case <-tick.C:
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(b, &info); err != nil {
				return info, fmt.Errorf("parse %s: %w", path, err)
			}
			return info, nil
		}
	}
}

func (m *Manager) health(ctx context.Context, d *routing.Dialer) (time.Duration, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.HealthURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := d.HTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	return time.Since(start), nil
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
