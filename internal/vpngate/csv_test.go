package vpngate

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cfg := base64.StdEncoding.EncodeToString([]byte("client\r\nremote 1.2.3.4 443\r\n"))
	in := "*vpn_servers\n" +
		"#HostName,IP,Score,Ping,Speed,CountryLong,CountryShort,NumVpnSessions,Uptime,TotalUsers,TotalTraffic,LogType,Operator,Message,OpenVPN_ConfigData_Base64\n" +
		"public-vpn-1,1.2.3.4,100,20,5000,Japan,jp,7,1,2,3,2weeks,Daiyuu Nobori_ Japan,msg," + cfg + "\n" +
		"broken,1.2.3.5,1,1,1,Japan,JP,1,1,1,1,x,y,z,\n" +
		"*\n"
	s, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 1 {
		t.Fatalf("got %d servers, want 1 (row without config skipped)", len(s))
	}
	if s[0].HostName != "public-vpn-1" || s[0].CountryShort != "JP" || s[0].Score != 100 || s[0].NumSessions != 7 {
		t.Errorf("unexpected: %+v", s[0])
	}
	if s[0].Config != "client\nremote 1.2.3.4 443\n" {
		t.Errorf("config not normalised: %q", s[0].Config)
	}
	if _, err := Parse(strings.NewReader("*vpn_servers\nnot,a,header\n")); err == nil {
		t.Error("expected header error")
	}
}
