package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"vpngate-proxy/internal/routing"
	"vpngate-proxy/internal/tunnel"
)

type fakePool []*tunnel.Tunnel

func (p fakePool) Ready() []*tunnel.Tunnel { return p }

func start(t *testing.T, authRequired bool) (addr string, pool fakePool) {
	t.Helper()
	pool = fakePool{
		tunnel.NewReady(1, "jp-one", "JP", &routing.Dialer{}),
		tunnel.NewReady(2, "kr-one", "KR", &routing.Dialer{}),
	}
	srv := &Server{Selector: NewSelector(pool, "rr", time.Minute), AuthRequired: authRequired, Password: "secret"}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	addr = ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.ListenAndServe(ctx, addr, "")
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

func backend(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello "+r.URL.Path)
	}))
	t.Cleanup(s.Close)
	return s
}

func get(t *testing.T, client *http.Client, u string) string {
	t.Helper()
	resp, err := client.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	return string(b)
}

func TestSocks5(t *testing.T) {
	addr, pool := start(t, true)
	be := backend(t)
	d, err := proxy.SOCKS5("tcp", addr, &proxy.Auth{User: "kr", Password: "secret"}, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Dial: d.Dial}}
	if got := get(t, client, be.URL+"/socks"); got != "hello /socks" {
		t.Errorf("got %q", got)
	}
	if pool[0].Bytes.Load() != 0 || pool[0].Conns.Load() != 0 {
		t.Error("JP tunnel should not have been used")
	}

	bad, _ := proxy.SOCKS5("tcp", addr, &proxy.Auth{User: "kr", Password: "wrong"}, proxy.Direct)
	if _, err := bad.Dial("tcp", "127.0.0.1:1"); err == nil {
		t.Error("wrong password should fail")
	}
	unknown, _ := proxy.SOCKS5("tcp", addr, &proxy.Auth{User: "zz", Password: "secret"}, proxy.Direct)
	if _, err := unknown.Dial("tcp", "127.0.0.1:1"); err == nil {
		t.Error("unknown country should fail")
	}
}

func TestSocks5NoAuth(t *testing.T) {
	addr, _ := start(t, false)
	be := backend(t)
	d, _ := proxy.SOCKS5("tcp", addr, nil, proxy.Direct)
	client := &http.Client{Transport: &http.Transport{Dial: d.Dial}}
	if got := get(t, client, be.URL+"/noauth"); got != "hello /noauth" {
		t.Errorf("got %q", got)
	}
}

func TestHTTPConnectAndPlain(t *testing.T) {
	addr, pool := start(t, true)
	be := backend(t)
	pu, _ := url.Parse("http://jp:secret@" + addr)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	if got := get(t, client, be.URL+"/plain"); got != "hello /plain" {
		t.Errorf("plain: got %q", got)
	}
	// CONNECT: Go only uses CONNECT for https, so drive it by hand.
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	beHost := be.Listener.Addr().String()
	io.WriteString(c, "CONNECT "+beHost+" HTTP/1.1\r\nHost: "+beHost+"\r\nProxy-Authorization: Basic anA6c2VjcmV0\r\n\r\n")
	buf := make([]byte, 64)
	n, _ := c.Read(buf)
	if string(buf[:n]) != "HTTP/1.1 200 Connection Established\r\n\r\n" {
		t.Fatalf("connect reply: %q", buf[:n])
	}
	io.WriteString(c, "GET /tunnelled HTTP/1.0\r\n\r\n")
	b, _ := io.ReadAll(c)
	if string(b[len(b)-len("hello /tunnelled"):]) != "hello /tunnelled" {
		t.Errorf("tunnelled: %q", b)
	}
	if pool[1].Bytes.Load() != 0 || pool[1].Conns.Load() != 0 {
		t.Error("KR tunnel should not have been used")
	}

	noauth := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: addr})}}
	resp, err := noauth.Get(be.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("without auth: status %d", resp.StatusCode)
	}
}

func TestSelectorSticky(t *testing.T) {
	pool := fakePool{tunnel.NewReady(1, "a", "JP", nil), tunnel.NewReady(2, "b", "JP", nil)}
	s := NewSelector(pool, "sticky", time.Minute)
	first, _ := s.Select("jp", "10.0.0.1")
	for i := 0; i < 5; i++ {
		if t2, _ := s.Select("JP", "10.0.0.1"); t2 != first {
			t.Fatal("sticky selection changed")
		}
	}
	if t3, _ := s.Select("tunnel:2", "10.0.0.1"); t3.ID != 2 {
		t.Error("tunnel:2 should select tunnel 2")
	}
	if t4, _ := s.Select("name:B", ""); t4.ID != 2 {
		t.Error("name:B should select tunnel 2")
	}
	if _, err := s.Select("us", ""); err == nil {
		t.Error("no US tunnel should error")
	}
}
