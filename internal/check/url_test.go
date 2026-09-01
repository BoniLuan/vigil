package check

import "testing"

func TestValidateExecutionURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		allowed bool
	}{
		{"HTTP", "http://example.com/health", true},
		{"HTTPS", "https://example.com:8443/health?full=true", true},
		{"credentials", "https://user:secret@example.com/health", false},
		{"FTP", "ftp://example.com/file", false},
		{"file", "file:///etc/passwd", false},
		{"missing host", "https:///health", false},
		{"relative", "/health", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := ValidateExecutionURL(test.raw)
			if test.allowed {
				if err != nil || parsed.Hostname() != "example.com" {
					t.Fatalf("ValidateExecutionURL(%q) = %v, %v", test.raw, parsed, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateExecutionURL(%q) unexpectedly allowed", test.raw)
			}
		})
	}
}
