// Package profile loads OpenVPN client profiles (*.ovpn) from a directory.
//
// A profile may carry metadata in a comment line:
//
//	# tags: country=JP name=public-vpn-251 score=3137099
//
// Profiles can come from the network (feeder), so directives that execute
// programs or otherwise reach outside the tunnel are rejected, and the
// directives the daemon sets itself are dropped.
package profile

import (
	"bufio"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Profile struct {
	ID      string // path relative to the profiles dir, without .ovpn
	Path    string
	Name    string
	Country string // ISO 3166-1 alpha-2, upper case, or ""
	Score   int
	Remote  string // "host port [proto]" of the first remote, for display
	Tags    map[string]string
	Body    string // profile text with controlled directives removed
}

var dangerous = map[string]bool{
	"up": true, "down": true, "plugin": true, "script-security": true, "route-up": true,
	"route-pre-down": true, "ipchange": true, "tls-verify": true, "client-connect": true,
	"client-disconnect": true, "learn-address": true, "auth-user-pass-verify": true,
	"management": true, "cd": true, "chroot": true, "setenv": true, "setenv-safe": true,
	"up-delay": true, "down-pre": true, "daemon": true, "log": true, "log-append": true,
	"writepid": true, "status": true,
}

var overridden = map[string]bool{
	"dev": true, "dev-type": true, "ifconfig-noexec": true, "route-noexec": true,
	"pull-filter": true, "verb": true, "connect-timeout": true, "connect-retry-max": true,
	"remap-usr1": true, "auth-nocache": true, "redirect-gateway": true, "route": true,
	"dhcp-option": true, "nobind": true,
}

// Load reads every *.ovpn under dir. Unreadable or rejected files are logged and skipped.
func Load(dir string) ([]Profile, error) {
	var out []Profile
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".ovpn") {
			return nil
		}
		p, err := Parse(path)
		if err != nil {
			slog.Warn("skip profile", "path", path, "err", err)
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		p.ID = strings.TrimSuffix(filepath.ToSlash(rel), ".ovpn")
		if p.Name == "" {
			p.Name = filepath.Base(p.ID)
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func Parse(path string) (Profile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	p, err := parse(string(b))
	p.Path = path
	return p, err
}

func parse(text string) (Profile, error) {
	p := Profile{Tags: map[string]string{}}
	var body strings.Builder
	inline := false // inside <ca> ... </ca> and friends
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "# tags:") {
			for _, kv := range strings.Fields(strings.TrimPrefix(trim, "# tags:")) {
				if k, v, ok := strings.Cut(kv, "="); ok {
					p.Tags[k] = v
				}
			}
			continue
		}
		if inline {
			body.WriteString(line + "\n")
			if strings.HasPrefix(trim, "</") {
				inline = false
			}
			continue
		}
		if trim == "" || trim[0] == '#' || trim[0] == ';' {
			continue
		}
		if trim[0] == '<' {
			inline = true
			body.WriteString(trim + "\n")
			continue
		}
		fields := strings.Fields(trim)
		key := strings.TrimLeft(fields[0], "-")
		if dangerous[key] {
			return Profile{}, fmt.Errorf("directive %q is not allowed", key)
		}
		if overridden[key] {
			continue
		}
		if key == "remote" && p.Remote == "" && len(fields) >= 2 {
			p.Remote = strings.Join(fields[1:], " ")
		}
		body.WriteString(trim + "\n")
	}
	if err := sc.Err(); err != nil {
		return Profile{}, err
	}
	if p.Remote == "" {
		return Profile{}, fmt.Errorf("no remote directive")
	}
	p.Body = body.String()
	p.Name = p.Tags["name"]
	p.Country = strings.ToUpper(p.Tags["country"])
	p.Score, _ = strconv.Atoi(p.Tags["score"])
	return p, nil
}
