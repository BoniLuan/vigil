package check

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/google/uuid"
)

var (
	firstPublicIP  = netip.MustParseAddr("1.1.1.1")
	secondPublicIP = netip.MustParseAddr("8.8.8.8")
)

type fakeResolver struct {
	addresses []netip.Addr
	err       error
	block     bool
	calls     atomic.Int32
}

func (r *fakeResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	r.calls.Add(1)
	if network != "ip" {
		return nil, fmt.Errorf("unexpected network %q", network)
	}
	if r.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return append([]netip.Addr(nil), r.addresses...), r.err
}

type dialRecorder struct {
	actual string

	mu        sync.Mutex
	requested []string
	fail      map[string]error
}

func (d *dialRecorder) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.requested = append(d.requested, address)
	err := d.fail[address]
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, network, d.actual)
}

func (d *dialRecorder) Requests() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.requested...)
}

type requestObservation struct {
	Method         string
	Host           string
	UserAgent      string
	AcceptEncoding string
	ServerName     string
}

func TestExecutorBasicGETAndHEAD(t *testing.T) {
	observations := make(chan requestObservation, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observations <- observe(r)
		switch r.URL.Path {
		case "/lower":
			w.WriteHeader(200)
		case "/upper":
			w.WriteHeader(201)
		case "/unexpected":
			w.WriteHeader(503)
		default:
			w.WriteHeader(204)
		}
		_, _ = io.WriteString(w, "body")
	}))
	defer server.Close()

	tests := []struct {
		name    string
		method  string
		path    string
		minimum int
		maximum int
		outcome Outcome
		status  int
	}{
		{"successful GET", monitor.MethodGET, "/lower", 200, 200, OutcomeSuccess, 200},
		{"successful HEAD", monitor.MethodHEAD, "/head", 200, 299, OutcomeSuccess, 204},
		{"lower boundary", monitor.MethodGET, "/lower", 200, 201, OutcomeSuccess, 200},
		{"upper boundary", monitor.MethodGET, "/upper", 200, 201, OutcomeSuccess, 201},
		{"unexpected status", monitor.MethodGET, "/unexpected", 200, 299, OutcomeHTTPFailure, 503},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor, resolver, dialer, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP})
			configured := testMonitor(logicalURL+test.path, test.method, 2*time.Second)
			configured.ExpectedStatusMin = test.minimum
			configured.ExpectedStatusMax = test.maximum
			before := time.Now().UTC()
			result := executor.Execute(context.Background(), configured)
			after := time.Now().UTC()

			if result.Outcome != test.outcome || result.StatusCode == nil || *result.StatusCode != test.status {
				t.Fatalf("Execute() = %+v", result)
			}
			if result.StartedAt.Before(before) || result.FinishedAt.After(after) || result.FinishedAt.Before(result.StartedAt) || result.Duration < 0 {
				t.Fatalf("invalid timing: %+v", result)
			}
			if result.DialedIP != firstPublicIP || resolver.calls.Load() != 1 {
				t.Fatalf("dialed IP = %s, resolver calls = %d", result.DialedIP, resolver.calls.Load())
			}
			requested := dialer.Requests()
			if len(requested) != 1 || !strings.HasPrefix(requested[0], firstPublicIP.String()+":") {
				t.Fatalf("dial requests = %v", requested)
			}
			observation := <-observations
			parsed, _ := url.Parse(logicalURL)
			if observation.Method != test.method || observation.Host != parsed.Host {
				t.Fatalf("server observation = %+v, want method %s host %s", observation, test.method, parsed.Host)
			}
			if observation.UserAgent != UserAgent || observation.AcceptEncoding != "" {
				t.Fatalf("headers = %+v", observation)
			}
		})
	}
}

func TestExecutorDNSFailuresFailClosedBeforeDial(t *testing.T) {
	tests := []struct {
		name      string
		resolver  *fakeResolver
		errorCode ErrorCode
	}{
		{"resolution error", &fakeResolver{err: errors.New("offline DNS failure")}, ErrorCodeDNSLookupFailed},
		{"empty result", &fakeResolver{}, ErrorCodeDNSNoAddresses},
		{"prohibited result", &fakeResolver{addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}, ErrorCodeDestinationProhibited},
		{"mixed result", &fakeResolver{addresses: []netip.Addr{firstPublicIP, netip.MustParseAddr("10.0.0.1")}}, ErrorCodeDestinationProhibited},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := NewExecutor(test.resolver)
			dialed := atomic.Bool{}
			executor.dial = func(context.Context, string, string) (net.Conn, error) {
				dialed.Store(true)
				return nil, errors.New("must not dial")
			}
			result := executor.Execute(context.Background(), testMonitor("http://service.test/", monitor.MethodGET, time.Second))
			if result.Outcome != OutcomeDNSError || result.Error == nil || result.Error.Code != test.errorCode {
				t.Fatalf("Execute() = %+v", result)
			}
			if dialed.Load() || test.resolver.calls.Load() != 1 {
				t.Fatalf("dialed = %v, resolver calls = %d", dialed.Load(), test.resolver.calls.Load())
			}
		})
	}
}

func TestExecutorAttemptsMultipleApprovedIPsInOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	executor, resolver, dialer, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP, secondPublicIP})
	parsed, _ := url.Parse(logicalURL)
	dialer.fail = map[string]error{
		net.JoinHostPort(firstPublicIP.String(), parsed.Port()): errors.New("first unavailable"),
	}
	result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, time.Second))
	if result.Outcome != OutcomeSuccess || result.DialedIP != secondPublicIP {
		t.Fatalf("Execute() = %+v", result)
	}
	want := []string{
		net.JoinHostPort(firstPublicIP.String(), parsed.Port()),
		net.JoinHostPort(secondPublicIP.String(), parsed.Port()),
	}
	if got := dialer.Requests(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("dial order = %v, want %v", got, want)
	}
	if resolver.calls.Load() != 1 {
		t.Fatalf("resolver calls = %d", resolver.calls.Load())
	}
}

func TestExecutorDoesNotResolveAgainBetweenValidationAndDial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	resolver := &changingResolver{}
	dialer := &dialRecorder{actual: parsed.Host}
	executor := NewExecutor(resolver)
	executor.dial = dialer.DialContext
	logicalURL := "http://" + net.JoinHostPort("service.test", parsed.Port())

	result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, time.Second))
	if result.Outcome != OutcomeSuccess || resolver.calls.Load() != 1 {
		t.Fatalf("Execute() = %+v, resolver calls = %d", result, resolver.calls.Load())
	}
	requested := dialer.Requests()
	if len(requested) != 1 || requested[0] != net.JoinHostPort(firstPublicIP.String(), parsed.Port()) {
		t.Fatalf("dial requests = %v", requested)
	}
}

func TestExecutorMultipleIPsShareOverallTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	resolver := &fakeResolver{addresses: []netip.Addr{firstPublicIP, secondPublicIP}}
	var requestsMu sync.Mutex
	var requests []string
	executor := NewExecutor(resolver)
	executor.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		requestsMu.Lock()
		requests = append(requests, address)
		requestsMu.Unlock()
		host, _, _ := net.SplitHostPort(address)
		if host == firstPublicIP.String() {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return (&net.Dialer{}).DialContext(ctx, network, parsed.Host)
	}
	logicalURL := "http://" + net.JoinHostPort("service.test", parsed.Port())
	started := time.Now()
	result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, 300*time.Millisecond))
	elapsed := time.Since(started)
	if result.Outcome != OutcomeSuccess || result.DialedIP != secondPublicIP {
		t.Fatalf("Execute() = %+v", result)
	}
	if elapsed >= 300*time.Millisecond {
		t.Fatalf("candidate attempts used more than the overall timeout: %s", elapsed)
	}
	requestsMu.Lock()
	requestCount := len(requests)
	requestsMu.Unlock()
	if requestCount != 2 {
		t.Fatalf("dial request count = %d", requestCount)
	}
}

func TestExecutorPreservesTLSHostnameAndSNI(t *testing.T) {
	observations := make(chan requestObservation, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observations <- observe(r)
		w.WriteHeader(200)
	}))
	defer server.Close()
	certificate := server.Certificate()
	if len(certificate.DNSNames) == 0 {
		t.Fatal("httptest certificate has no DNS names")
	}
	logicalHostname := certificate.DNSNames[0]
	serverURL, _ := url.Parse(server.URL)
	logicalURL := "https://" + net.JoinHostPort(logicalHostname, serverURL.Port()) + "/"
	resolver := &fakeResolver{addresses: []netip.Addr{firstPublicIP}}
	dialer := &dialRecorder{actual: server.Listener.Addr().String()}
	executor := NewExecutor(resolver)
	executor.dial = dialer.DialContext
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	executor.tlsConfig = executor.tlsConfig.Clone()
	executor.tlsConfig.RootCAs = roots

	result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, 2*time.Second))
	if result.Outcome != OutcomeSuccess || result.TLSExpiresAt == nil {
		t.Fatalf("Execute() = %+v", result)
	}
	observation := <-observations
	if observation.ServerName != logicalHostname || !strings.HasPrefix(observation.Host, logicalHostname+":") {
		t.Fatalf("TLS observation = %+v, hostname = %q", observation, logicalHostname)
	}
	if requested := dialer.Requests(); len(requested) != 1 || !strings.HasPrefix(requested[0], firstPublicIP.String()+":") {
		t.Fatalf("dial requests = %v", requested)
	}
}

func TestExecutorTimeoutAndCancellation(t *testing.T) {
	t.Run("configured timeout covers DNS", func(t *testing.T) {
		resolver := &fakeResolver{block: true}
		executor := NewExecutor(resolver)
		started := time.Now()
		result := executor.Execute(context.Background(), testMonitor("http://service.test/", monitor.MethodGET, 80*time.Millisecond))
		if result.Outcome != OutcomeTimeout || time.Since(started) > 500*time.Millisecond {
			t.Fatalf("Execute() = %+v, elapsed = %s", result, time.Since(started))
		}
	})

	t.Run("caller cancellation", func(t *testing.T) {
		resolver := &fakeResolver{block: true}
		executor := NewExecutor(resolver)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result := executor.Execute(ctx, testMonitor("http://service.test/", monitor.MethodGET, time.Second))
		if result.Outcome != OutcomeTimeout {
			t.Fatalf("Execute() = %+v", result)
		}
	})

	t.Run("slow response uses global deadline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()
		executor, _, _, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP})
		started := time.Now()
		result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, 100*time.Millisecond))
		if result.Outcome != OutcomeTimeout || time.Since(started) > 600*time.Millisecond {
			t.Fatalf("Execute() = %+v, elapsed = %s", result, time.Since(started))
		}
	})

	t.Run("slow body drain uses global deadline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		defer server.Close()
		executor, _, _, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP})
		started := time.Now()
		result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, 100*time.Millisecond))
		if result.Outcome != OutcomeTimeout || result.StatusCode == nil || time.Since(started) > 600*time.Millisecond {
			t.Fatalf("Execute() = %+v, elapsed = %s", result, time.Since(started))
		}
	})
}

func TestExecutorConnectionFailure(t *testing.T) {
	resolver := &fakeResolver{addresses: []netip.Addr{firstPublicIP}}
	executor := NewExecutor(resolver)
	executor.dial = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}
	result := executor.Execute(context.Background(), testMonitor("http://service.test:8080/", monitor.MethodGET, time.Second))
	if result.Outcome != OutcomeConnectionError || result.Error == nil || result.Error.Code != ErrorCodeConnectionFailed {
		t.Fatalf("Execute() = %+v", result)
	}
}

func TestExecutorIgnoresEnvironmentProxyAcrossRedirects(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	var redirected atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			redirected.Store(true)
			w.WriteHeader(200)
			return
		}
		w.Header().Set("Location", "/redirected")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	executor, _, _, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP})
	configured := testMonitor(logicalURL, monitor.MethodGET, time.Second)
	result := executor.Execute(context.Background(), configured)
	if result.Outcome != OutcomeSuccess || result.StatusCode == nil || *result.StatusCode != http.StatusOK || !redirected.Load() {
		t.Fatalf("Execute() = %+v, redirected = %v", result, redirected.Load())
	}
}

func TestExecutorBoundsResponseDrainAndClosesBody(t *testing.T) {
	clientClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher := w.(http.Flusher)
		chunk := make([]byte, 4096)
		for {
			if _, err := w.Write(chunk); err != nil {
				close(clientClosed)
				return
			}
			flusher.Flush()
		}
	}))
	defer server.Close()
	executor, _, _, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP})
	started := time.Now()
	result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, time.Second))
	if result.Outcome != OutcomeSuccess || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("Execute() = %+v, elapsed = %s", result, time.Since(started))
	}
	select {
	case <-clientClosed:
	case <-time.After(time.Second):
		t.Fatal("server did not observe the bounded response body being closed")
	}
}

func executorForServer(t *testing.T, serverURL string, addresses []netip.Addr) (*Executor, *fakeResolver, *dialRecorder, string) {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &fakeResolver{addresses: addresses}
	dialer := &dialRecorder{actual: parsed.Host}
	executor := NewExecutor(resolver)
	executor.dial = dialer.DialContext
	logicalURL := parsed.Scheme + "://" + net.JoinHostPort("service.test", parsed.Port())
	return executor, resolver, dialer, logicalURL
}

func testMonitor(destination, method string, timeout time.Duration) monitor.Monitor {
	return monitor.Monitor{
		ID: uuid.Must(uuid.NewV7()), URL: destination, HTTPMethod: method,
		ExpectedStatusMin: 200, ExpectedStatusMax: 299, Timeout: timeout,
	}
}

func observe(request *http.Request) requestObservation {
	serverName := ""
	if request.TLS != nil {
		serverName = request.TLS.ServerName
	}
	return requestObservation{
		Method: request.Method, Host: request.Host,
		UserAgent: request.UserAgent(), AcceptEncoding: request.Header.Get("Accept-Encoding"),
		ServerName: serverName,
	}
}

type changingResolver struct {
	calls atomic.Int32
}

func (r *changingResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	if r.calls.Add(1) == 1 {
		return []netip.Addr{firstPublicIP}, nil
	}
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}
