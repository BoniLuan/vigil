package check

import (
	"context"
	"net/netip"
)

// Resolver is the narrow deterministic boundary required by Part 2. The future
// executor will resolve once, pass all candidates through DestinationPolicy,
// and configure its dialer with an approved netip address. The HTTP transport
// must never independently resolve the original hostname.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}
