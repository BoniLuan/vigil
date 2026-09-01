package check

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
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

	candidates, err := e.resolver.LookupNetIP(executionCtx, "ip", destination.Hostname())
	if err != nil {
		if executionCtx.Err() != nil {
			setTimeout(&result)
		} else {
			result.Outcome = OutcomeDNSError
			result.Error = errorPointer(ErrorCodeDNSLookupFailed, "hostname resolution failed")
		}
		return finish()
	}
	approved, err := ValidateResolvedAddresses(e.policy, candidates)
	if err != nil {
		result.Outcome = OutcomeDNSError
		if errors.Is(err, ErrDestinationProhibited) {
			result.Error = errorPointer(ErrorCodeDestinationProhibited, "hostname resolved to a prohibited destination")
		} else {
			result.Error = errorPointer(ErrorCodeDNSLookupFailed, "hostname resolution returned no usable addresses")
		}
		return finish()
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
		host: destination.Hostname(), port: port, candidates: approved, dial: e.dial,
		executionCtx: executionCtx,
	}
	transport := e.transport(destination, dialer)
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(executionCtx, configured.HTTPMethod, destination.String(), nil)
	if err != nil {
		result.Outcome = OutcomeInternalError
		result.Error = errorPointer(ErrorCodeInternal, "failed to construct HTTP request")
		return finish()
	}
	request.Host = destination.Host
	request.Header.Set("User-Agent", UserAgent)

	response, err := client.Do(request)
	result.DialedIP = dialer.DialedIP()
	if err != nil {
		if executionCtx.Err() != nil {
			setTimeout(&result)
		} else {
			result.Outcome = OutcomeConnectionError
			result.Error = errorPointer(ErrorCodeConnectionFailed, "connection or HTTP exchange failed")
		}
		return finish()
	}
	defer response.Body.Close()
	statusCode := response.StatusCode
	result.StatusCode = &statusCode
	if response.TLS != nil && len(response.TLS.PeerCertificates) > 0 {
		expires := response.TLS.PeerCertificates[0].NotAfter.UTC()
		result.TLSExpiresAt = &expires
	}
	if configured.HTTPMethod == monitor.MethodGET {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, MaxResponseDrainBytes))
		if executionCtx.Err() != nil {
			setTimeout(&result)
			return finish()
		}
	}
	if response.StatusCode >= configured.ExpectedStatusMin && response.StatusCode <= configured.ExpectedStatusMax {
		result.Outcome = OutcomeSuccess
	} else {
		result.Outcome = OutcomeHTTPFailure
		result.Error = errorPointer(ErrorCodeUnexpectedStatus, "HTTP response status was outside the expected range")
	}
	return finish()
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
