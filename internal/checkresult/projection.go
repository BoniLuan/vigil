package checkresult

import (
	"math"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/monitor"
)

func NextProjection(current Projection, outcome check.Outcome, failureThreshold, recoveryThreshold int) Projection {
	if current.State == monitor.StatePaused {
		return current
	}
	if outcome == check.OutcomeSuccess {
		current.ConsecutiveFailures = 0
		current.ConsecutiveSuccesses = increment(current.ConsecutiveSuccesses)
		switch current.State {
		case monitor.StatePending, monitor.StateUp:
			current.State = monitor.StateUp
		case monitor.StateDown:
			if current.ConsecutiveSuccesses >= int64(recoveryThreshold) {
				current.State = monitor.StateUp
			}
		}
		return current
	}

	current.ConsecutiveSuccesses = 0
	current.ConsecutiveFailures = increment(current.ConsecutiveFailures)
	switch current.State {
	case monitor.StatePending, monitor.StateUp:
		if current.ConsecutiveFailures >= int64(failureThreshold) {
			current.State = monitor.StateDown
		}
	case monitor.StateDown:
		current.State = monitor.StateDown
	}
	return current
}

func increment(value int64) int64 {
	if value == math.MaxInt64 {
		return value
	}
	return value + 1
}
