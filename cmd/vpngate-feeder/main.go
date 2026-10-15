// vpngate-feeder turns the VPN Gate server list into *.ovpn profiles for vpngate-proxy.
// It is the only component that knows about VPN Gate.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"vpngate-proxy/internal/vpngate"
)

var remoteRe = regexp.MustCompile(`(?m)^remote\s+\S+\s+(\d+)`)

func main() {
	out := flag.String("out", "", "directory to write profiles into (required; the feeder owns it)")
	interval := flag.Duration("interval", 0, "re-run every interval; 0 = run once")
	urls := flag.String("urls", "http://www.vpngate.net/api/iphone/", "comma separated API URLs, tried in order")
	countries := flag.String("countries", "", "comma separated country codes to keep (empty = all)")
	minScore := flag.Int("min-score", 0, "minimum Score")
	maxSessions := flag.Int("max-sessions", 0, "skip servers with more sessions than this (0 = no limit)")
	useDDNS := flag.Bool("use-ddns", true, "replace the remote IP with HostName.opengw.net")
	keep := flag.Duration("keep", 6*time.Hour, "delete profiles not seen for this long")
	flag.Parse()
	if *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		if err := run(ctx, *out, strings.Split(*urls, ","), splitUpper(*countries), *minScore, *maxSessions, *useDDNS, *keep); err != nil {
			slog.Error("feeder run failed", "err", err)
			if *interval == 0 {
				os.Exit(1)
			}
		}
		if *interval == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(*interval):
		}
	}
}

func run(ctx context.Context, out string, urls, countries []string, minScore, maxSessions int, useDDNS bool, keep time.Duration) error {
	servers, src, err := fetch(ctx, urls)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	now := time.Now()
	written := 0
	for _, s := range servers {
		if len(countries) > 0 && !contains(countries, s.CountryShort) {
			continue
		}
		if s.Score < minScore || (maxSessions > 0 && s.NumSessions > maxSessions) {
			continue
		}
		cfg := s.Config
		if useDDNS {
			cfg = remoteRe.ReplaceAllString(cfg, "remote "+s.HostName+".opengw.net $1")
		}
		header := fmt.Sprintf("# tags: country=%s name=%s score=%d ping=%d speed=%d sessions=%d\n# source: %s at %s\n",
			s.CountryShort, s.HostName, s.Score, s.Ping, s.Speed, s.NumSessions, src, now.Format(time.RFC3339))
		if err := writeAtomic(filepath.Join(out, s.HostName+".ovpn"), []byte(header+cfg)); err != nil {
			return err
		}
		written++
	}
	pruned := 0
	entries, err := os.ReadDir(out)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ovpn") {
			continue
		}
		info, err := e.Info()
		if err == nil && info.ModTime().Before(now.Add(-keep)) {
			if os.Remove(filepath.Join(out, e.Name())) == nil {
				pruned++
			}
		}
	}
	slog.Info("feeder run", "source", src, "servers", len(servers), "written", written, "pruned", pruned)
	return nil
}

func fetch(ctx context.Context, urls []string) ([]vpngate.Server, string, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	var errs []error
	for _, u := range urls {
		u = strings.TrimSpace(u)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		servers, err := parseResponse(resp)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", u, err))
			continue
		}
		return servers, u, nil
	}
	return nil, "", errors.Join(errs...)
}

func parseResponse(resp *http.Response) ([]vpngate.Server, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	servers, err := vpngate.Parse(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, errors.New("empty server list")
	}
	return servers, nil
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func splitUpper(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToUpper(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
