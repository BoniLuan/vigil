package check

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStableOutcomes(t *testing.T) {
	for _, outcome := range []Outcome{
		OutcomeSuccess, OutcomeHTTPFailure, OutcomeTimeout, OutcomeDNSError,
		OutcomeTLSError, OutcomeConnectionError, OutcomeInternalError,
	} {
		if !outcome.Valid() {
			t.Errorf("outcome %q is invalid", outcome)
		}
	}
	if Outcome("unknown").Valid() {
		t.Fatal("unknown outcome is valid")
	}
}

func TestSanitizeErrorDescription(t *testing.T) {
	raw := "  connection\nfailed\twith\x00control  " + strings.Repeat("é", 400)
	got := SanitizeErrorDescription(raw)
	if strings.ContainsAny(got, "\n\t\x00") {
		t.Fatalf("description contains controls: %q", got)
	}
	if len(got) > MaxErrorDescriptionBytes {
		t.Fatalf("description length = %d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("description is not valid UTF-8")
	}
	detail := NewErrorDetail(ErrorCodeConnectionFailed, raw)
	if detail.Code != ErrorCodeConnectionFailed || detail.Description != got {
		t.Fatalf("NewErrorDetail() = %+v", detail)
	}
	invalid := NewErrorDetail(ErrorCode(strings.Repeat("x", 1000)), "safe")
	if invalid.Code != ErrorCodeInternal {
		t.Fatalf("invalid error code = %q", invalid.Code)
	}
}
