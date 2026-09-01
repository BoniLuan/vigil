package check

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/monitor"
)

func TestExecutorPreservesGETAcrossRedirectStatuses(t *testing.T) {
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
			result := executor.Execute(context.Background(), testMonitor(logicalURL+"/start", monitor.MethodGET, time.Second))
			if result.Outcome != OutcomeSuccess || <-methods != monitor.MethodGET || <-methods != monitor.MethodGET {
				t.Fatalf("Execute() = %+v", result)
			}
		})
	}
}
