// Package vpngate parses the VPN Gate public API CSV (http://www.vpngate.net/api/iphone/).
// Only the feeder uses it; the proxy daemon does not depend on VPN Gate.
package vpngate

import (
	"bufio"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Server struct {
	HostName     string
	IP           string
	CountryShort string
	CountryLong  string
	Operator     string
	Score        int
	Ping         int
	Speed        int
	NumSessions  int
	Config       string // decoded OpenVPN profile
}

// Parse reads the CSV. Lines starting with '*' are decoration, the header starts with '#'.
func Parse(r io.Reader) ([]Server, error) {
	var lines []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 8<<20), 8<<20)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "*") {
			continue
		}
		lines = append(lines, l)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "#") {
		return nil, fmt.Errorf("missing header line")
	}
	cr := csv.NewReader(strings.NewReader(strings.Join(lines, "\n")))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	recs, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, h := range recs[0] {
		idx[strings.TrimPrefix(h, "#")] = i
	}
	for _, n := range []string{"HostName", "IP", "Score", "Ping", "Speed", "CountryLong", "CountryShort", "NumVpnSessions", "Operator", "OpenVPN_ConfigData_Base64"} {
		if _, ok := idx[n]; !ok {
			return nil, fmt.Errorf("missing column %s", n)
		}
	}
	var out []Server
	for _, rec := range recs[1:] {
		if len(rec) < len(recs[0]) {
			continue
		}
		get := func(n string) string { return rec[idx[n]] }
		cfg, err := base64.StdEncoding.DecodeString(get("OpenVPN_ConfigData_Base64"))
		if err != nil || len(cfg) == 0 {
			continue
		}
		out = append(out, Server{
			HostName:     get("HostName"),
			IP:           get("IP"),
			CountryShort: strings.ToUpper(get("CountryShort")),
			CountryLong:  get("CountryLong"),
			Operator:     get("Operator"),
			Score:        atoi(get("Score")),
			Ping:         atoi(get("Ping")),
			Speed:        atoi(get("Speed")),
			NumSessions:  atoi(get("NumVpnSessions")),
			Config:       strings.ReplaceAll(string(cfg), "\r\n", "\n"),
		})
	}
	return out, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
