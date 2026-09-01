package monitor

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeSlug(t *testing.T) {
	tests := map[string]string{
		" FinPulse API ": "finpulse-api",
		"one---two":      "one-two",
		"Already-valid":  "already-valid",
		"symbols_! here": "symbols-here",
	}
	for input, want := range tests {
		if got := NormalizeSlug(input); got != want {
			t.Errorf("NormalizeSlug(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPrepareCreateAppliesDefaults(t *testing.T) {
	value, err := prepareCreate(CreateInput{Name: "FinPulse API", URL: "https://example.com/health"})
	if err != nil {
		t.Fatalf("prepareCreate() error = %v", err)
	}
	if value.Slug != "finpulse-api" || value.Kind != KindHTTP || value.HTTPMethod != MethodGET {
		t.Fatalf("unexpected identity defaults: %+v", value)
	}
	if value.Interval != time.Minute || value.Timeout != 2*time.Second {
		t.Fatalf("unexpected timing defaults: %+v", value)
	}
	if value.ExpectedStatusMin != 200 || value.ExpectedStatusMax != 200 || !value.Enabled {
		t.Fatalf("unexpected behavior defaults: %+v", value)
	}
}

func TestValidateRejectsInvalidConfiguration(t *testing.T) {
	base, err := prepareCreate(CreateInput{Name: "Valid", URL: "https://example.com/health"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		alter func(*Monitor)
		field string
	}{
		{"empty name", func(m *Monitor) { m.Name = "" }, "name"},
		{"bad slug", func(m *Monitor) { m.Slug = "Bad_Slug" }, "slug"},
		{"credentials", func(m *Monitor) { m.URL = "https://user:pass@example.com" }, "url"},
		{"scheme", func(m *Monitor) { m.URL = "ftp://example.com" }, "url"},
		{"method", func(m *Monitor) { m.HTTPMethod = "POST" }, "http_method"},
		{"short interval", func(m *Monitor) { m.Interval = 9 * time.Second }, "interval_seconds"},
		{"long interval", func(m *Monitor) { m.Interval = 25 * time.Hour }, "interval_seconds"},
		{"short timeout", func(m *Monitor) { m.Timeout = 99 * time.Millisecond }, "timeout_ms"},
		{"timeout interval", func(m *Monitor) { m.Timeout = m.Interval }, "timeout_ms"},
		{"status minimum", func(m *Monitor) { m.ExpectedStatusMin = 99 }, "expected_status_min"},
		{"status order", func(m *Monitor) { m.ExpectedStatusMin, m.ExpectedStatusMax = 300, 200 }, "expected_status_max"},
		{"failure threshold", func(m *Monitor) { m.FailureThreshold = 0 }, "failure_threshold"},
		{"recovery threshold", func(m *Monitor) { m.RecoveryThreshold = 0 }, "recovery_threshold"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.alter(&value)
			var validation *ValidationError
			if err := Validate(value); !errors.As(err, &validation) || validation.Fields[test.field] == "" {
				t.Fatalf("Validate() error = %#v, want field %q", err, test.field)
			}
		})
	}
}

func TestApplyPatchDistinguishesFalseAndDescriptionNull(t *testing.T) {
	description := "old"
	value, err := prepareCreate(CreateInput{Name: "Valid", URL: "https://example.com", Description: &description, Public: true})
	if err != nil {
		t.Fatal(err)
	}
	public := false
	applyPatch(&value, PatchInput{Description: OptionalString{Set: true}, Public: &public})
	if value.Description != nil || value.Public {
		t.Fatalf("patch did not preserve explicit null/false: %+v", value)
	}
}

func TestValidateState(t *testing.T) {
	for _, state := range []string{StatePending, StateUp, StateDown, StatePaused} {
		if err := ValidateState(state); err != nil {
			t.Errorf("ValidateState(%q) error = %v", state, err)
		}
	}
	if err := ValidateState("degraded"); err == nil {
		t.Fatal("ValidateState(degraded) error = nil")
	}
}
