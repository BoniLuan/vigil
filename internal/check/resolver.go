package check

import (
	"context"
	"net/netip"
)

// Resolver is the narrow deterministic DNS boundary. The executor resolves each
// network hop once, validates every candidate, and configures its dialer with
// only approved addresses. The HTTP transport never resolves the hostname.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}
