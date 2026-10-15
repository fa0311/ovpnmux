package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

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

// get fetches u (https, so the client uses CONNECT) through the proxy with Basic auth user:pass.
func get(t *testing.T, proxyAddr, user, pass, u string) (string, error) {
	t.Helper()
	pu := &url.URL{Scheme: "http", Host: proxyAddr}
	if user != "" {
		pu.User = url.UserPassword(user, pass)
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy:             http.ProxyURL(pu),
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		DisableKeepAlives: true,
	}}
	resp, err := client.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func TestConnect(t *testing.T) {
	addr, beta := start(t, "secret")
	be := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello") }))
	defer be.Close()

	if got, err := get(t, addr, "beta", "secret", be.URL); err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
	time.Sleep(50 * time.Millisecond)
	if beta.Bytes.Load() == 0 {
		t.Error("bytes should be counted on beta")
	}
	if _, err := get(t, addr, "beta", "wrong", be.URL); err == nil {
		t.Error("wrong password should fail")
	}
	if _, err := get(t, addr, "gamma", "secret", be.URL); err == nil {
		t.Error("unknown profile should fail")
	}
	if _, err := get(t, addr, "", "", be.URL); err == nil {
		t.Error("missing auth should fail")
	}

	// Anything but CONNECT is refused.
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n")
	buf := make([]byte, 64)
	n, _ := c.Read(buf)
	if want := "HTTP/1.1 405 "; string(buf[:len(want)]) != want {
		t.Errorf("plain request: got %q", buf[:n])
	}

	open, _ := start(t, "")
	if got, err := get(t, open, "alpha", "whatever", be.URL); err != nil || got != "hello" {
		t.Fatalf("no-password: got %q, %v", got, err)
	}
}
