package check

import (
	"errors"
	"net/netip"
	"testing"
)

func TestDestinationPolicyClassify(t *testing.T) {
	policy := DefaultDestinationPolicy()
	tests := []struct {
		name    string
		address string
		class   DestinationClass
		allowed bool
	}{
		{"public IPv4", "1.1.1.1", DestinationPublic, true},
		{"public IPv6", "2606:4700:4700::1111", DestinationPublic, true},
		{"IPv4 loopback", "127.0.0.1", DestinationLoopback, false},
		{"IPv6 loopback", "::1", DestinationLoopback, false},
		{"private 10", "10.0.0.1", DestinationPrivate, false},
		{"private 172", "172.16.10.20", DestinationPrivate, false},
		{"private 192", "192.168.1.1", DestinationPrivate, false},
		{"metadata", "169.254.169.254", DestinationLinkLocal, false},
		{"IPv4 link local", "169.254.10.20", DestinationLinkLocal, false},
		{"IPv6 link local", "fe80::1", DestinationLinkLocal, false},
		{"IPv6 unique local", "fd12:3456::1", DestinationPrivate, false},
		{"IPv4 multicast", "224.0.0.1", DestinationMulticast, false},
		{"IPv6 multicast", "ff02::1", DestinationMulticast, false},
		{"IPv4 unspecified", "0.0.0.0", DestinationUnspecified, false},
		{"IPv6 unspecified", "::", DestinationUnspecified, false},
		{"carrier grade NAT", "100.64.0.1", DestinationReserved, false},
		{"IPv4 documentation", "192.0.2.1", DestinationReserved, false},
		{"IPv4 benchmark", "198.18.0.1", DestinationReserved, false},
		{"IPv4 reserved", "240.0.0.1", DestinationReserved, false},
		{"IPv6 documentation", "2001:db8::1", DestinationReserved, false},
		{"IPv6 6to4", "2002:7f00:1::", DestinationReserved, false},
		{"IPv6 deprecated site local", "fec0::1", DestinationReserved, false},
		{"IPv6 zone", "2606:4700:4700::1111%eth0", DestinationReserved, false},
		{"IPv4 mapped loopback", "::ffff:127.0.0.1", DestinationLoopback, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			address := netip.MustParseAddr(test.address)
			if got := policy.Classify(address); got != test.class {
				t.Errorf("Classify(%s) = %s, want %s", address, got, test.class)
			}
			err := policy.Validate(address)
			if test.allowed && err != nil {
				t.Errorf("Validate(%s) error = %v", address, err)
			}
			if !test.allowed && !errors.Is(err, ErrDestinationProhibited) {
				t.Errorf("Validate(%s) error = %v, want ErrDestinationProhibited", address, err)
			}
		})
	}
}

func TestDestinationPolicyRejectsInvalidAddress(t *testing.T) {
	policy := DefaultDestinationPolicy()
	if got := policy.Classify(netip.Addr{}); got != DestinationInvalid {
		t.Fatalf("Classify(invalid) = %s", got)
	}
	if !errors.Is(policy.Validate(netip.Addr{}), ErrDestinationProhibited) {
		t.Fatal("invalid address was allowed")
	}
}

func TestValidateResolvedAddressesFailsClosed(t *testing.T) {
	policy := DefaultDestinationPolicy()
	approved, err := ValidateResolvedAddresses(policy, []netip.Addr{
		netip.MustParseAddr("1.1.1.1"),
		netip.MustParseAddr("2606:4700:4700::1111"),
	})
	if err != nil || len(approved) != 2 {
		t.Fatalf("public candidates = %v, %v", approved, err)
	}
	if _, err := ValidateResolvedAddresses(policy, []netip.Addr{
		netip.MustParseAddr("1.1.1.1"),
		netip.MustParseAddr("127.0.0.1"),
	}); !errors.Is(err, ErrDestinationProhibited) {
		t.Fatalf("mixed candidates error = %v", err)
	}
	if _, err := ValidateResolvedAddresses(policy, nil); err == nil {
		t.Fatal("empty candidates error = nil")
	}
}
