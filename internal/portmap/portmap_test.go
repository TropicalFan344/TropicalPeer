package portmap

import (
	"net/netip"
	"testing"
)

func TestIsPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"203.0.113.7": true,
		"8.8.8.8":     true,
		"192.168.1.1": false, // home network
		"10.1.2.3":    false,
		"172.16.0.5":  false,
		"100.72.10.1": false, // carrier-grade NAT
		"100.128.0.1": true,  // just outside 100.64.0.0/10
		"127.0.0.1":   false,
		"169.254.1.1": false,
		"2001:db8::1": true,
		"fd00::1":     false,
		"0.0.0.0":     false,
	} {
		if got := IsPublic(netip.MustParseAddr(addr)); got != want {
			t.Errorf("IsPublic(%s) = %v, want %v", addr, got, want)
		}
	}
}
