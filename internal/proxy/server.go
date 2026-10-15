// Package proxy serves SOCKS5 and HTTP (CONNECT and plain) on one port and
// sends each connection out through a tunnel chosen by the Selector.
package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"vpngate-proxy/internal/tunnel"
)

type Server struct {
	Selector     *Selector
	AuthRequired bool
	Password     string
}

// ListenAndServe accepts connections on addr until ctx is done. A non-empty
// fixedKey overrides the selection key sent by the client (per-port selection).
func (s *Server) ListenAndServe(ctx context.Context, addr, fixedKey string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	slog.Info("proxy listening", "addr", addr, "select", fixedKey)
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
		go s.handle(ctx, c, fixedKey)
	}
}

type session struct {
	srv      *Server
	conn     net.Conn
	br       *bufio.Reader
	fixedKey string
	clientIP string
}

func (s *Server) handle(ctx context.Context, c net.Conn, fixedKey string) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second)) // handshake only; cleared before splicing
	br := bufio.NewReader(c)
	b, err := br.Peek(1)
	if err != nil {
		return
	}
	host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	ss := &session{srv: s, conn: c, br: br, fixedKey: fixedKey, clientIP: host}
	if b[0] == 0x05 {
		ss.socks5(ctx)
	} else {
		ss.http(ctx)
	}
}

func (s *Server) authOK(password string) bool {
	return !s.AuthRequired || password == s.Password
}

func (ss *session) key(user string) string {
	if ss.fixedKey != "" {
		return ss.fixedKey
	}
	return user
}

// open picks a tunnel for the key and dials the target through it.
func (ss *session) open(ctx context.Context, user, hostport string) (net.Conn, *tunnel.Tunnel, error) {
	t, err := ss.srv.Selector.Select(ss.key(user), ss.clientIP)
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
