package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"ovpnmux/internal/routing"
	"ovpnmux/internal/tunnel"
)

type fakePool []*tunnel.Tunnel

func (p fakePool) Ready() []*tunnel.Tunnel { return p }

func start(t *testing.T, password string) (addr string, pool fakePool) {
	t.Helper()
	pool = fakePool{tunnel.NewReady(1, "alpha", &routing.Dialer{}), tunnel.NewReady(2, "beta", &routing.Dialer{})}
	srv := &Server{Selector: NewSelector(pool, 0), Password: password}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	addr = ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.ListenAndServe(ctx, addr)
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return addr, pool
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("proxy did not start")
	return
}

func get(t *testing.T, socksAddr string, auth *proxy.Auth, u string) (string, error) {
	t.Helper()
	d, err := proxy.SOCKS5("tcp", socksAddr, auth, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Dial: d.Dial, DisableKeepAlives: true}}
	resp, err := client.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func TestSocks5(t *testing.T) {
	addr, pool := start(t, "secret")
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello") }))
	defer be.Close()

	if got, err := get(t, addr, &proxy.Auth{User: "beta", Password: "secret"}, be.URL); err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
	if pool[0].Conns.Load() != 0 && pool[0].Bytes.Load() != 0 {
		t.Error("tunnel 1 should not have been used")
	}
	if _, err := get(t, addr, &proxy.Auth{User: "any", Password: "wrong"}, be.URL); err == nil {
		t.Error("wrong password should fail")
	}
	if _, err := get(t, addr, &proxy.Auth{User: "gamma", Password: "secret"}, be.URL); err == nil {
		t.Error("unknown profile should fail")
	}
	if _, err := get(t, addr, nil, be.URL); err == nil {
		t.Error("missing auth should fail")
	}

	open, _ := start(t, "")
	if got, err := get(t, open, nil, be.URL); err != nil || got != "hello" {
		t.Fatalf("no-auth: got %q, %v", got, err)
	}
}

func TestSelectorSticky(t *testing.T) {
	pool := fakePool{tunnel.NewReady(1, "alpha", nil), tunnel.NewReady(2, "beta", nil)}
	s := NewSelector(pool, time.Minute)
	first, _ := s.Select("", "10.0.0.1")
	for i := 0; i < 5; i++ {
		if t2, _ := s.Select("any", "10.0.0.1"); t2 != first {
			t.Fatal("sticky selection changed")
		}
	}
	if t3, _ := s.Select("Beta", "10.0.0.1"); t3.ID != 2 {
		t.Error("profile name should select tunnel 2")
	}
	if _, err := s.Select("gamma", ""); err == nil {
		t.Error("unknown profile should error")
	}
}
