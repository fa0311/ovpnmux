// Package proxy serves SOCKS5; the username names the tunnel to use.
package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	slog.Info("socks5 listening", "addr", addr)
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

type session struct {
	srv      *Server
	conn     net.Conn
	br       *bufio.Reader
	clientIP string
}

func (s *Server) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second)) // handshake only; cleared before splicing
	host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	ss := &session{srv: s, conn: c, br: bufio.NewReader(c), clientIP: host}
	ss.socks5(ctx)
}

// open dials the target through the named tunnel.
func (ss *session) open(ctx context.Context, name, hostport string) (net.Conn, *tunnel.Tunnel, error) {
	t, err := ss.srv.Lookup(name)
	if err != nil {
		return nil, nil, err
	}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, err := t.Dialer().DialContext(dctx, "tcp", hostport)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s via tunnel %d: %w", hostport, t.ID, err)
	}
	return c, t, nil
}

// splice copies both directions until either side finishes, then closes both.
func (ss *session) splice(target net.Conn, t *tunnel.Tunnel) {
	_ = ss.conn.SetDeadline(time.Time{})
	t.Conns.Add(1)
	defer t.Conns.Add(-1)
	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(target, ss.br)
		t.Bytes.Add(n)
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(ss.conn, target)
		t.Bytes.Add(n)
		done <- struct{}{}
	}()
	<-done
	target.Close()
	ss.conn.Close()
	<-done
}
