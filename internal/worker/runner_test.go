package worker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/checkresult"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/scheduler"
	"github.com/google/uuid"
)

func TestRunnerBoundsClaimsAndConcurrency(t *testing.T) {
	ids := []scheduler.Execution{job(), job(), job(), job()}
	claimer := &fakeClaimer{jobs: ids}
	checker := newBlockingChecker(4)
	completer := &fakeCompleter{}
	runner := testRunner(t, Config{Concurrency: 2, PollInterval: 5 * time.Millisecond, LeaseDuration: 45 * time.Second, ShutdownGrace: time.Second}, claimer, checker, completer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	checker.waitStarted(t, 2)
	claimer.mu.Lock()
	firstLimit := claimer.limits[0]
	callsWhileFull := len(claimer.limits)
	claimer.mu.Unlock()
	if firstLimit != 2 || callsWhileFull != 1 {
		t.Fatalf("claim limits while full = %v", claimer.limits)
	}
	if got := checker.maximum.Load(); got != 2 {
		t.Fatalf("maximum concurrency = %d, want 2", got)
	}
	checker.release <- struct{}{}
	checker.release <- struct{}{}
	checker.waitStarted(t, 2)
	cancel()
	checker.release <- struct{}{}
	checker.release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if completer.count.Load() != 4 {
		t.Fatalf("completion count = %d", completer.count.Load())
	}
}

func TestRunnerGracefulShutdownDrains(t *testing.T) {
	claimer := &fakeClaimer{jobs: []scheduler.Execution{job()}}
	checker := newBlockingChecker(1)
	runner := testRunner(t, Config{Concurrency: 1, PollInterval: time.Hour, LeaseDuration: 45 * time.Second, ShutdownGrace: time.Second}, claimer, checker, &fakeCompleter{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	checker.waitStarted(t, 1)
	cancel()
	select {
	case <-done:
		t.Fatal("worker exited before active check drained")
	case <-time.After(20 * time.Millisecond):
	}
	checker.release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not drain")
	}
}

func TestRunnerCancelsAfterGrace(t *testing.T) {
	claimer := &fakeClaimer{jobs: []scheduler.Execution{job()}}
	checker := &cancellingChecker{started: make(chan struct{})}
	runner := testRunner(t, Config{Concurrency: 1, PollInterval: time.Hour, LeaseDuration: 45 * time.Second, ShutdownGrace: 20 * time.Millisecond}, claimer, checker, &fakeCompleter{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	<-checker.started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel slow check")
	}
	if !checker.cancelled.Load() {
		t.Fatal("checker context was not cancelled")
	}
}

func TestRunnerIsolatesJobPanic(t *testing.T) {
	claimer := &fakeClaimer{jobs: []scheduler.Execution{job(), job()}}
	checker := &panicOnceChecker{}
	completer := &fakeCompleter{}
	runner := testRunner(t, Config{Concurrency: 2, PollInterval: 5 * time.Millisecond, LeaseDuration: 45 * time.Second, ShutdownGrace: time.Second}, claimer, checker, completer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for completer.count.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if completer.count.Load() != 1 {
		t.Fatalf("completion count = %d", completer.count.Load())
	}
}

func TestErrorBackoff(t *testing.T) {
	backoff := newErrorBackoff()
	for index, want := range []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 5 * time.Second} {
		if got := backoff.Next(); got != want {
			t.Fatalf("delay %d = %s, want %s", index, got, want)
		}
	}
	backoff.Reset()
	if got := backoff.Next(); got != time.Second {
		t.Fatalf("reset delay = %s", got)
	}
}

func testRunner(t *testing.T, config Config, claimer *fakeClaimer, checker Checker, completer Completer) *Runner {
	t.Helper()
	loader := &fakeMonitorLoader{value: runnableMonitor()}
	runner, err := New(config, uuid.New(), claimer, loader, checker, completer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return runner
}

func job() scheduler.Execution {
	return scheduler.Execution{ID: uuid.New(), MonitorID: uuid.New(), Status: scheduler.StatusClaimed}
}
func runnableMonitor() monitor.Monitor {
	return monitor.Monitor{ID: uuid.New(), Enabled: true, Timeout: 100 * time.Millisecond}
}

type fakeClaimer struct {
	mu     sync.Mutex
	jobs   []scheduler.Execution
	limits []int
	err    error
}

func (f *fakeClaimer) ClaimDue(_ context.Context, _ uuid.UUID, options scheduler.ClaimOptions) ([]scheduler.Execution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limits = append(f.limits, options.BatchSize)
	if f.err != nil {
		return nil, f.err
	}
	count := options.BatchSize
	if count > len(f.jobs) {
		count = len(f.jobs)
	}
	result := append([]scheduler.Execution(nil), f.jobs[:count]...)
	f.jobs = f.jobs[count:]
	return result, nil
}
func (*fakeClaimer) CanStartExecution(context.Context, uuid.UUID, uuid.UUID, time.Duration) (bool, error) {
	return true, nil
}

type fakeMonitorLoader struct {
	value monitor.Monitor
	err   error
}

func (f *fakeMonitorLoader) Get(context.Context, uuid.UUID) (monitor.Monitor, error) {
	return f.value, f.err
}

type fakeCompleter struct{ count atomic.Int32 }

func (f *fakeCompleter) CompleteExecution(context.Context, uuid.UUID, uuid.UUID, check.Result) (checkresult.StoredResult, error) {
	f.count.Add(1)
	return checkresult.StoredResult{}, nil
}

type blockingChecker struct {
	started chan struct{}
	release chan struct{}
	active  atomic.Int32
	maximum atomic.Int32
}

func newBlockingChecker(size int) *blockingChecker {
	return &blockingChecker{started: make(chan struct{}, size), release: make(chan struct{}, size)}
}
func (c *blockingChecker) Execute(ctx context.Context, configured monitor.Monitor) check.Result {
	active := c.active.Add(1)
	defer c.active.Add(-1)
	for {
		old := c.maximum.Load()
		if active <= old || c.maximum.CompareAndSwap(old, active) {
			break
		}
	}
	c.started <- struct{}{}
	select {
	case <-c.release:
	case <-ctx.Done():
	}
	now := time.Now().UTC()
	return check.Result{MonitorID: configured.ID, StartedAt: now, FinishedAt: now, Outcome: check.OutcomeSuccess}
}
func (c *blockingChecker) waitStarted(t *testing.T, count int) {
	t.Helper()
	for range count {
		select {
		case <-c.started:
		case <-time.After(time.Second):
			t.Fatal("check did not start")
		}
	}
}

type cancellingChecker struct {
	started   chan struct{}
	cancelled atomic.Bool
}

func (c *cancellingChecker) Execute(ctx context.Context, configured monitor.Monitor) check.Result {
	close(c.started)
	<-ctx.Done()
	c.cancelled.Store(true)
	now := time.Now().UTC()
	return check.Result{MonitorID: configured.ID, StartedAt: now, FinishedAt: now, Outcome: check.OutcomeTimeout}
}

type panicOnceChecker struct{ calls atomic.Int32 }

func (c *panicOnceChecker) Execute(_ context.Context, configured monitor.Monitor) check.Result {
	if c.calls.Add(1) == 1 {
		panic("test panic")
	}
	now := time.Now().UTC()
	return check.Result{MonitorID: configured.ID, StartedAt: now, FinishedAt: now, Outcome: check.OutcomeSuccess}
}
