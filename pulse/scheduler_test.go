package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTicker struct {
	ch      chan time.Time
	mu      sync.Mutex
	stopped bool
}

func newFakeTicker() *fakeTicker { return &fakeTicker{ch: make(chan time.Time, 10)} }

func (f *fakeTicker) C() <-chan time.Time { return f.ch }

func (f *fakeTicker) Stop() {
	f.mu.Lock()
	f.stopped = true
	f.mu.Unlock()
}

func (f *fakeTicker) wasStopped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

func (f *fakeTicker) tick() { f.ch <- time.Now() }

type fakeRunner struct {
	mu    sync.Mutex
	calls []string
	code  int
	err   error
}

func (f *fakeRunner) Run(cmd string) (int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, cmd)
	f.mu.Unlock()
	return f.code, f.err
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeLoadChecker struct {
	load float64
	err  error
}

func (f fakeLoadChecker) Load1() (float64, error) { return f.load, f.err }

func newTestScheduler(runner CommandRunner, loadCheck LoadChecker, newTicker func(time.Duration) Ticker) (*Scheduler, *bytes.Buffer, *bytes.Buffer) {
	var out, errBuf bytes.Buffer
	return NewScheduler(runner, loadCheck, newTicker, &out, &errBuf), &out, &errBuf
}

func TestFireNoLoadGateRuns(t *testing.T) {
	runner := &fakeRunner{code: 0}
	sched, out, _ := newTestScheduler(runner, nil, nil)
	job := Job{Name: "j", Interval: time.Second, Command: "echo hi"}

	sched.fire(job)

	if runner.callCount() != 1 || runner.calls[0] != "echo hi" {
		t.Fatalf("runner.calls = %v", runner.calls)
	}
	if !strings.Contains(out.String(), "[run] j: echo hi") || !strings.Contains(out.String(), "[done] j: exit 0") {
		t.Errorf("out = %q", out.String())
	}
}

func TestFireLoadUnderCeilingRuns(t *testing.T) {
	runner := &fakeRunner{code: 0}
	max := 4.0
	sched, _, _ := newTestScheduler(runner, fakeLoadChecker{load: 1.0}, nil)
	job := Job{Name: "j", Interval: time.Second, Command: "echo hi", MaxLoad1: &max}

	sched.fire(job)

	if runner.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1", runner.callCount())
	}
}

func TestFireLoadOverCeilingSkips(t *testing.T) {
	runner := &fakeRunner{code: 0}
	max := 1.0
	sched, out, _ := newTestScheduler(runner, fakeLoadChecker{load: 5.0}, nil)
	job := Job{Name: "j", Interval: time.Second, Command: "echo hi", MaxLoad1: &max}

	sched.fire(job)

	if runner.callCount() != 0 {
		t.Fatalf("callCount = %d, want 0 (should have been skipped)", runner.callCount())
	}
	if !strings.Contains(out.String(), "[skip] j: load 5.00 > max 1.00") {
		t.Errorf("out = %q", out.String())
	}
}

func TestFireLoadCheckErrorSkips(t *testing.T) {
	runner := &fakeRunner{code: 0}
	max := 1.0
	loadErr := errFake("sysctl broke")
	sched, _, errBuf := newTestScheduler(runner, fakeLoadChecker{err: loadErr}, nil)
	job := Job{Name: "j", Interval: time.Second, Command: "echo hi", MaxLoad1: &max}

	sched.fire(job)

	if runner.callCount() != 0 {
		t.Fatalf("callCount = %d, want 0 (fail-closed on load check error)", runner.callCount())
	}
	if !strings.Contains(errBuf.String(), "[error] j: load check failed") {
		t.Errorf("errBuf = %q", errBuf.String())
	}
}

func TestFireNonzeroExitIsNotLoggedAsError(t *testing.T) {
	runner := &fakeRunner{code: 1}
	sched, out, errBuf := newTestScheduler(runner, nil, nil)
	job := Job{Name: "j", Interval: time.Second, Command: "false"}

	sched.fire(job)

	if !strings.Contains(out.String(), "[done] j: exit 1") {
		t.Errorf("out = %q", out.String())
	}
	if errBuf.Len() != 0 {
		t.Errorf("errBuf = %q, want empty (nonzero exit is not itself an error)", errBuf.String())
	}
}

func TestFireRunnerErrorLogsToStderr(t *testing.T) {
	runErr := errFake("exec failed")
	runner := &fakeRunner{err: runErr}
	sched, _, errBuf := newTestScheduler(runner, nil, nil)
	job := Job{Name: "j", Interval: time.Second, Command: "echo hi"}

	sched.fire(job)

	if !strings.Contains(errBuf.String(), "[error] j:") {
		t.Errorf("errBuf = %q", errBuf.String())
	}
}

func TestSchedulerRunDrivesTicksForOneJob(t *testing.T) {
	ft := newFakeTicker()
	runner := &fakeRunner{code: 0}
	sched, _, _ := newTestScheduler(runner, nil, func(time.Duration) Ticker { return ft })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sched.Run(ctx, []Job{{Name: "j", Interval: time.Second, Command: "echo hi"}})
		close(done)
	}()

	ft.tick()
	ft.tick()
	ft.tick()
	waitForCallCount(t, runner, 3)

	cancel()
	waitForDone(t, done)

	if !ft.wasStopped() {
		t.Error("ticker was not stopped")
	}
}

func TestSchedulerRunDrivesTwoJobsIndependently(t *testing.T) {
	tickerA := newFakeTicker()
	tickerB := newFakeTicker()
	runner := &fakeRunner{code: 0}
	newTicker := func(d time.Duration) Ticker {
		switch d {
		case time.Second:
			return tickerA
		case 2 * time.Second:
			return tickerB
		default:
			t.Fatalf("unexpected interval %v", d)
			return nil
		}
	}
	sched, _, _ := newTestScheduler(runner, nil, newTicker)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sched.Run(ctx, []Job{
			{Name: "a", Interval: time.Second, Command: "echo a"},
			{Name: "b", Interval: 2 * time.Second, Command: "echo b"},
		})
		close(done)
	}()

	tickerA.tick()
	tickerA.tick()
	tickerB.tick()
	waitForCallCount(t, runner, 3)

	cancel()
	waitForDone(t, done)

	countA, countB := 0, 0
	for _, c := range runner.calls {
		switch c {
		case "echo a":
			countA++
		case "echo b":
			countB++
		}
	}
	if countA != 2 || countB != 1 {
		t.Errorf("countA=%d countB=%d, want 2 and 1 (calls=%v)", countA, countB, runner.calls)
	}
}

func TestSchedulerRunCancelsPromptly(t *testing.T) {
	ft := newFakeTicker()
	runner := &fakeRunner{code: 0}
	sched, _, _ := newTestScheduler(runner, nil, func(time.Duration) Ticker { return ft })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sched.Run(ctx, []Job{{Name: "j", Interval: time.Second, Command: "echo hi"}})
		close(done)
	}()

	cancel()
	waitForDone(t, done)

	if !ft.wasStopped() {
		t.Error("ticker was not stopped after cancellation")
	}
}

func TestParseLoadavg(t *testing.T) {
	cases := []struct {
		in      string
		want    float64
		wantErr bool
	}{
		{"{ 1.23 1.45 1.60 }\n", 1.23, false},
		{"{ 1.23 1.45 1.60 }", 1.23, false},
		{"  {  0.50   0.60  0.70  }  ", 0.50, false},
		{"", 0, true},
		{"   ", 0, true},
		{"not a number here", 0, true},
	}
	for _, c := range cases {
		got, err := parseLoadavg(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseLoadavg(%q): want error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseLoadavg(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseLoadavg(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// --- test helpers ---

type errFake string

func (e errFake) Error() string { return string(e) }

func waitForCallCount(t *testing.T, runner *fakeRunner, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if runner.callCount() >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for callCount >= %d (got %d)", want, runner.callCount())
		case <-time.After(time.Millisecond):
		}
	}
}

func waitForDone(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Scheduler.Run did not return within 2s of cancellation")
	}
}
