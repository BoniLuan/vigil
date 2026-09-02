package checkresult

import (
	"errors"
	"net/netip"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/google/uuid"
)

var (
	ErrInvalidResult       = errors.New("invalid completed check result")
	ErrExecutionNotFound   = errors.New("scheduled execution not found")
	ErrExecutionNotClaimed = errors.New("scheduled execution is not claimed")
	ErrLeaseLost           = errors.New("scheduled execution lease is no longer owned")
)

type StoredResult struct {
	ID           uuid.UUID
	MonitorID    uuid.UUID
	ExecutionID  *uuid.UUID
	StartedAt    time.Time
	FinishedAt   time.Time
	Duration     time.Duration
	Outcome      check.Outcome
	StatusCode   *int
	Error        *check.ErrorDetail
	DialedIP     *netip.Addr
	TLSExpiresAt *time.Time
	CreatedAt    time.Time
}

type Projection struct {
	State                string
	ConsecutiveFailures  int64
	ConsecutiveSuccesses int64
}

type Summary struct {
	Window           string
	CompletedChecks  int64
	SuccessfulChecks int64
	UptimePercent    *float64
	AverageLatency   *time.Duration
}

type ListOptions struct {
	Limit  int
	Offset int
}
