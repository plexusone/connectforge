package egress

import (
	"net/netip"
	"testing"
)

func TestBlocked(t *testing.T) {
	cases := map[string]bool{
		// Public addresses.
		"8.8.8.8":              false,
		"1.1.1.1":              false,
		"2606:4700::1111":      false,
		"::ffff:8.8.8.8":       false,
		"64:ff9b::808:808":     false, // NAT64 of 8.8.8.8
		"2001:4860:4860::8888": false,

		// Loopback, private, link-local, multicast, unspecified.
		"127.0.0.1":       true,
		"10.0.0.1":        true,
		"172.16.0.1":      true,
		"192.168.1.1":     true,
		"169.254.169.254": true,
		"224.0.0.1":       true,
		"0.0.0.0":         true,
		"::1":             true,
		"::":              true,
		"fe80::1":         true,
		"fd00:ec2::254":   true,
		"ff02::1":         true,
		"::ffff:10.0.0.1": true,

		// IPv4 special-purpose ranges.
		"0.1.2.3":         true,
		"100.64.0.1":      true,
		"192.0.0.1":       true,
		"192.0.2.1":       true,
		"192.88.99.1":     true,
		"198.18.0.1":      true,
		"198.51.100.1":    true,
		"203.0.113.1":     true,
		"240.0.0.1":       true,
		"255.255.255.255": true,

		// IPv6 forms that embed an IPv4 address.
		"64:ff9b::a9fe:a9fe":  true, // NAT64 of 169.254.169.254
		"64:ff9b::a00:1":      true, // NAT64 of 10.0.0.1
		"64:ff9b::7f00:1":     true, // NAT64 of 127.0.0.1
		"64:ff9b:1::a00:1":    true, // local-use NAT64
		"::127.0.0.1":         true, // IPv4-compatible
		"::a9fe:a9fe":         true,
		"::8.8.8.8":           true,
		"2002:a9fe:a9fe::1":   true, // 6to4
		"2002:808:808::1":     true,
		"2001:0:4136:e378::1": true, // Teredo

		// IPv6 special-purpose ranges.
		"100::1":      true,
		"2001:db8::1": true,
		"3fff::1":     true,
		"5f00::1":     true,
		"fec0::1":     true,
		"2001:2::1":   true, // benchmarking
	}
	for s, want := range cases {
		if got := blocked(netip.MustParseAddr(s)); got != want {
			t.Errorf("blocked(%s) = %v, want %v", s, got, want)
		}
	}
}
