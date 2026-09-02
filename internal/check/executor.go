package check

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/BoniLuan/vigil/internal/monitor"
)

const (
	// DefaultUserAgent is used by tests and local checker construction.
	DefaultUserAgent            = "Vigil/dev"
	MaxResponseDrainBytes int64 = 32 << 10
)

type Executor struct {
	resolver  Resolver
	policy    DestinationPolicy
	dial      dialContextFunc
	tlsConfig *tls.Config
	now       func() time.Time
	userAgent string
}

func NewExecutor(resolver Resolver) *Executor {
	return NewExecutorWithUserAgent(resolver, DefaultUserAgent)
}

func NewExecutorWithUserAgent(resolver Resolver, userAgent string) *Executor {
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	netDialer := &net.Dialer{KeepAlive: 30 * time.Second}
	return &Executor{
		resolver: resolver,
		policy:   DefaultDestinationPolicy(),
		dial:     netDialer.DialContext,
		tlsConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		now: time.Now, userAgent: userAgent,
	}
}

func (e *Executor) Execute(ctx context.Context, configured monitor.Monitor) Result {
	started := e.now()
	result := Result{MonitorID: configured.ID, StartedAt: started.UTC()}
	finish := func() Result {
		finished := e.now()
		result.FinishedAt = finished.UTC()
		result.Duration = finished.Sub(started)
		if result.Duration < 0 {
			result.Duration = 0
		}
		return result
	}

	if failure := classifyContextError(ctx, ctx.Err()); failure != nil {
		result.Outcome = failure.outcome
		result.Error = errorPointer(failure.code, failure.description)
		return finish()
	}

	if configured.HTTPMethod != monitor.MethodGET && configured.HTTPMethod != monitor.MethodHEAD {
		result.Outcome = OutcomeInternalError
		result.Error = errorPointer(ErrorCodeInternal, "unsupported HTTP method in monitor configuration")
		return finish()
	}
	destination, err := ValidateExecutionURL(configured.URL)
	if err != nil {
		result.Outcome = OutcomeInternalError
		result.Error = errorPointer(ErrorCodeInternal, "invalid monitor destination URL")
		return finish()
	}

	executionCtx, cancel := context.WithTimeoutCause(ctx, configured.Timeout, errMonitorDeadline)
	defer cancel()
	visited := map[string]struct{}{redirectKey(destination): {}}
	redirects := 0

	for {
		response, transport, dialer, failure := e.executeHop(executionCtx, destination, configured.HTTPMethod)
		if dialer != nil {
			result.DialedIP = dialer.DialedIP()
		}
		if failure != nil {
			result.Outcome = failure.outcome
			result.Error = errorPointer(failure.code, failure.description)
			return finish()
		}

		statusCode := response.StatusCode
		result.StatusCode = &statusCode
		result.TLSExpiresAt = nil
		if leaf := verifiedLeaf(response.TLS); leaf != nil {
			expires := leaf.NotAfter.UTC()
			result.TLSExpiresAt = &expires
		}
		next, redirect, redirectErr := redirectTarget(response, destination)
		if err := closeHopResponse(executionCtx, response, configured.HTTPMethod, transport); err != nil {
			setContextFailure(&result, executionCtx)
			return finish()
		}
		if redirectErr != nil {
			result.Outcome = OutcomeConnectionError
			result.Error = errorPointer(ErrorCodeRedirectInvalid, "redirect target is invalid or unsafe")
			return finish()
		}
		if !redirect {
			if response.StatusCode >= configured.ExpectedStatusMin && response.StatusCode <= configured.ExpectedStatusMax {
				result.Outcome = OutcomeSuccess
			} else {
				result.Outcome = OutcomeHTTPFailure
				result.Error = errorPointer(ErrorCodeUnexpectedStatus, "HTTP response status was outside the expected range")
			}
			return finish()
		}
		if destination.Scheme == "https" && next.Scheme == "http" {
			result.Outcome = OutcomeConnectionError
			result.Error = errorPointer(ErrorCodeRedirectDowngrade, "HTTPS redirect to HTTP is prohibited")
			return finish()
		}
		key := redirectKey(next)
		if _, found := visited[key]; found {
			result.Outcome = OutcomeConnectionError
			result.Error = errorPointer(ErrorCodeRedirectLoop, "redirect loop detected")
			return finish()
		}
		if redirects >= MaxRedirects {
			result.Outcome = OutcomeConnectionError
			result.Error = errorPointer(ErrorCodeRedirectLimit, "redirect limit exceeded")
			return finish()
		}
		redirects++
		visited[key] = struct{}{}
		destination = next
	}
}

func (e *Executor) transport(destination *url.URL, dialer *controlledDialer) *http.Transport {
	tlsConfig := e.tlsConfig.Clone()
	if destination.Scheme == "https" {
		tlsConfig.ServerName = destination.Hostname()
	}
	dialTLSContext := func(ctx context.Context, network, address string) (net.Conn, error) {
		connection, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		tlsConnection := tls.Client(connection, tlsConfig.Clone())
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			_ = connection.Close()
			return nil, &tlsHandshakeError{err: err}
		}
		return tlsConnection, nil
	}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		DialTLSContext:        dialTLSContext,
		TLSClientConfig:       tlsConfig,
		ForceAttemptHTTP2:     true,
		DisableCompression:    true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

func setContextFailure(result *Result, ctx context.Context) {
	failure := classifyContextError(ctx, ctx.Err())
	if failure == nil {
		failure = &hopFailure{OutcomeTimeout, ErrorCodeDeadlineExceeded, "check deadline exceeded"}
	}
	result.Outcome = failure.outcome
	result.Error = errorPointer(failure.code, failure.description)
}

func errorPointer(code ErrorCode, description string) *ErrorDetail {
	detail := NewErrorDetail(code, description)
	return &detail
}
