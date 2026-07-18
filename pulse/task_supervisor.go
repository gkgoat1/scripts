package main

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gkgoat1/scripts/pulse/tasks"
)

// TaskSupervisor owns long-lived v2 services. It is deliberately the only
// component that can execute a rapid-service restart loop: callers supply
// typed tasks, and only the RapidService domain is allowed to restart after a
// successful exit without delay.
type TaskCommitmentVerifier interface {
	Verify(tasks.Task) (bool, string, error)
}

type TaskSupervisor struct {
	Runner   CommandRunner
	Verifier TaskCommitmentVerifier
	Out      io.Writer
	Err      io.Writer
	logMu    sync.Mutex
}

func (s *TaskSupervisor) logf(w io.Writer, format string, args ...any) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	fmt.Fprintf(w, format, args...)
}

// Run starts only service-shaped domains and blocks until ctx is cancelled and
// their currently executing commands return. User and disabled tasks are never
// accepted here; scheduled v2 tasks are likewise intentionally not services.
func (s *TaskSupervisor) Run(ctx context.Context, all []tasks.Task) {
	var wg sync.WaitGroup
	for _, task := range all {
		if task.Domain != tasks.Service && task.Domain != tasks.RapidService && task.Domain != tasks.StoppableService {
			continue
		}
		task := task
		wg.Add(1)
		go func() { defer wg.Done(); s.runService(ctx, task) }()
	}
	wg.Wait()
}

func (s *TaskSupervisor) runService(ctx context.Context, task tasks.Task) {
	attempts := 0
	for {
		if ctx.Err() != nil {
			return
		}
		ok, reason, err := s.Verifier.Verify(task)
		if err != nil {
			s.logf(s.Err, "[error] %s: commitment check failed, service not started: %v\n", task.ID, err)
			return
		}
		if !ok {
			s.logf(s.Err, "[error] %s: commitment verification failed (%s), service not started\n", task.ID, reason)
			return
		}
		if task.Domain == tasks.RapidService && attempts == 0 {
			s.logf(s.Err, "[warn] %s: committed rapid-service loop enabled: %s\n", task.ID, task.Command)
		}
		s.logf(s.Out, "[service] %s: start: %s\n", task.ID, task.Command)
		code, err := s.Runner.Run(task.Command)
		if err != nil {
			s.logf(s.Err, "[error] %s: service runner: %v\n", task.ID, err)
		}
		if ctx.Err() != nil {
			return
		}

		// rapid-service deliberately restarts after every completion, including
		// exit 0.  The aggregate log prevents a legitimate fast loop from
		// producing unbounded per-iteration logs.
		if task.Domain == tasks.RapidService {
			attempts++
			if attempts == 1 || attempts%1000 == 0 {
				s.logf(s.Out, "[rapid] %s: %d restart(s), last exit %d\n", task.ID, attempts, code)
			}
			continue
		}
		if err != nil || code != 0 {
			attempts++
			if attempts > task.RestartMaxAttempts {
				s.logf(s.Err, "[error] %s: restart limit exhausted\n", task.ID)
				return
			}
			delay := task.RestartMinDelay
			for i := 1; i < attempts && delay < task.RestartMaxDelay; i++ {
				delay *= 2
				if delay > task.RestartMaxDelay {
					delay = task.RestartMaxDelay
				}
			}
			s.logf(s.Out, "[service] %s: exit %d; retry %d/%d in %s\n", task.ID, code, attempts, task.RestartMaxAttempts, delay)
			if !sleepContext(ctx, delay) {
				return
			}
			continue
		}
		s.logf(s.Out, "[service] %s: exit 0; not restarting\n", task.ID)
		return
	}
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
