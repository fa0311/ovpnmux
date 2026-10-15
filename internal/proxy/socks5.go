package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"strconv"
)

// socks5 implements RFC 1928 CONNECT with optional RFC 1929 username/password.
// The username selects the tunnel: empty or "any" for any, or a profile name.
func (ss *session) socks5(ctx context.Context) {
	br, c := ss.br, ss.conn
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil || hdr[0] != 5 {
		return
	}
	methods := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	var method byte
	switch {
	case bytes.IndexByte(methods, 0x02) >= 0:
		method = 0x02
	case bytes.IndexByte(methods, 0x00) >= 0 && ss.srv.Password == "":
		method = 0x00
	default:
		c.Write([]byte{5, 0xFF})
		return
	}
	c.Write([]byte{5, method})

	key := ""
	if method == 0x02 {
		v := make([]byte, 2) // version, username length
		if _, err := io.ReadFull(br, v); err != nil {
			return
		}
		u := make([]byte, int(v[1]))
		if _, err := io.ReadFull(br, u); err != nil {
			return
		}
		pl := make([]byte, 1)
		if _, err := io.ReadFull(br, pl); err != nil {
			return
		}
		p := make([]byte, int(pl[0]))
		if _, err := io.ReadFull(br, p); err != nil {
			return
		}
		if ss.srv.Password != "" && string(p) != ss.srv.Password {
			c.Write([]byte{1, 1})
			return
		}
		c.Write([]byte{1, 0})
		key = string(u)
	}

	req := make([]byte, 4) // version, command, reserved, address type
	if _, err := io.ReadFull(br, req); err != nil || req[0] != 5 {
		return
	}
	var host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3:
		l := make([]byte, 1)
		if _, err := io.ReadFull(br, l); err != nil {
			return
		}
		b := make([]byte, int(l[0]))
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = string(b)
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	default:
		reply(c, 8)
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(br, pb); err != nil {
		return
	}
	if req[1] != 1 { // only CONNECT
		reply(c, 7)
		return
	}
	hostport := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb))))
	target, t, err := ss.open(ctx, key, hostport)
	if err != nil {
		slog.Debug("connect failed", "client", ss.clientIP, "target", hostport, "err", err)
		reply(c, 4)
		return
	}
	defer target.Close()
	reply(c, 0)
	ss.splice(target, t)
}

func reply(w io.Writer, code byte) {
	w.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
}
