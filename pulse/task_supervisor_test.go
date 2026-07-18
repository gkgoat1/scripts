package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/gkgoat1/scripts/pulse/tasks"
)

type allowTaskVerifier struct{}

func (allowTaskVerifier) Verify(tasks.Task) (bool, string, error) { return true, "", nil }

func TestSupervisorRapidServiceRestartsAfterSuccess(t *testing.T) {
	runner := &fakeRunner{code: 0}
	var out, errBuf bytes.Buffer
	s := &TaskSupervisor{Runner: runner, Verifier: allowTaskVerifier{}, Out: &out, Err: &errBuf}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx, []tasks.Task{{ID: "fast", Domain: tasks.RapidService, Command: "true"}})
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for runner.callCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop")
	}
	if runner.callCount() < 2 {
		t.Fatalf("calls=%d, want rapid restart", runner.callCount())
	}
}

func TestSupervisorRejectsNonServiceDomains(t *testing.T) {
	runner := &fakeRunner{}
	s := &TaskSupervisor{Runner: runner, Verifier: allowTaskVerifier{}, Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	s.Run(context.Background(), []tasks.Task{{ID: "u", Domain: tasks.User, Command: "true"}, {ID: "d", Domain: tasks.Disabled, Command: "true"}})
	if runner.callCount() != 0 {
		t.Fatalf("calls=%d, want 0", runner.callCount())
	}
}
