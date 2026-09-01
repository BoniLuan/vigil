package check

import (
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Outcome string

const (
	OutcomeSuccess         Outcome = "success"
	OutcomeHTTPFailure     Outcome = "http_failure"
	OutcomeTimeout         Outcome = "timeout"
	OutcomeDNSError        Outcome = "dns_error"
	OutcomeTLSError        Outcome = "tls_error"
	OutcomeConnectionError Outcome = "connection_error"
	OutcomeInternalError   Outcome = "internal_error"
)

func (o Outcome) Valid() bool {
	switch o {
	case OutcomeSuccess, OutcomeHTTPFailure, OutcomeTimeout, OutcomeDNSError,
		OutcomeTLSError, OutcomeConnectionError, OutcomeInternalError:
		return true
	default:
		return false
	}
}

type ErrorCode string

const (
	ErrorCodeDestinationProhibited ErrorCode = "destination_prohibited"
	ErrorCodeDNSLookupFailed       ErrorCode = "dns_lookup_failed"
	ErrorCodeDNSNoAddresses        ErrorCode = "dns_no_addresses"
	ErrorCodeRequestTimeout        ErrorCode = "request_timeout"
	ErrorCodeDeadlineExceeded      ErrorCode = "deadline_exceeded"
	ErrorCodeCancelled             ErrorCode = "cancelled"
	ErrorCodeTLSCertificate        ErrorCode = "tls_certificate"
	ErrorCodeTLSHostname           ErrorCode = "tls_hostname"
	ErrorCodeTLSHandshakeFailed    ErrorCode = "tls_handshake_failed"
	ErrorCodeConnectionFailed      ErrorCode = "connection_failed"
	ErrorCodeConnectionRefused     ErrorCode = "connection_refused"
	ErrorCodeConnectionReset       ErrorCode = "connection_reset"
	ErrorCodeConnectionClosed      ErrorCode = "connection_closed"
	ErrorCodeNetworkUnreachable    ErrorCode = "network_unreachable"
	ErrorCodeUnexpectedStatus      ErrorCode = "unexpected_status"
	ErrorCodeRedirectLimit         ErrorCode = "redirect_limit"
	ErrorCodeRedirectLoop          ErrorCode = "redirect_loop"
	ErrorCodeRedirectDowngrade     ErrorCode = "redirect_downgrade"
	ErrorCodeRedirectInvalid       ErrorCode = "redirect_invalid"
	ErrorCodeInternal              ErrorCode = "internal_error"

	MaxErrorDescriptionBytes = 512
)

func (code ErrorCode) Valid() bool {
	switch code {
	case ErrorCodeDestinationProhibited, ErrorCodeDNSLookupFailed, ErrorCodeDNSNoAddresses,
		ErrorCodeRequestTimeout, ErrorCodeDeadlineExceeded, ErrorCodeCancelled,
		ErrorCodeTLSCertificate, ErrorCodeTLSHostname, ErrorCodeTLSHandshakeFailed,
		ErrorCodeConnectionFailed, ErrorCodeConnectionRefused, ErrorCodeConnectionReset,
		ErrorCodeConnectionClosed, ErrorCodeNetworkUnreachable, ErrorCodeUnexpectedStatus,
		ErrorCodeRedirectLimit, ErrorCodeRedirectLoop, ErrorCodeRedirectDowngrade,
		ErrorCodeRedirectInvalid, ErrorCodeInternal:
		return true
	default:
		return false
	}
}

type ErrorDetail struct {
	Code        ErrorCode
	Description string
}

func NewErrorDetail(code ErrorCode, description string) ErrorDetail {
	if !code.Valid() {
		code = ErrorCodeInternal
	}
	return ErrorDetail{Code: code, Description: SanitizeErrorDescription(description)}
}

// SanitizeErrorDescription makes diagnostic text safe and bounded for future
// storage. Callers must not pass full URLs or other secret-bearing values.
func SanitizeErrorDescription(description string) string {
	description = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, description)
	description = strings.Join(strings.Fields(description), " ")
	if len(description) <= MaxErrorDescriptionBytes {
		return description
	}
	description = description[:MaxErrorDescriptionBytes]
	for !utf8.ValidString(description) {
		description = description[:len(description)-1]
	}
	return description
}

type Result struct {
	MonitorID    uuid.UUID
	StartedAt    time.Time
	FinishedAt   time.Time
	Duration     time.Duration
	Outcome      Outcome
	StatusCode   *int
	Error        *ErrorDetail
	DialedIP     netip.Addr
	TLSExpiresAt *time.Time
}
