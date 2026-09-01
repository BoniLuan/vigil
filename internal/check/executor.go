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
	// UserAgent remains stable until build metadata is exposed outside cmd/vigil.
	UserAgent                   = "Vigil/0.1"
	MaxResponseDrainBytes int64 = 32 << 10
)

type Executor struct {
	resolver  Resolver
	policy    DestinationPolicy
	dial      dialContextFunc
	tlsConfig *tls.Config
	now       func() time.Time
}

func NewExecutor(resolver Resolver) *Executor {
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
		now: time.Now,
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

	executionCtx, cancel := context.WithTimeout(ctx, configured.Timeout)
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
		if response.TLS != nil && len(response.TLS.PeerCertificates) > 0 {
			expires := response.TLS.PeerCertificates[0].NotAfter.UTC()
			result.TLSExpiresAt = &expires
		}
		next, redirect, redirectErr := redirectTarget(response, destination)
		if err := closeHopResponse(executionCtx, response, configured.HTTPMethod, transport); err != nil {
			setTimeout(&result)
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
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
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

func setTimeout(result *Result) {
	result.Outcome = OutcomeTimeout
	result.Error = errorPointer(ErrorCodeRequestTimeout, "check deadline exceeded or canceled")
}

func errorPointer(code ErrorCode, description string) *ErrorDetail {
	detail := NewErrorDetail(code, description)
	return &detail
}
