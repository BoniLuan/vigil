package check

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"syscall"
)

var errMonitorDeadline = errors.New("monitor check deadline exceeded")

type tlsHandshakeError struct {
	err error
}

func (e *tlsHandshakeError) Error() string { return "TLS handshake failed" }
func (e *tlsHandshakeError) Unwrap() error { return e.err }

func classifyExecutionError(ctx context.Context, err error) *hopFailure {
	if failure := classifyContextError(ctx, err); failure != nil {
		return failure
	}

	var handshakeError *tlsHandshakeError
	if errors.As(err, &handshakeError) {
		return classifyTLSHandshakeError(handshakeError.err)
	}

	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return &hopFailure{OutcomeConnectionError, ErrorCodeConnectionRefused, "destination refused the connection"}
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return &hopFailure{OutcomeConnectionError, ErrorCodeNetworkUnreachable, "destination network is unreachable"}
	case errors.Is(err, syscall.ECONNRESET):
		return &hopFailure{OutcomeConnectionError, ErrorCodeConnectionReset, "connection was reset"}
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed):
		return &hopFailure{OutcomeConnectionError, ErrorCodeConnectionClosed, "connection closed before a complete response"}
	default:
		return &hopFailure{OutcomeConnectionError, ErrorCodeConnectionFailed, "connection or HTTP exchange failed"}
	}
}

func classifyContextError(ctx context.Context, err error) *hopFailure {
	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, errMonitorDeadline):
		return &hopFailure{OutcomeTimeout, ErrorCodeRequestTimeout, "configured check timeout exceeded"}
	case errors.Is(cause, context.Canceled):
		return &hopFailure{OutcomeTimeout, ErrorCodeCancelled, "check canceled by caller"}
	case errors.Is(cause, context.DeadlineExceeded):
		return &hopFailure{OutcomeTimeout, ErrorCodeDeadlineExceeded, "caller deadline exceeded"}
	case errors.Is(err, context.Canceled):
		return &hopFailure{OutcomeTimeout, ErrorCodeCancelled, "check canceled"}
	case errors.Is(err, context.DeadlineExceeded):
		return &hopFailure{OutcomeTimeout, ErrorCodeDeadlineExceeded, "check deadline exceeded"}
	default:
		return nil
	}
}

func classifyTLSHandshakeError(err error) *hopFailure {
	var hostnameError x509.HostnameError
	if errors.As(err, &hostnameError) {
		return &hopFailure{OutcomeTLSError, ErrorCodeTLSHostname, "TLS certificate is not valid for the monitor hostname"}
	}
	var unknownAuthorityError x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthorityError) {
		return &hopFailure{OutcomeTLSError, ErrorCodeTLSCertificate, "TLS certificate is not trusted"}
	}
	var certificateInvalidError x509.CertificateInvalidError
	if errors.As(err, &certificateInvalidError) {
		return &hopFailure{OutcomeTLSError, ErrorCodeTLSCertificate, "TLS certificate is invalid or outside its validity period"}
	}
	var verificationError *tls.CertificateVerificationError
	if errors.As(err, &verificationError) {
		return &hopFailure{OutcomeTLSError, ErrorCodeTLSCertificate, "TLS certificate verification failed"}
	}
	return &hopFailure{OutcomeTLSError, ErrorCodeTLSHandshakeFailed, "TLS negotiation or handshake failed"}
}

func verifiedLeaf(state *tls.ConnectionState) *x509.Certificate {
	if state == nil {
		return nil
	}
	if len(state.VerifiedChains) > 0 && len(state.VerifiedChains[0]) > 0 {
		return state.VerifiedChains[0][0]
	}
	if len(state.PeerCertificates) > 0 {
		return state.PeerCertificates[0]
	}
	return nil
}
