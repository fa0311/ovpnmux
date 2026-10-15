package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `# tags: country=jp name=public-vpn-251 score=42
client
dev tun
proto tcp
remote 219.100.37.223 443
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
	if p.Country != "JP" || p.Name != "public-vpn-251" || p.Score != 42 {
		t.Errorf("tags not parsed: %+v", p)
	}
	if p.Remote != "219.100.37.223 443" {
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
}

func TestParseRejectsDangerous(t *testing.T) {
	for _, d := range []string{"up /bin/sh", "plugin foo.so", "script-security 2", "--management 127.0.0.1 7505"} {
		if _, err := parse("remote a 1\n" + d + "\n"); err == nil {
			t.Errorf("%q should be rejected", d)
		}
	}
	if _, err := parse("client\ncipher AES-128-CBC\n"); err == nil {
		t.Error("profile without remote should be rejected")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "sub", "a.ovpn"), []byte(sample), 0o644)
	os.WriteFile(filepath.Join(dir, "b.ovpn"), []byte("remote x 1\nup /bin/sh\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "c.ovpn"), []byte("remote y 2\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.txt"), []byte("ignored"), 0o644)
	ps, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].ID != "c" || ps[1].ID != "sub/a" {
		t.Fatalf("unexpected profiles: %+v", ps)
	}
	if ps[0].Name != "c" || ps[1].Name != "public-vpn-251" {
		t.Errorf("names: %q %q", ps[0].Name, ps[1].Name)
	}
}
