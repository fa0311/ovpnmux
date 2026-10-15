package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `client
dev tun
proto tcp
remote 203.0.113.10 443
;http-proxy-retry
cipher AES-128-CBC
verb 3
<ca>
-----BEGIN CERTIFICATE-----
# not a comment inside a block
-----END CERTIFICATE-----
</ca>
`

func TestParse(t *testing.T) {
	p, err := parse(sample)
	if err != nil {
		t.Fatal(err)
	}
	if p.Remote != "203.0.113.10 443" {
		t.Errorf("remote = %q", p.Remote)
	}
	for _, bad := range []string{"dev tun", "verb 3", ";http-proxy"} {
		if strings.Contains(p.Body, bad) {
			t.Errorf("body still contains %q:\n%s", bad, p.Body)
		}
	}
	if !strings.Contains(p.Body, "# not a comment inside a block") {
		t.Errorf("inline block was mangled:\n%s", p.Body)
	}
	for _, d := range []string{"up /bin/sh", "plugin foo.so", "script-security 2", "--management 127.0.0.1 7505"} {
		if _, err := parse("remote a 1\n" + d + "\n"); err == nil {
			t.Errorf("%q should be rejected", d)
		}
	}
	if _, err := parse("client\n"); err == nil {
		t.Error("profile without remote should be rejected")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "a.ovpn"), []byte(sample), 0o644)
	os.WriteFile(filepath.Join(dir, "b.ovpn"), []byte("remote x 1\nup /bin/sh\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "c.ovpn"), []byte("remote y 2\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.txt"), []byte("ignored"), 0o644)
	ps, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].ID != "c" || ps[1].ID != "sub/a" || ps[1].Name != "a" {
		t.Fatalf("unexpected profiles: %+v", ps)
	}
}
