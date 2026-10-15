// Package config loads the daemon configuration (YAML with ${ENV} expansion).
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen      string `yaml:"listen"`
	AdminListen string `yaml:"admin_listen"`
	Auth        struct {
		Required bool   `yaml:"required"`
		Password string `yaml:"password"`
	} `yaml:"auth"`
	ProfilesDir string `yaml:"profiles_dir"`
	Pool        struct {
		Size       int            `yaml:"size"`
		PerCountry map[string]int `yaml:"per_country"`
	} `yaml:"pool"`
	Selection struct {
		Default   string        `yaml:"default"` // rr | sticky
		StickyTTL time.Duration `yaml:"sticky_ttl"`
	} `yaml:"selection"`
	ExtraListeners []ExtraListener `yaml:"extra_listeners"`
	Routing        struct {
		TableBase   int      `yaml:"table_base"`
		FallbackDNS []string `yaml:"fallback_dns"`
	} `yaml:"routing"`
	Health struct {
		URL      string        `yaml:"url"`
		GeoURL   string        `yaml:"geo_url"`
		Interval time.Duration `yaml:"interval"`
		Cooldown time.Duration `yaml:"cooldown"`
	} `yaml:"health"`
	OpenVPNBin string `yaml:"openvpn_bin"`
	DataDir    string `yaml:"data_dir"`
}

// ExtraListener is an additional proxy port with a fixed selection key,
// for clients that cannot send a username.
type ExtraListener struct {
	Listen string `yaml:"listen"`
	Select string `yaml:"select"`
}

func Default() *Config {
	c := &Config{}
	c.Listen = "0.0.0.0:1080"
	c.AdminListen = "0.0.0.0:9090"
	c.ProfilesDir = "/profiles"
	c.Pool.Size = 4
	c.Selection.Default = "sticky"
	c.Selection.StickyTTL = 10 * time.Minute
	c.Routing.TableBase = 100
	c.Routing.FallbackDNS = []string{"1.1.1.1", "8.8.8.8"}
	c.Health.URL = "http://www.gstatic.com/generate_204"
	c.Health.GeoURL = "http://ip-api.com/json/?fields=status,countryCode,query"
	c.Health.Interval = 30 * time.Second
	c.Health.Cooldown = 15 * time.Minute
	c.OpenVPNBin = "openvpn"
	c.DataDir = "/var/lib/vpngate-proxy"
	return c
}

func Load(path string) (*Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(b))), c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Auth.Required && c.Auth.Password == "" {
		return nil, fmt.Errorf("auth.required is true but auth.password is empty")
	}
	if c.Pool.Size < 1 {
		return nil, fmt.Errorf("pool.size must be >= 1")
	}
	if c.Selection.Default != "rr" && c.Selection.Default != "sticky" {
		return nil, fmt.Errorf("selection.default must be rr or sticky")
	}
	return c, nil
}
