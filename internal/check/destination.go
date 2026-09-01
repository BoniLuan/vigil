package check

import (
	"errors"
	"fmt"
	"net/netip"
)

type DestinationClass string

const (
	DestinationPublic      DestinationClass = "public"
	DestinationInvalid     DestinationClass = "invalid"
	DestinationUnspecified DestinationClass = "unspecified"
	DestinationLoopback    DestinationClass = "loopback"
	DestinationPrivate     DestinationClass = "private"
	DestinationLinkLocal   DestinationClass = "link_local"
	DestinationMulticast   DestinationClass = "multicast"
	DestinationReserved    DestinationClass = "reserved"
)

var ErrDestinationProhibited = errors.New("destination is not a globally routable public address")

// DestinationPolicy is deliberately a concrete, secure-by-default policy. A
// future trusted-target allowlist can be added as explicit policy state without
// changing or weakening DefaultDestinationPolicy.
type DestinationPolicy struct{}

func DefaultDestinationPolicy() DestinationPolicy { return DestinationPolicy{} }

func (DestinationPolicy) Classify(address netip.Addr) DestinationClass {
	if !address.IsValid() {
		return DestinationInvalid
	}
	if address.Zone() != "" {
		return DestinationReserved
	}
	address = address.Unmap()
	if address.IsUnspecified() {
		return DestinationUnspecified
	}
	if address.IsLoopback() {
		return DestinationLoopback
	}
	if address.IsPrivate() {
		return DestinationPrivate
	}
	if address.IsMulticast() {
		return DestinationMulticast
	}
	if address.IsLinkLocalUnicast() {
		return DestinationLinkLocal
	}
	if !address.IsGlobalUnicast() || matchesAny(address, reservedPrefixes) {
		return DestinationReserved
	}
	return DestinationPublic
}

func (policy DestinationPolicy) Validate(address netip.Addr) error {
	class := policy.Classify(address)
	if class == DestinationPublic {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrDestinationProhibited, class)
}

// ValidateResolvedAddresses fails closed if DNS returns no addresses or a mix
// containing any prohibited address. Part 2 must dial one of the returned IPs
// directly and must not give the hostname to a transport that resolves again.
func ValidateResolvedAddresses(policy DestinationPolicy, candidates []netip.Addr) ([]netip.Addr, error) {
	if len(candidates) == 0 {
		return nil, errors.New("DNS resolution returned no addresses")
	}
	approved := make([]netip.Addr, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = candidate.Unmap()
		if err := policy.Validate(candidate); err != nil {
			return nil, err
		}
		approved = append(approved, candidate)
	}
	return approved, nil
}

func matchesAny(address netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

var reservedPrefixes = prefixes(
	"0.0.0.0/8",
	"100.64.0.0/10",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.88.99.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"240.0.0.0/4",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"100::/64",
	"2001::/23",
	"2001:db8::/32",
	"2002::/16",
	"3fff::/20",
	"5f00::/16",
	"fec0::/10",
)

func prefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}
