package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/checkresult"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/scheduler"
	"github.com/google/uuid"
)

const (
	DefaultConcurrency     = 5
	MaximumConcurrency     = 100
	DefaultPollInterval    = time.Second
	CompletionSafetyMargin = 5 * time.Second
)

var ErrInvalidConfig = errors.New("invalid worker configuration")

type Config struct {
	Concurrency   int
	PollInterval  time.Duration
	LeaseDuration time.Duration
	ShutdownGrace time.Duration
}

type Claimer interface {
	ClaimDue(context.Context, uuid.UUID, scheduler.ClaimOptions) ([]scheduler.Execution, error)
	CanStartExecution(context.Context, uuid.UUID, uuid.UUID, time.Duration) (bool, error)
}

type MonitorLoader interface {
	Get(context.Context, uuid.UUID) (monitor.Monitor, error)
}

type Checker interface {
	Execute(context.Context, monitor.Monitor) check.Result
}

type Completer interface {
	CompleteExecution(context.Context, uuid.UUID, uuid.UUID, check.Result) (checkresult.StoredResult, error)
}

type Runner struct {
	config     Config
	workerID   uuid.UUID
	claimer    Claimer
	monitors   MonitorLoader
	checker    Checker
	completer  Completer
	logger     *slog.Logger
	completion chan struct{}
	observer   Observer
}

func New(config Config, workerID uuid.UUID, claimer Claimer, monitors MonitorLoader, checker Checker, completer Completer, logger *slog.Logger, options ...Option) (*Runner, error) {
	if config.Concurrency < 1 || config.Concurrency > MaximumConcurrency ||
		config.PollInterval <= 0 || config.LeaseDuration < monitor.MaxTimeout+CompletionSafetyMargin ||
		config.ShutdownGrace <= 0 || workerID == uuid.Nil || claimer == nil || monitors == nil || checker == nil || completer == nil {
		return nil, ErrInvalidConfig
	}
	if logger == nil {
		logger = slog.Default()
	}
	runner := &Runner{
		config: config, workerID: workerID, claimer: claimer, monitors: monitors,
		checker: checker, completer: completer, logger: logger,
		completion: make(chan struct{}, 1), observer: noopObserver{},
	}
	for _, option := range options {
		option(runner)
	}
	return runner, nil
}

// Run polls until ctx is cancelled. Claimed work starts immediately because the
// claim limit always equals currently available execution capacity.
func (r *Runner) Run(ctx context.Context) error {
	jobsCtx, cancelJobs := context.WithCancel(context.Background())
	defer cancelJobs()
	var jobs sync.WaitGroup
	slots := make(chan struct{}, r.config.Concurrency)
	backoff := newErrorBackoff()

	r.logger.Info("worker started", "worker_id", r.workerID, "concurrency", r.config.Concurrency,
		"poll_interval", r.config.PollInterval, "lease_duration", r.config.LeaseDuration)

	for ctx.Err() == nil {
		available := cap(slots) - len(slots)
		delay := r.config.PollInterval
		allowCompletionWake := true
		if available > 0 {
			claims, err := r.claimer.ClaimDue(ctx, r.workerID, scheduler.ClaimOptions{
				BatchSize: available, LeaseDuration: r.config.LeaseDuration,
			})
			r.observer.ObserveClaim(len(claims), err)
			if err != nil {
				delay = backoff.Next()
				allowCompletionWake = false
				r.logger.Warn("scheduler claim failed", "error", err, "retry_in", delay)
			} else {
				backoff.Reset()
				if len(claims) > 0 {
					r.logger.Debug("scheduled executions claimed", "count", len(claims))
				}
				for _, execution := range claims {
					slots <- struct{}{}
					jobs.Add(1)
					go r.runJob(jobsCtx, execution, slots, &jobs)
				}
			}
		}
		if !r.wait(ctx, delay, allowCompletionWake) {
			break
		}
	}

	r.logger.Info("worker shutdown started")
	drained := make(chan struct{})
	go func() {
		jobs.Wait()
		close(drained)
	}()
	timer := time.NewTimer(r.config.ShutdownGrace)
	defer timer.Stop()
	select {
	case <-drained:
		r.logger.Info("worker shutdown complete", "drained", true)
	case <-timer.C:
		r.logger.Warn("worker shutdown grace expired; cancelling active checks")
		cancelJobs()
		<-drained
		r.logger.Info("worker shutdown complete", "drained", false)
	}
	return nil
}

func (r *Runner) runJob(ctx context.Context, execution scheduler.Execution, slots chan struct{}, jobs *sync.WaitGroup) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.observer.PanicRecovered()
			r.logger.Error("scheduled execution panicked; lease left for recovery",
				"execution_id", execution.ID, "monitor_id", execution.MonitorID)
		}
		<-slots
		jobs.Done()
		select {
		case r.completion <- struct{}{}:
		default:
		}
	}()

	configured, err := r.monitors.Get(ctx, execution.MonitorID)
	if err != nil {
		r.logger.Error("load claimed monitor failed", "execution_id", execution.ID,
			"monitor_id", execution.MonitorID, "error", err)
		return
	}
	if !configured.Enabled || configured.ArchivedAt != nil {
		r.logger.Debug("claimed execution no longer runnable", "execution_id", execution.ID,
			"monitor_id", execution.MonitorID)
		return
	}

	requiredLease := configured.Timeout + CompletionSafetyMargin
	canStart, err := r.claimer.CanStartExecution(ctx, execution.ID, r.workerID, requiredLease)
	if err != nil {
		r.logger.Error("verify execution lease failed", "execution_id", execution.ID,
			"monitor_id", execution.MonitorID, "error", err)
		return
	}
	if !canStart {
		r.observer.LeaseStartRejected()
		r.logger.Debug("execution lease has insufficient remaining time", "execution_id", execution.ID,
			"monitor_id", execution.MonitorID, "required_lease", requiredLease)
		return
	}

	r.observer.CheckStarted()
	defer r.observer.CheckStopped()
	result := r.checker.Execute(ctx, configured)
	r.observer.ObserveCheck(result.Outcome, result.Duration)
	if _, err := r.completer.CompleteExecution(ctx, execution.ID, r.workerID, result); err != nil {
		r.observer.CompletionFailed()
		r.logger.Error("complete scheduled execution failed", "execution_id", execution.ID,
			"monitor_id", execution.MonitorID, "outcome", result.Outcome, "error", err)
		return
	}
	r.logger.Debug("scheduled execution completed", "execution_id", execution.ID,
		"monitor_id", execution.MonitorID, "outcome", result.Outcome)
}

func (r *Runner) wait(ctx context.Context, delay time.Duration, allowCompletionWake bool) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	if !allowCompletionWake {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	select {
	case <-ctx.Done():
		return false
	case <-r.completion:
		return true
	case <-timer.C:
		return true
	}
}

type errorBackoff struct{ failures int }

func newErrorBackoff() *errorBackoff { return &errorBackoff{} }

func (b *errorBackoff) Next() time.Duration {
	delays := [...]time.Duration{time.Second, 2 * time.Second, 5 * time.Second}
	index := b.failures
	if index >= len(delays) {
		index = len(delays) - 1
	}
	b.failures++
	return delays[index]
}

func (b *errorBackoff) Reset() { b.failures = 0 }
