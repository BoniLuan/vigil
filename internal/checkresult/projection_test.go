package checkresult

import (
	"testing"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/monitor"
)

func TestNextProjection(t *testing.T) {
	tests := []struct {
		name     string
		current  Projection
		outcome  check.Outcome
		failure  int
		recovery int
		want     Projection
	}{
		{"pending success", Projection{State: monitor.StatePending}, check.OutcomeSuccess, 3, 2, Projection{State: monitor.StateUp, ConsecutiveSuccesses: 1}},
		{"pending failure below threshold", Projection{State: monitor.StatePending, ConsecutiveFailures: 1}, check.OutcomeConnectionError, 3, 2, Projection{State: monitor.StatePending, ConsecutiveFailures: 2}},
		{"pending failure reaches threshold", Projection{State: monitor.StatePending, ConsecutiveFailures: 2}, check.OutcomeTimeout, 3, 2, Projection{State: monitor.StateDown, ConsecutiveFailures: 3}},
		{"up success resets failures", Projection{State: monitor.StateUp, ConsecutiveFailures: 2}, check.OutcomeSuccess, 3, 2, Projection{State: monitor.StateUp, ConsecutiveSuccesses: 1}},
		{"up failure below threshold", Projection{State: monitor.StateUp, ConsecutiveSuccesses: 5}, check.OutcomeHTTPFailure, 2, 2, Projection{State: monitor.StateUp, ConsecutiveFailures: 1}},
		{"up failure reaches threshold", Projection{State: monitor.StateUp, ConsecutiveFailures: 1}, check.OutcomeDNSError, 2, 2, Projection{State: monitor.StateDown, ConsecutiveFailures: 2}},
		{"down success below recovery", Projection{State: monitor.StateDown, ConsecutiveFailures: 4}, check.OutcomeSuccess, 2, 2, Projection{State: monitor.StateDown, ConsecutiveSuccesses: 1}},
		{"down success reaches recovery", Projection{State: monitor.StateDown, ConsecutiveSuccesses: 1}, check.OutcomeSuccess, 2, 2, Projection{State: monitor.StateUp, ConsecutiveSuccesses: 2}},
		{"down failure resets successes", Projection{State: monitor.StateDown, ConsecutiveSuccesses: 1}, check.OutcomeTLSError, 2, 2, Projection{State: monitor.StateDown, ConsecutiveFailures: 1}},
		{"paused ignores success", Projection{State: monitor.StatePaused, ConsecutiveFailures: 2}, check.OutcomeSuccess, 2, 2, Projection{State: monitor.StatePaused, ConsecutiveFailures: 2}},
		{"paused ignores failure", Projection{State: monitor.StatePaused, ConsecutiveSuccesses: 2}, check.OutcomeConnectionError, 2, 2, Projection{State: monitor.StatePaused, ConsecutiveSuccesses: 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NextProjection(test.current, test.outcome, test.failure, test.recovery); got != test.want {
				t.Fatalf("NextProjection() = %+v, want %+v", got, test.want)
			}
		})
	}
}
