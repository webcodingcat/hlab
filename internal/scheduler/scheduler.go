// Package scheduler runs time-driven work while the cloud runs, for
// example the server status watch and the metrics sampling. It takes a
// Clock so tests can drive time themselves.
package scheduler

import (
	"context"
	"sync"
	"time"
)

// Clock is the source of time of the scheduler and of the modules' jobs.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock returns the wall clock.
func RealClock() Clock { return realClock{} }

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Job is work that runs every Every until the scheduler stops. Run gets
// the scheduler's context and should return soon after it ends.
type Job struct {
	Name  string
	Every time.Duration
	Run   func(ctx context.Context)
}

// Run starts one goroutine per job. Each job runs once per interval, the
// first time one interval after the start; runs of one job never overlap.
// Run returns when ctx ends and every job has returned.
func Run(ctx context.Context, c Clock, jobs []Job) {
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j Job) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case <-c.After(j.Every):
				}
				if ctx.Err() != nil {
					return
				}
				j.Run(ctx)
			}
		}(j)
	}
	wg.Wait()
}
