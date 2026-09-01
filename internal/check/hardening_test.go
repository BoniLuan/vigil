package check

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/monitor"
)

func TestExecutorClassifiesTLSFailures(t *testing.T) {
	t.Run("untrusted certificate", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
		defer server.Close()
		hostname := server.Certificate().DNSNames[0]
		executor, logicalURL := executorForTLSFixture(t, server, hostname, nil)
		result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, time.Second))
		assertCheckError(t, result, OutcomeTLSError, ErrorCodeTLSCertificate)
	})

	t.Run("hostname mismatch", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
		defer server.Close()
		roots := x509.NewCertPool()
		roots.AddCert(server.Certificate())
		executor, logicalURL := executorForTLSFixture(t, server, "wrong-host.test", roots)
		result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, time.Second))
		assertCheckError(t, result, OutcomeTLSError, ErrorCodeTLSHostname)
	})

	for _, test := range []struct {
		name      string
		notBefore time.Time
		notAfter  time.Time
	}{
		{"expired", time.Now().Add(-2 * time.Hour), time.Now().Add(-time.Hour)},
		{"not yet valid", time.Now().Add(time.Hour), time.Now().Add(2 * time.Hour)},
	} {
		t.Run(test.name+" certificate", func(t *testing.T) {
			certificate, leaf := testCertificate(t, "validity.test", test.notBefore, test.notAfter)
			server := newTLSServer(t, certificate, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
			defer server.Close()
			roots := x509.NewCertPool()
			roots.AddCert(leaf)
			executor, logicalURL := executorForTLSFixture(t, server, "validity.test", roots)
			result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, time.Second))
			assertCheckError(t, result, OutcomeTLSError, ErrorCodeTLSCertificate)
		})
	}

	t.Run("plain HTTP on HTTPS target", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
		defer server.Close()
		parsed, _ := url.Parse(server.URL)
		resolver := &fakeResolver{addresses: []netip.Addr{firstPublicIP}}
		dialer := &dialRecorder{actual: server.Listener.Addr().String()}
		executor := NewExecutor(resolver)
		executor.dial = dialer.DialContext
		logicalURL := "https://" + net.JoinHostPort("plain.test", parsed.Port())
		result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, time.Second))
		assertCheckError(t, result, OutcomeTLSError, ErrorCodeTLSHandshakeFailed)
	})
}

func TestExecutorExtractsVerifiedLeafExpiry(t *testing.T) {
	certificate, leaf := testCertificate(t, "expiry.test", time.Now().Add(-time.Hour), time.Now().Add(48*time.Hour))
	server := newTLSServer(t, certificate, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	executor, logicalURL := executorForTLSFixture(t, server, "expiry.test", roots)
	result := executor.Execute(context.Background(), testMonitor(logicalURL, monitor.MethodGET, time.Second))
	if result.Outcome != OutcomeSuccess || result.TLSExpiresAt == nil || !result.TLSExpiresAt.Equal(leaf.NotAfter.UTC()) {
		t.Fatalf("Execute() = %+v, certificate expiry = %s", result, leaf.NotAfter.UTC())
	}
}

func TestExecutorClassifiesConnectionFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code ErrorCode
	}{
		{"refused", syscall.ECONNREFUSED, ErrorCodeConnectionRefused},
		{"network unreachable", syscall.ENETUNREACH, ErrorCodeNetworkUnreachable},
		{"reset", syscall.ECONNRESET, ErrorCodeConnectionReset},
		{"closed", io.EOF, ErrorCodeConnectionClosed},
		{"generic", errors.New("fixture failure"), ErrorCodeConnectionFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := NewExecutor(&fakeResolver{addresses: []netip.Addr{firstPublicIP}})
			executor.dial = func(context.Context, string, string) (net.Conn, error) { return nil, test.err }
			result := executor.Execute(context.Background(), testMonitor("http://service.test:8080/", monitor.MethodGET, time.Second))
			assertCheckError(t, result, OutcomeConnectionError, test.code)
		})
	}
}

func TestExecutorContextClassification(t *testing.T) {
	t.Run("already canceled avoids resolver", func(t *testing.T) {
		resolver := &fakeResolver{addresses: []netip.Addr{firstPublicIP}}
		executor := NewExecutor(resolver)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result := executor.Execute(ctx, testMonitor("http://service.test/", monitor.MethodGET, time.Second))
		assertCheckError(t, result, OutcomeTimeout, ErrorCodeCancelled)
		if resolver.calls.Load() != 0 {
			t.Fatalf("resolver calls = %d", resolver.calls.Load())
		}
	})

	t.Run("configured timeout during connection", func(t *testing.T) {
		executor := NewExecutor(&fakeResolver{addresses: []netip.Addr{firstPublicIP}})
		executor.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		result := executor.Execute(context.Background(), testMonitor("http://service.test:8080/", monitor.MethodGET, 50*time.Millisecond))
		assertCheckError(t, result, OutcomeTimeout, ErrorCodeRequestTimeout)
	})

	t.Run("caller deadline", func(t *testing.T) {
		resolver := &fakeResolver{block: true}
		executor := NewExecutor(resolver)
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		result := executor.Execute(ctx, testMonitor("http://service.test/", monitor.MethodGET, time.Second))
		assertCheckError(t, result, OutcomeTimeout, ErrorCodeDeadlineExceeded)
	})

	t.Run("active caller cancellation", func(t *testing.T) {
		resolver := &fakeResolver{block: true}
		executor := NewExecutor(resolver)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()
		result := executor.Execute(ctx, testMonitor("http://service.test/", monitor.MethodGET, time.Second))
		assertCheckError(t, result, OutcomeTimeout, ErrorCodeCancelled)
	})
}

func TestExecutorErrorDetailsDoNotLeakInputs(t *testing.T) {
	const secret = "super-secret-token"
	resolver := &fakeResolver{err: errors.New("resolver leaked " + secret + " HTTP_PROXY=http://proxy.internal")}
	executor := NewExecutor(resolver)
	result := executor.Execute(context.Background(), testMonitor("http://service.test/path?token="+secret, monitor.MethodGET, time.Second))
	assertCheckError(t, result, OutcomeDNSError, ErrorCodeDNSLookupFailed)
	if strings.Contains(result.Error.Description, secret) || strings.Contains(result.Error.Description, "proxy.internal") || len(result.Error.Description) > MaxErrorDescriptionBytes {
		t.Fatalf("unsafe error description = %q", result.Error.Description)
	}
}

func TestTransportSecurityConfiguration(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:1234")
	destination, _ := url.Parse("https://service.test/")
	dialer := &controlledDialer{host: "service.test", port: "443", candidates: []netip.Addr{firstPublicIP}}
	transport := NewExecutor(&fakeResolver{}).transport(destination, dialer)
	if transport.Proxy != nil || transport.DialContext == nil || transport.DialTLSContext == nil || !transport.DisableCompression {
		t.Fatalf("unsafe transport configuration: %+v", transport)
	}
	if transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.MinVersion != tls.VersionTLS12 || transport.TLSClientConfig.ServerName != "service.test" {
		t.Fatalf("unsafe TLS configuration: %+v", transport.TLSClientConfig)
	}
}

func executorForTLSFixture(t *testing.T, server *httptest.Server, hostname string, roots *x509.CertPool) (*Executor, string) {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &fakeResolver{addresses: []netip.Addr{firstPublicIP}}
	dialer := &dialRecorder{actual: server.Listener.Addr().String()}
	executor := NewExecutor(resolver)
	executor.dial = dialer.DialContext
	if roots != nil {
		executor.tlsConfig.RootCAs = roots
	}
	return executor, "https://" + net.JoinHostPort(hostname, parsed.Port()) + "/"
}

func testCertificate(t *testing.T, hostname string, notBefore, notAfter time.Time) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: hostname},
		DNSNames: []string{hostname}, NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{certificateDER}, PrivateKey: privateKey, Leaf: leaf}, leaf
}

func newTLSServer(t *testing.T, certificate tls.Certificate, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	return server
}

func TestClassificationNeverUsesInternalErrorForOperationalFailures(t *testing.T) {
	results := []Result{
		NewExecutor(&fakeResolver{err: errors.New("DNS failed")}).Execute(context.Background(), testMonitor("http://service.test/", monitor.MethodGET, time.Second)),
	}
	for _, result := range results {
		if result.Outcome == OutcomeInternalError {
			t.Fatalf("operational failure became internal error: %+v", result)
		}
	}
}

func TestCallerCancellationDoesNotDial(t *testing.T) {
	resolver := &fakeResolver{addresses: []netip.Addr{firstPublicIP}}
	executor := NewExecutor(resolver)
	var dialed atomic.Bool
	executor.dial = func(context.Context, string, string) (net.Conn, error) {
		dialed.Store(true)
		return nil, errors.New("unexpected dial")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = executor.Execute(ctx, testMonitor("http://service.test/", monitor.MethodGET, time.Second))
	if dialed.Load() {
		t.Fatal("canceled execution dialed a destination")
	}
}

func TestExecutorConfiguredTimeoutDuringTLSHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	release := make(chan struct{})
	defer close(release)
	accepted := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		close(accepted)
		defer connection.Close()
		<-release
	}()

	_, port, _ := net.SplitHostPort(listener.Addr().String())
	executor := NewExecutor(&fakeResolver{addresses: []netip.Addr{firstPublicIP}})
	executor.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
	}
	result := executor.Execute(context.Background(), testMonitor("https://service.test:"+port+"/", monitor.MethodGET, 60*time.Millisecond))
	assertCheckError(t, result, OutcomeTimeout, ErrorCodeRequestTimeout)
	select {
	case <-accepted:
	default:
		t.Fatal("TLS fixture never accepted the controlled connection")
	}
}
