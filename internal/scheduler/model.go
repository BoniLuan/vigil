package scheduler

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	StatusPending   = "pending"
	StatusClaimed   = "claimed"
	StatusCompleted = "completed"
	StatusSkipped   = "skipped"

	DefaultBatchSize     = 25
	MaximumBatchSize     = 100
	DefaultLeaseDuration = 45 * time.Second
)

var ErrInvalidClaimOptions = errors.New("invalid scheduler claim options")

type Execution struct {
	ID             uuid.UUID
	MonitorID      uuid.UUID
	ScheduledAt    time.Time
	Status         string
	LeaseOwner     *uuid.UUID
	LeaseExpiresAt *time.Time
	ClaimCount     int
	FinishedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ClaimOptions struct {
	BatchSize     int
	LeaseDuration time.Duration
}

func NewWorkerID() (uuid.UUID, error) {
	return uuid.NewRandom()
}
