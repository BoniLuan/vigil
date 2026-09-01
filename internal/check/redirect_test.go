package check

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/monitor"
)

type routingResolver struct {
	mu        sync.Mutex
	addresses map[string][]netip.Addr
	hosts     []string
}

func (r *routingResolver) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	if network != "ip" {
		return nil, fmt.Errorf("unexpected network %q", network)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts = append(r.hosts, host)
	addresses, found := r.addresses[host]
	if !found {
		return nil, fmt.Errorf("no fixture for %q", host)
	}
	return append([]netip.Addr(nil), addresses...), nil
}

func (r *routingResolver) Hosts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.hosts...)
}

type routingDialer struct {
	mu     sync.Mutex
	routes map[string]string
	dials  []string
}

func (d *routingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.dials = append(d.dials, address)
	host, _, _ := net.SplitHostPort(address)
	actual, found := d.routes[host]
	d.mu.Unlock()
	if !found {
		return nil, fmt.Errorf("unapproved test address %q", address)
	}
	return (&net.Dialer{}).DialContext(ctx, network, actual)
}

func (d *routingDialer) Dials() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.dials...)
}

func TestExecutorFollowsSameHostRelativeRedirect(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	executor, resolver, dialer, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP})
	result := executor.Execute(context.Background(), testMonitor(logicalURL+"/start", monitor.MethodGET, time.Second))
	if result.Outcome != OutcomeSuccess || result.StatusCode == nil || *result.StatusCode != http.StatusNoContent {
		t.Fatalf("Execute() = %+v", result)
	}
	if resolver.calls.Load() != 2 || len(dialer.Requests()) != 2 {
		t.Fatalf("resolver calls = %d, dials = %v", resolver.calls.Load(), dialer.Requests())
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(paths) != fmt.Sprint([]string{"/start", "/final"}) {
		t.Fatalf("paths = %v", paths)
	}
}

func TestExecutorCrossHostRedirectRevalidatesAndPreservesHost(t *testing.T) {
	finalHost := make(chan string, 1)
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finalHost <- r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer final.Close()
	finalURL, _ := url.Parse(final.URL)
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://"+net.JoinHostPort("next.test", finalURL.Port())+"/done")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer start.Close()
	startURL, _ := url.Parse(start.URL)

	resolver := &routingResolver{addresses: map[string][]netip.Addr{
		"start.test": {firstPublicIP}, "next.test": {secondPublicIP},
	}}
	dialer := &routingDialer{routes: map[string]string{
		firstPublicIP.String(): start.Listener.Addr().String(), secondPublicIP.String(): final.Listener.Addr().String(),
	}}
	executor := NewExecutor(resolver)
	executor.dial = dialer.DialContext
	logical := "http://" + net.JoinHostPort("start.test", startURL.Port()) + "/"
	result := executor.Execute(context.Background(), testMonitor(logical, monitor.MethodGET, time.Second))
	if result.Outcome != OutcomeSuccess || result.DialedIP != secondPublicIP {
		t.Fatalf("Execute() = %+v", result)
	}
	if got := resolver.Hosts(); fmt.Sprint(got) != fmt.Sprint([]string{"start.test", "next.test"}) {
		t.Fatalf("resolved hosts = %v", got)
	}
	dials := dialer.Dials()
	if len(dials) != 2 || !strings.HasPrefix(dials[0], firstPublicIP.String()+":") || !strings.HasPrefix(dials[1], secondPublicIP.String()+":") {
		t.Fatalf("explicit dials = %v", dials)
	}
	if got := <-finalHost; got != net.JoinHostPort("next.test", finalURL.Port()) {
		t.Fatalf("final Host = %q", got)
	}
}

func TestExecutorPreservesHEADAcrossRedirectStatuses(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			methods := make(chan string, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods <- r.Method
				if r.URL.Path == "/start" {
					w.Header().Set("Location", "/final")
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			executor, _, _, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP})
			result := executor.Execute(context.Background(), testMonitor(logicalURL+"/start", monitor.MethodHEAD, time.Second))
			if result.Outcome != OutcomeSuccess || <-methods != monitor.MethodHEAD || <-methods != monitor.MethodHEAD {
				t.Fatalf("Execute() = %+v", result)
			}
		})
	}
}

func TestExecutorRedirectDestinationPolicyFailsClosed(t *testing.T) {
	tests := []struct {
		name      string
		addresses []netip.Addr
	}{
		{"loopback", []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
		{"private", []netip.Addr{netip.MustParseAddr("10.0.0.1")}},
		{"metadata link-local", []netip.Addr{netip.MustParseAddr("169.254.169.254")}},
		{"mixed", []netip.Addr{secondPublicIP, netip.MustParseAddr("192.168.1.1")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "http://blocked.test/private")
				w.WriteHeader(http.StatusFound)
			}))
			defer server.Close()
			parsed, _ := url.Parse(server.URL)
			resolver := &routingResolver{addresses: map[string][]netip.Addr{
				"start.test": {firstPublicIP}, "blocked.test": test.addresses,
			}}
			dialer := &routingDialer{routes: map[string]string{firstPublicIP.String(): server.Listener.Addr().String()}}
			executor := NewExecutor(resolver)
			executor.dial = dialer.DialContext
			logical := "http://" + net.JoinHostPort("start.test", parsed.Port())
			result := executor.Execute(context.Background(), testMonitor(logical, monitor.MethodGET, time.Second))
			if result.Outcome != OutcomeDNSError || result.Error == nil || result.Error.Code != ErrorCodeDestinationProhibited {
				t.Fatalf("Execute() = %+v", result)
			}
			if len(resolver.Hosts()) != 2 || len(dialer.Dials()) != 1 {
				t.Fatalf("hosts = %v, dials = %v", resolver.Hosts(), dialer.Dials())
			}
		})
	}
}

func TestExecutorRedirectLimitAndLoop(t *testing.T) {
	t.Run("exact maximum succeeds", func(t *testing.T) {
		server := redirectCountServer(MaxRedirects)
		defer server.Close()
		executor, resolver, _, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP})
		result := executor.Execute(context.Background(), testMonitor(logicalURL+"/0", monitor.MethodGET, time.Second))
		if result.Outcome != OutcomeSuccess || resolver.calls.Load() != MaxRedirects+1 {
			t.Fatalf("Execute() = %+v, resolutions = %d", result, resolver.calls.Load())
		}
	})

	t.Run("more than maximum fails", func(t *testing.T) {
		server := redirectCountServer(MaxRedirects + 1)
		defer server.Close()
		executor, resolver, _, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP})
		result := executor.Execute(context.Background(), testMonitor(logicalURL+"/0", monitor.MethodGET, time.Second))
		assertCheckError(t, result, OutcomeConnectionError, ErrorCodeRedirectLimit)
		if resolver.calls.Load() != MaxRedirects+1 {
			t.Fatalf("resolutions = %d", resolver.calls.Load())
		}
	})

	t.Run("loop has specific diagnostic", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/a" {
				http.Redirect(w, r, "/b", http.StatusFound)
				return
			}
			http.Redirect(w, r, "/a", http.StatusFound)
		}))
		defer server.Close()
		executor, _, _, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP})
		result := executor.Execute(context.Background(), testMonitor(logicalURL+"/a", monitor.MethodGET, time.Second))
		assertCheckError(t, result, OutcomeConnectionError, ErrorCodeRedirectLoop)
	})
}

func TestExecutorRejectsInvalidRedirectTargets(t *testing.T) {
	for _, location := range []string{"ftp://example.test/file", "http://user:secret@example.test/", "http://[::1"} {
		t.Run(location, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", location)
				w.WriteHeader(http.StatusFound)
			}))
			defer server.Close()
			executor, resolver, dialer, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP})
			result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, time.Second))
			assertCheckError(t, result, OutcomeConnectionError, ErrorCodeRedirectInvalid)
			if resolver.calls.Load() != 1 || len(dialer.Requests()) != 1 {
				t.Fatalf("resolver calls = %d, dials = %v", resolver.calls.Load(), dialer.Requests())
			}
		})
	}
}

func TestExecutorRedirectChainUsesSingleTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(70 * time.Millisecond)
		step, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if step < 4 {
			w.Header().Set("Location", fmt.Sprintf("/%d", step+1))
			w.WriteHeader(http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	executor, _, _, logicalURL := executorForServer(t, server.URL, []netip.Addr{firstPublicIP})
	started := time.Now()
	result := executor.Execute(context.Background(), testMonitor(logicalURL+"/0", monitor.MethodGET, 150*time.Millisecond))
	elapsed := time.Since(started)
	if result.Outcome != OutcomeTimeout || elapsed > 500*time.Millisecond {
		t.Fatalf("Execute() = %+v, elapsed = %s", result, elapsed)
	}
}

func TestExecutorHTTPToHTTPSRedirectPreservesTLSName(t *testing.T) {
	observed := make(chan requestObservation, 1)
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- observe(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer secure.Close()
	certificate := secure.Certificate()
	logicalHostname := certificate.DNSNames[0]
	secureURL, _ := url.Parse(secure.URL)
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://"+net.JoinHostPort(logicalHostname, secureURL.Port())+"/final")
		w.WriteHeader(http.StatusFound)
	}))
	defer start.Close()
	startURL, _ := url.Parse(start.URL)

	resolver := &routingResolver{addresses: map[string][]netip.Addr{
		"start.test": {firstPublicIP}, logicalHostname: {secondPublicIP},
	}}
	dialer := &routingDialer{routes: map[string]string{
		firstPublicIP.String(): start.Listener.Addr().String(), secondPublicIP.String(): secure.Listener.Addr().String(),
	}}
	executor := NewExecutor(resolver)
	executor.dial = dialer.DialContext
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	executor.tlsConfig = executor.tlsConfig.Clone()
	executor.tlsConfig.RootCAs = roots
	logical := "http://" + net.JoinHostPort("start.test", startURL.Port())
	result := executor.Execute(context.Background(), testMonitor(logical, monitor.MethodGET, time.Second))
	if result.Outcome != OutcomeSuccess || result.DialedIP != secondPublicIP || result.TLSExpiresAt == nil {
		t.Fatalf("Execute() = %+v", result)
	}
	observation := <-observed
	if observation.ServerName != logicalHostname || observation.Host != net.JoinHostPort(logicalHostname, secureURL.Port()) {
		t.Fatalf("TLS observation = %+v", observation)
	}
}

func TestExecutorHTTPSRedirectAndDowngradePolicy(t *testing.T) {
	certificateServer := httptest.NewTLSServer(nil)
	certificate := certificateServer.Certificate()
	certificateServer.Close()
	logicalHostname := certificate.DNSNames[0]
	roots := x509.NewCertPool()
	roots.AddCert(certificate)

	t.Run("HTTPS to HTTPS", func(t *testing.T) {
		final := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS.ServerName != logicalHostname {
				t.Errorf("SNI = %q", r.TLS.ServerName)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer final.Close()
		finalURL, _ := url.Parse(final.URL)
		start := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "https://"+net.JoinHostPort(logicalHostname, finalURL.Port()))
			w.WriteHeader(http.StatusFound)
		}))
		defer start.Close()
		startURL, _ := url.Parse(start.URL)
		executor := tlsRoutingExecutor(logicalHostname, roots, start, final)
		logical := "https://" + net.JoinHostPort(logicalHostname, startURL.Port())
		result := executor.Execute(context.Background(), testMonitor(logical, monitor.MethodGET, time.Second))
		if result.Outcome != OutcomeSuccess {
			t.Fatalf("Execute() = %+v", result)
		}
	})

	t.Run("HTTPS to HTTP is rejected", func(t *testing.T) {
		var plaintextReached atomic.Bool
		plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { plaintextReached.Store(true) }))
		defer plain.Close()
		plainURL, _ := url.Parse(plain.URL)
		start := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "http://"+net.JoinHostPort("plain.test", plainURL.Port()))
			w.WriteHeader(http.StatusFound)
		}))
		defer start.Close()
		startURL, _ := url.Parse(start.URL)
		resolver := &routingResolver{addresses: map[string][]netip.Addr{logicalHostname: {firstPublicIP}}}
		dialer := &routingDialer{routes: map[string]string{firstPublicIP.String(): start.Listener.Addr().String()}}
		executor := NewExecutor(resolver)
		executor.dial = dialer.DialContext
		executor.tlsConfig.RootCAs = roots
		logical := "https://" + net.JoinHostPort(logicalHostname, startURL.Port())
		result := executor.Execute(context.Background(), testMonitor(logical, monitor.MethodGET, time.Second))
		assertCheckError(t, result, OutcomeConnectionError, ErrorCodeRedirectDowngrade)
		if plaintextReached.Load() || len(resolver.Hosts()) != 1 {
			t.Fatalf("plaintext reached = %v, hosts = %v", plaintextReached.Load(), resolver.Hosts())
		}
	})
}

func redirectCountServer(final int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if step < final {
			w.Header().Set("Location", fmt.Sprintf("/%d", step+1))
			w.WriteHeader(http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func tlsRoutingExecutor(host string, roots *x509.CertPool, servers ...*httptest.Server) *Executor {
	resolver := &routingResolver{addresses: map[string][]netip.Addr{host: {firstPublicIP}}}
	dialer := &routingDialer{routes: map[string]string{firstPublicIP.String(): servers[0].Listener.Addr().String()}}
	// Select by port because both logical hops intentionally share an IP.
	dialer.routes[firstPublicIP.String()] = servers[0].Listener.Addr().String()
	executor := NewExecutor(resolver)
	executor.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, _ := net.SplitHostPort(address)
		for _, server := range servers {
			_, serverPort, _ := net.SplitHostPort(server.Listener.Addr().String())
			if port == serverPort {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
		}
		return nil, fmt.Errorf("unknown TLS fixture port %q", port)
	}
	executor.tlsConfig.RootCAs = roots
	return executor
}

func assertCheckError(t *testing.T, result Result, outcome Outcome, code ErrorCode) {
	t.Helper()
	if result.Outcome != outcome || result.Error == nil || result.Error.Code != code {
		t.Fatalf("Execute() = %+v, want outcome %q code %q", result, outcome, code)
	}
}
