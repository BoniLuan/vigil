package worker

import (
	"time"

	"github.com/BoniLuan/vigil/internal/check"
)

type Observer interface {
	ObserveClaim(count int, err error)
	CheckStarted()
	CheckStopped()
	ObserveCheck(outcome check.Outcome, duration time.Duration)
	CompletionFailed()
	PanicRecovered()
	LeaseStartRejected()
}

type Option func(*Runner)

func WithObserver(observer Observer) Option {
	return func(runner *Runner) {
		if observer != nil {
			runner.observer = observer
		}
	}
}

type noopObserver struct{}

func (noopObserver) ObserveClaim(int, error)                   {}
func (noopObserver) CheckStarted()                             {}
func (noopObserver) CheckStopped()                             {}
func (noopObserver) ObserveCheck(check.Outcome, time.Duration) {}
func (noopObserver) CompletionFailed()                         {}
func (noopObserver) PanicRecovered()                           {}
func (noopObserver) LeaseStartRejected()                       {}
