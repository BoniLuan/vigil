package check

import "testing"

func TestExecutorUserAgentUsesInjectedBuildVersion(t *testing.T) {
	executor := NewExecutorWithUserAgent(&fakeResolver{}, "Vigil/1.2.3")
	if executor.userAgent != "Vigil/1.2.3" {
		t.Fatalf("user agent=%q", executor.userAgent)
	}
	if fallback := NewExecutorWithUserAgent(&fakeResolver{}, ""); fallback.userAgent != DefaultUserAgent {
		t.Fatalf("fallback=%q", fallback.userAgent)
	}
}
