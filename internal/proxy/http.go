package proxy

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// http handles one HTTP proxy request: CONNECT (tunnelled) or an absolute-URI request.
// The Proxy-Authorization Basic username is the selection key.
func (ss *session) http(ctx context.Context) {
	req, err := http.ReadRequest(ss.br)
	if err != nil {
		return
	}
	user, pass := basicAuth(req.Header.Get("Proxy-Authorization"))
	if !ss.srv.authOK(pass) {
		ss.respond(http.StatusProxyAuthRequired, "Proxy-Authenticate: Basic realm=\"ovpnmux\"\r\n")
		return
	}
	if req.Method == http.MethodConnect {
		hostport := req.Host
		if _, _, err := net.SplitHostPort(hostport); err != nil {
			hostport = net.JoinHostPort(hostport, "443")
		}
		target, t, err := ss.open(ctx, user, hostport)
		if err != nil {
			slog.Debug("http connect failed", "client", ss.clientIP, "target", hostport, "err", err)
			ss.respond(http.StatusBadGateway, "")
			return
		}
		defer target.Close()
		if _, err := ss.conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			return
		}
		ss.splice(target, t)
		return
	}
	if req.URL.Host == "" {
		ss.respond(http.StatusBadRequest, "")
		return
	}
	t, err := ss.srv.Selector.Select(ss.key(user), ss.clientIP)
	if err != nil {
		ss.respond(http.StatusBadGateway, "")
		return
	}
	tr := &http.Transport{DialContext: t.Dialer().DialContext, DisableKeepAlives: true}
	req.RequestURI = ""
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("Proxy-Connection")
	req.Close = true
	resp, err := tr.RoundTrip(req.WithContext(ctx))
	if err != nil {
		slog.Debug("http request failed", "client", ss.clientIP, "url", req.URL.String(), "err", err)
		ss.respond(http.StatusBadGateway, "")
		return
	}
	defer resp.Body.Close()
	resp.Close = true
	t.Conns.Add(1)
	defer t.Conns.Add(-1)
	_ = ss.conn.SetDeadline(noDeadline)
	if err := resp.Write(ss.conn); err == nil {
		t.Bytes.Add(resp.ContentLength)
	}
}

func (ss *session) respond(code int, extraHeaders string) {
	fmt.Fprintf(ss.conn, "HTTP/1.1 %d %s\r\n%sContent-Length: 0\r\nConnection: close\r\n\r\n", code, http.StatusText(code), extraHeaders)
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
