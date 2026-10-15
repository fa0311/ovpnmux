package proxy

import (
	"context"
	"fmt"
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

func start(t *testing.T, password string) (addr string, beta *tunnel.Tunnel) {
	t.Helper()
	alpha, beta := tunnel.NewReady("alpha", &routing.Dialer{}), tunnel.NewReady("beta", &routing.Dialer{})
	srv := &Server{Password: password, Lookup: func(name string) (*tunnel.Tunnel, error) {
		switch name {
		case "alpha":
			return alpha, nil
		case "beta":
			return beta, nil
		}
		return nil, fmt.Errorf("no profile %q", name)
	}}
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
			return addr, beta
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
	addr, beta := start(t, "secret")
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello") }))
	defer be.Close()

	if got, err := get(t, addr, &proxy.Auth{User: "beta", Password: "secret"}, be.URL); err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
	time.Sleep(50 * time.Millisecond)
	if beta.Bytes.Load() == 0 {
		t.Error("bytes should be counted on beta")
	}
	if _, err := get(t, addr, &proxy.Auth{User: "beta", Password: "wrong"}, be.URL); err == nil {
		t.Error("wrong password should fail")
	}
	if _, err := get(t, addr, &proxy.Auth{User: "gamma", Password: "secret"}, be.URL); err == nil {
		t.Error("unknown profile should fail")
	}
	if _, err := get(t, addr, nil, be.URL); err == nil {
		t.Error("missing username should fail")
	}

	open, _ := start(t, "")
	if got, err := get(t, open, &proxy.Auth{User: "alpha", Password: "whatever"}, be.URL); err != nil || got != "hello" {
		t.Fatalf("no-password: got %q, %v", got, err)
	}
}
