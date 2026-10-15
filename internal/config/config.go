// Package config loads the YAML configuration.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen         string        `yaml:"listen"`
	AdminListen    string        `yaml:"admin_listen"`
	Password       string        `yaml:"password"` // SOCKS5 password; empty disables authentication
	ProfilesDir    string        `yaml:"profiles_dir"`
	Tunnels        int           `yaml:"tunnels"`
	StickyTTL      time.Duration `yaml:"sticky_ttl"` // 0 = round robin
	HealthURL      string        `yaml:"health_url"`
	HealthInterval time.Duration `yaml:"health_interval"`
	Cooldown       time.Duration `yaml:"cooldown"`
	FallbackDNS    []string      `yaml:"fallback_dns"`
	TableBase      int           `yaml:"table_base"`
	OpenVPNBin     string        `yaml:"openvpn_bin"`
	DataDir        string        `yaml:"data_dir"`
}

func Load(path string) (*Config, error) {
	c := &Config{
		Listen:         "0.0.0.0:1080",
		AdminListen:    "0.0.0.0:9090",
		ProfilesDir:    "/profiles",
		Tunnels:        3,
		StickyTTL:      10 * time.Minute,
		HealthURL:      "http://www.gstatic.com/generate_204",
		HealthInterval: 30 * time.Second,
		Cooldown:       15 * time.Minute,
		FallbackDNS:    []string{"1.1.1.1", "8.8.8.8"},
		TableBase:      100,
		OpenVPNBin:     "openvpn",
		DataDir:        "/var/lib/ovpnmux",
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Tunnels < 1 {
		return nil, fmt.Errorf("tunnels must be >= 1")
	}
	return c, nil
}
