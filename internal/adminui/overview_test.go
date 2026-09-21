package adminui

import (
	"testing"

	"github.com/BoniLuan/vigil/internal/monitor"
)

func TestSummarizeMonitorStates(t *testing.T) {
	got := summarize([]monitor.Monitor{
		{State: monitor.StateUp}, {State: monitor.StateUp},
		{State: monitor.StateDown}, {State: monitor.StatePending},
		{State: monitor.StatePaused},
	})
	if got.Total != 5 || got.Up != 2 || got.Down != 1 || got.Pending != 1 || got.Paused != 1 {
		t.Fatalf("overview = %+v", got)
	}
}
