// ovpnmux keeps several OpenVPN tunnels up and exposes them as one HTTP CONNECT proxy.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sync/errgroup"

	"ovpnmux/internal/admin"
	"ovpnmux/internal/config"
	"ovpnmux/internal/proxy"
	"ovpnmux/internal/routing"
	"ovpnmux/internal/tunnel"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "_up" {
		os.Exit(up())
	}
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: ovpnmux serve [-config FILE] [-debug]")
		os.Exit(2)
	}
	os.Exit(serve(os.Args[2:]))
}

func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/ovpnmux/config.yaml", "config file")
	debug := fs.Bool("debug", false, "log at debug level (includes openvpn output)")
	fs.Parse(args)
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("config", "err", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	m, err := tunnel.New(cfg)
	if err != nil {
		slog.Error("profiles", "err", err)
		return 1
	}
	srv := &proxy.Server{Lookup: m.Lookup, Password: cfg.Password}
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return m.Run(ctx) })
	g.Go(func() error { return srv.ListenAndServe(ctx, cfg.Listen) })
	g.Go(func() error { return admin.ListenAndServe(ctx, cfg.AdminListen, m) })
	if err := g.Wait(); err != nil {
		slog.Error("exit", "err", err)
		return 1
	}
	return 0
}

// up is run by openvpn as its --up script. It reports the tunnel parameters to the daemon.
func up() int {
	path := os.Getenv("OVPNMUX_UP_FILE")
	if path == "" {
		fmt.Fprintln(os.Stderr, "OVPNMUX_UP_FILE not set")
		return 1
	}
	info := routing.UpInfo{
		Dev:     os.Getenv("dev"),
		Local:   os.Getenv("ifconfig_local"),
		Remote:  os.Getenv("ifconfig_remote"),
		Netmask: os.Getenv("ifconfig_netmask"),
		Gateway: os.Getenv("route_vpn_gateway"),
	}
	info.MTU, _ = strconv.Atoi(os.Getenv("tun_mtu"))
	for i := 1; ; i++ {
		opt := os.Getenv("foreign_option_" + strconv.Itoa(i))
		if opt == "" {
			break
		}
		if f := strings.Fields(opt); len(f) == 3 && f[0] == "dhcp-option" && f[1] == "DNS" {
			info.DNS = append(info.DNS, f[2])
		}
	}
	if info.Dev == "" || info.Local == "" {
		fmt.Fprintf(os.Stderr, "missing dev/ifconfig_local in environment: %+v\n", info)
		return 1
	}
	b, _ := json.Marshal(info)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.Rename(tmp, path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
