// Package proxy serves an HTTP CONNECT proxy; the Basic auth username names the tunnel to use.
package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"ovpnmux/internal/tunnel"
)

type Server struct {
	Lookup   func(name string) (*tunnel.Tunnel, error)
	Password string // empty accepts any password
}

func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	slog.Info("proxy listening", "addr", addr)
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("accept", "err", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go s.handle(ctx, c)
	}
}

// handle serves one CONNECT request and then tunnels bytes until either side closes.
func (s *Server) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second)) // request only; cleared before tunnelling
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method != http.MethodConnect {
		respond(c, http.StatusMethodNotAllowed, "Allow: CONNECT\r\n")
		return
	}
	name, password := basicAuth(req.Header.Get("Proxy-Authorization"))
	if name == "" || (s.Password != "" && password != s.Password) {
		respond(c, http.StatusProxyAuthRequired, "Proxy-Authenticate: Basic realm=\"ovpnmux\"\r\n")
		return
	}
	t, err := s.Lookup(name)
	if err != nil {
		slog.Debug("connect refused", "client", c.RemoteAddr(), "err", err)
		respond(c, http.StatusBadGateway, "")
		return
	}
	hostport := req.Host
	if _, _, err := net.SplitHostPort(hostport); err != nil {
		hostport = net.JoinHostPort(hostport, "443")
	}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	target, err := t.Dialer().DialContext(dctx, "tcp", hostport)
	cancel()
	if err != nil {
		slog.Debug("connect failed", "client", c.RemoteAddr(), "target", hostport, "profile", name, "err", err)
		respond(c, http.StatusBadGateway, "")
		return
	}
	defer target.Close()
	if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})

	t.Conns.Add(1)
	defer t.Conns.Add(-1)
	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(target, br)
		t.Bytes.Add(n)
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(c, target)
		t.Bytes.Add(n)
		done <- struct{}{}
	}()
	<-done
	target.Close()
	c.Close()
	<-done
}

func respond(w io.Writer, code int, extraHeaders string) {
	fmt.Fprintf(w, "HTTP/1.1 %d %s\r\n%sContent-Length: 0\r\nConnection: close\r\n\r\n", code, http.StatusText(code), extraHeaders)
}

func basicAuth(h string) (user, pass string) {
	scheme, cred, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Basic") {
		return "", ""
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cred))
	if err != nil {
		return "", ""
	}
	user, pass, _ = strings.Cut(string(b), ":")
	return user, pass
}
