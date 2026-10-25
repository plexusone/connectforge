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

func TestControl(t *testing.T) {
	loopback := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	tenNet := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	everything := []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
	cases := []struct {
		name    string
		p       Policy
		address string
		ok      bool
	}{
		{"public", Policy{}, "8.8.8.8:443", true},
		{"private by default", Policy{}, "10.1.2.3:443", false},
		{"loopback by default", Policy{}, "127.0.0.1:443", false},
		{"listed loopback", Policy{AllowPrefixes: loopback}, "127.0.0.1:443", true},
		{"listed private range", Policy{AllowPrefixes: tenNet}, "10.1.2.3:443", true},
		{"unlisted private range", Policy{AllowPrefixes: tenNet}, "192.168.1.1:443", false},
		{"allow private, loopback", Policy{AllowPrivate: true}, "127.0.0.1:443", true},
		{"allow private, private", Policy{AllowPrivate: true}, "[fd12::1]:443", true},

		// Link-local and metadata addresses are refused whatever the policy.
		{"allow private, metadata", Policy{AllowPrivate: true}, "169.254.169.254:80", false},
		{"allow private, link-local", Policy{AllowPrivate: true}, "169.254.1.1:80", false},
		{"allow private, IPv6 link-local", Policy{AllowPrivate: true}, "[fe80::1]:80", false},
		{"allow private, IPv6 metadata", Policy{AllowPrivate: true}, "[fd00:ec2::254]:80", false},
		{"allow private, shared-space metadata", Policy{AllowPrivate: true}, "100.100.100.200:80", false},
		{"allow private, NAT64 metadata", Policy{AllowPrivate: true}, "[64:ff9b::a9fe:a9fe]:80", false},
		{"allow private, IPv4-compatible metadata", Policy{AllowPrivate: true}, "[::a9fe:a9fe]:80", false},
		{"allow private, 6to4 metadata", Policy{AllowPrivate: true}, "[2002:a9fe:a9fe::1]:80", false},
		{"allow private, mapped metadata", Policy{AllowPrivate: true}, "[::ffff:169.254.169.254]:80", false},
		{"listed metadata", Policy{AllowPrefixes: everything}, "169.254.169.254:80", false},
		{"listed IPv6 metadata", Policy{AllowPrefixes: everything}, "[fd00:ec2::254]:80", false},
	}
	for _, tc := range cases {
		err := tc.p.control("tcp", tc.address, nil)
		if (err == nil) != tc.ok {
			t.Errorf("%s: control(%s) = %v, want ok=%v", tc.name, tc.address, err, tc.ok)
		}
	}
}
