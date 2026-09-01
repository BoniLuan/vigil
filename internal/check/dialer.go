package check

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

type controlledDialer struct {
	host         string
	port         string
	candidates   []netip.Addr
	dial         dialContextFunc
	executionCtx context.Context

	mu       sync.Mutex
	dialedIP netip.Addr
}

func (d *controlledDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if d.executionCtx != nil {
		ctx = d.executionCtx
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("transport requested an invalid destination address")
	}
	if !strings.EqualFold(strings.TrimSuffix(host, "."), strings.TrimSuffix(d.host, ".")) || port != d.port {
		return nil, errors.New("transport requested an unexpected destination")
	}

	var lastErr error
	// Preserve resolver order. Each attempt receives an equal share of the
	// remaining overall deadline, so candidate count cannot multiply timeout.
	for index, candidate := range d.candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d.setDialedIP(candidate)
		attemptCtx, cancel := candidateContext(ctx, len(d.candidates)-index)
		conn, err := d.dial(attemptCtx, network, net.JoinHostPort(candidate.String(), port))
		cancel()
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lastErr == nil {
		lastErr = errors.New("no approved destination addresses")
	}
	return nil, fmt.Errorf("all approved destination addresses failed: %w", lastErr)
}

func (d *controlledDialer) DialedIP() netip.Addr {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dialedIP
}

func (d *controlledDialer) setDialedIP(address netip.Addr) {
	d.mu.Lock()
	d.dialedIP = address
	d.mu.Unlock()
}

func candidateContext(ctx context.Context, candidatesRemaining int) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok || candidatesRemaining <= 1 {
		return context.WithCancel(ctx)
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, remaining/time.Duration(candidatesRemaining))
}
