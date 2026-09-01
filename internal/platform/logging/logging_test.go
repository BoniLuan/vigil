package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewJSONHonorsLevel(t *testing.T) {
	var output bytes.Buffer
	logger, err := New("warn", "json", &output)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	logger.Info("hidden")
	logger.Warn("visible", "component", "test")

	if strings.Contains(output.String(), "hidden") || !strings.Contains(output.String(), `"msg":"visible"`) {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestNewRejectsInvalidOptions(t *testing.T) {
	for _, test := range []struct{ level, format string }{{"loud", "json"}, {"info", "xml"}} {
		if _, err := New(test.level, test.format, &bytes.Buffer{}); err == nil {
			t.Fatalf("New(%q, %q) error = nil", test.level, test.format)
		}
	}
}
