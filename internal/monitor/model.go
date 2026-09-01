package monitor

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	KindHTTP   = "http"
	MethodGET  = "GET"
	MethodHEAD = "HEAD"

	StatePending = "pending"
	StateUp      = "up"
	StateDown    = "down"
	StatePaused  = "paused"

	MinInterval = 10 * time.Second
	MaxInterval = 24 * time.Hour
	MinTimeout  = 100 * time.Millisecond
	MaxTimeout  = 30 * time.Second
)

var (
	ErrNotFound      = errors.New("monitor not found")
	ErrSlugConflict  = errors.New("monitor slug already exists")
	ErrWriteConflict = errors.New("monitor was concurrently modified")
	ErrArchived      = errors.New("monitor is archived")
)

type Monitor struct {
	ID                uuid.UUID
	Name              string
	Slug              string
	Description       *string
	Kind              string
	URL               string
	HTTPMethod        string
	ExpectedStatusMin int
	ExpectedStatusMax int
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  int
	RecoveryThreshold int
	Enabled           bool
	Public            bool
	Version           int64
	State             string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	StateUpdatedAt    time.Time
	ArchivedAt        *time.Time
}

type CreateInput struct {
	Name              string
	Slug              string
	Description       *string
	Kind              string
	URL               string
	HTTPMethod        string
	ExpectedStatusMin int
	ExpectedStatusMax int
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  int
	RecoveryThreshold int
	Enabled           *bool
	Public            bool
}

type OptionalString struct {
	Set   bool
	Value *string
}

type PatchInput struct {
	Name              *string
	Slug              *string
	Description       OptionalString
	URL               *string
	HTTPMethod        *string
	ExpectedStatusMin *int
	ExpectedStatusMax *int
	Interval          *time.Duration
	Timeout           *time.Duration
	FailureThreshold  *int
	RecoveryThreshold *int
	Public            *bool
}

type ListOptions struct {
	Limit  int
	Offset int
}
