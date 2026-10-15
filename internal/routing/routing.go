// Package routing binds a tunnel to a routing table and provides a dialer whose
// sockets leave through that tunnel (fwmark policy routing, no network namespaces).
package routing

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// UpInfo is what openvpn reports (through the `_up` subcommand) once the tunnel is up.
type UpInfo struct {
	Dev     string   `json:"dev"`
	Local   string   `json:"local"`
	Remote  string   `json:"remote,omitempty"`  // p2p peer (net30 topology)
	Netmask string   `json:"netmask,omitempty"` // set for subnet topology
	Gateway string   `json:"gateway,omitempty"` // route_vpn_gateway
	MTU     int      `json:"mtu,omitempty"`
	DNS     []string `json:"dns,omitempty"` // pushed dhcp-option DNS
}

func ip(args ...string) error {
	out, err := exec.Command("ip", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Setup addresses the tun device and routes traffic marked with `mark` through it.
// Table `table` gets a default route via the tunnel plus an unreachable fallback,
// so a dead tunnel fails closed instead of leaking through the main table.
func Setup(info UpInfo, table, mark int) error {
	Teardown(table, mark)
	t, m := strconv.Itoa(table), strconv.Itoa(mark)
	link := []string{"link", "set", "dev", info.Dev, "up"}
	if info.MTU > 0 {
		link = append(link, "mtu", strconv.Itoa(info.MTU))
	}
	if err := ip(link...); err != nil {
		return err
	}
	if info.Remote != "" && info.Netmask == "" {
		if err := ip("addr", "add", info.Local, "peer", info.Remote+"/32", "dev", info.Dev); err != nil {
			return err
		}
	} else if err := ip("addr", "add", info.Local+"/32", "dev", info.Dev); err != nil {
		return err
	}
	gw := info.Gateway
	if gw == "" {
		gw = info.Remote
	}
	route := []string{"route", "add", "default", "dev", info.Dev, "table", t}
	if gw != "" {
		route = []string{"route", "add", "default", "via", gw, "dev", info.Dev, "onlink", "table", t}
	}
	if err := ip(route...); err != nil {
		return err
	}
	if err := ip("route", "add", "unreachable", "default", "metric", "1000", "table", t); err != nil {
		return err
	}
	if err := ip("rule", "add", "fwmark", m, "lookup", t, "priority", t); err != nil {
		return err
	}
	// Best effort; inside docker /proc/sys is read-only and compose sets this via sysctls.
	_ = os.WriteFile("/proc/sys/net/ipv4/conf/"+info.Dev+"/rp_filter", []byte("0"), 0)
	return nil
}

// Teardown removes what Setup added. Errors are ignored (nothing may exist yet).
func Teardown(table, mark int) {
	t, m := strconv.Itoa(table), strconv.Itoa(mark)
	for i := 0; i < 8; i++ {
		if ip("rule", "del", "fwmark", m, "lookup", t) != nil {
			break
		}
	}
	_ = ip("route", "flush", "table", t)
}

// Dialer dials IPv4 TCP through the tunnel whose fwmark is Mark. Mark 0 dials normally.
// Name resolution also goes through the tunnel, to the given DNS servers.
type Dialer struct {
	Mark int
	DNS  []string
	next atomic.Uint32
}

func (d *Dialer) control(_, _ string, c syscall.RawConn) error {
	if d.Mark == 0 {
		return nil
	}
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, d.Mark)
	})
	if err != nil {
		return err
	}
	return serr
}

func (d *Dialer) resolver() *net.Resolver {
	if len(d.DNS) == 0 {
		return nil
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			server := d.DNS[int(d.next.Add(1)-1)%len(d.DNS)]
			nd := net.Dialer{Timeout: 5 * time.Second, Control: d.control}
			return nd.DialContext(ctx, strings.TrimSuffix(network, "4")+"4", net.JoinHostPort(server, "53"))
		},
	}
}

func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	nd := net.Dialer{Timeout: 15 * time.Second, Control: d.control, Resolver: d.resolver()}
	return nd.DialContext(ctx, strings.TrimSuffix(network, "4")+"4", addr)
}

func (d *Dialer) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: d.DialContext, DisableKeepAlives: true},
	}
}
