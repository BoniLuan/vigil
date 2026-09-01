package check

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/BoniLuan/vigil/internal/monitor"
)

// MaxRedirects is the number of redirect hops permitted after the initial
// request. It is intentionally not configurable in v0.1.
const MaxRedirects = 5

type hopFailure struct {
	outcome     Outcome
	code        ErrorCode
	description string
}

// executeHop performs exactly one request. Each call resolves and validates
// its logical hostname and gives the transport only the approved addresses.
func (e *Executor) executeHop(ctx context.Context, destination *url.URL, method string) (*http.Response, *http.Transport, *controlledDialer, *hopFailure) {
	candidates, err := e.resolver.LookupNetIP(ctx, "ip", destination.Hostname())
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, &hopFailure{OutcomeTimeout, ErrorCodeRequestTimeout, "check deadline exceeded or canceled"}
		}
		return nil, nil, nil, &hopFailure{OutcomeDNSError, ErrorCodeDNSLookupFailed, "hostname resolution failed"}
	}
	approved, err := ValidateResolvedAddresses(e.policy, candidates)
	if err != nil {
		if errors.Is(err, ErrDestinationProhibited) {
			return nil, nil, nil, &hopFailure{OutcomeDNSError, ErrorCodeDestinationProhibited, "hostname resolved to a prohibited destination"}
		}
		return nil, nil, nil, &hopFailure{OutcomeDNSError, ErrorCodeDNSLookupFailed, "hostname resolution returned no usable addresses"}
	}

	port := destination.Port()
	if port == "" {
		if destination.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	dialer := &controlledDialer{
		host: destination.Hostname(), port: port, candidates: approved,
		dial: e.dial, executionCtx: ctx,
	}
	transport := e.transport(destination, dialer)
	request, err := http.NewRequestWithContext(ctx, method, destination.String(), nil)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, nil, dialer, &hopFailure{OutcomeInternalError, ErrorCodeInternal, "failed to construct HTTP request"}
	}
	request.Host = destination.Host
	request.Header.Set("User-Agent", UserAgent)

	response, err := transport.RoundTrip(request)
	if err != nil {
		transport.CloseIdleConnections()
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, nil, dialer, &hopFailure{OutcomeTimeout, ErrorCodeRequestTimeout, "check deadline exceeded or canceled"}
		}
		return nil, nil, dialer, &hopFailure{OutcomeConnectionError, ErrorCodeConnectionFailed, "connection or HTTP exchange failed"}
	}
	return response, transport, dialer, nil
}

func closeHopResponse(ctx context.Context, response *http.Response, method string, transport *http.Transport) error {
	if method == monitor.MethodGET {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, MaxResponseDrainBytes))
	}
	_ = response.Body.Close()
	transport.CloseIdleConnections()
	return ctx.Err()
}

func redirectTarget(response *http.Response, current *url.URL) (*url.URL, bool, error) {
	if !isRedirectStatus(response.StatusCode) {
		return nil, false, nil
	}
	location := response.Header.Get("Location")
	if location == "" {
		return nil, false, nil
	}
	reference, err := url.Parse(location)
	if err != nil {
		return nil, false, errors.New("invalid redirect location")
	}
	resolved := current.ResolveReference(reference)
	validated, err := ValidateExecutionURL(resolved.String())
	if err != nil {
		return nil, false, errors.New("redirect target failed URL safety validation")
	}
	validated.Fragment = ""
	return validated, true, nil
}

func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

// redirectKey is deliberately simple: its purpose is clearer diagnostics, not
// security. MaxRedirects remains the hard safety boundary.
func redirectKey(destination *url.URL) string {
	copy := *destination
	copy.Scheme = strings.ToLower(copy.Scheme)
	copy.Host = strings.ToLower(copy.Host)
	copy.Fragment = ""
	return copy.String()
}
