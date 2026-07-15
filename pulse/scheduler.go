package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// CommandRunner executes a shell command and reports its exit code.
type CommandRunner interface {
	Run(shellCmd string) (exitCode int, err error)
}

// LoadChecker reports the current 1-minute load average.
type LoadChecker interface {
	Load1() (float64, error)
}

// Ticker abstracts time.Ticker so tests can drive ticks without real time.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Scheduler runs a set of Jobs, one goroutine per job, until its context is done.
type Scheduler struct {
	Runner    CommandRunner
	LoadCheck LoadChecker
	NewTicker func(time.Duration) Ticker
	Out       io.Writer
	Err       io.Writer

	logMu sync.Mutex // guards writes to Out/Err, since jobs fire concurrently
}

// NewScheduler builds a Scheduler from its dependencies.
func NewScheduler(runner CommandRunner, loadCheck LoadChecker, newTicker func(time.Duration) Ticker, out, err io.Writer) *Scheduler {
	return &Scheduler{Runner: runner, LoadCheck: loadCheck, NewTicker: newTicker, Out: out, Err: err}
}

// Run spawns one goroutine per job and blocks until ctx is cancelled and every
// job goroutine has returned (i.e. any in-flight fire has completed).
func (s *Scheduler) Run(ctx context.Context, jobs []Job) {
	var wg sync.WaitGroup
	for _, job := range jobs {
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := s.NewTicker(job.Interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C():
					s.fire(job)
				}
			}
		}()
	}
	wg.Wait()
}

// fire evaluates the optional load gate and, if it passes, runs job.Command once.
func (s *Scheduler) fire(job Job) {
	if job.MaxLoad1 != nil {
		load, err := s.LoadCheck.Load1()
		if err != nil {
			s.logf(s.Err, "[error] %s: load check failed, skipping: %v\n", job.Name, err)
			return
		}
		if load > *job.MaxLoad1 {
			s.logf(s.Out, "[skip] %s: load %.2f > max %.2f\n", job.Name, load, *job.MaxLoad1)
			return
		}
	}

	s.logf(s.Out, "[run] %s: %s\n", job.Name, job.Command)
	code, err := s.Runner.Run(job.Command)
	if err != nil {
		s.logf(s.Err, "[error] %s: %v\n", job.Name, err)
		return
	}
	s.logf(s.Out, "[done] %s: exit %d\n", job.Name, code)
}

// logf writes a formatted line to w, serialized against other jobs' log lines.
func (s *Scheduler) logf(w io.Writer, format string, args ...any) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	fmt.Fprintf(w, format, args...)
}

// --- real implementations, wired in main.go ---

type realCommandRunner struct {
	proxyURL string
}

// proxypassEnv returns a child environment with proxy variables set or merged.
func proxypassEnv(base []string, proxyURL string) []string {
	noProxy := ""
	for _, e := range base {
		if strings.HasPrefix(e, "NO_PROXY=") {
			noProxy = strings.TrimPrefix(e, "NO_PROXY=")
			break
		}
	}
	out := append([]string(nil), base...)
	out = setEnv(out, "HTTP_PROXY", proxyURL)
	out = setEnv(out, "HTTPS_PROXY", proxyURL)
	out = setEnv(out, "NO_PROXY", mergeNoProxyDefaults(noProxy))
	return out
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func mergeNoProxyDefaults(existing string) string {
	defaults := []string{"localhost", "127.0.0.1", "::1", "*.local"}
	seen := make(map[string]bool)
	for _, h := range defaults {
		seen[h] = true
	}
	var extra []string
	if existing != "" {
		for _, h := range strings.Split(existing, ",") {
			h = strings.TrimSpace(h)
			if h != "" && !seen[h] {
				seen[h] = true
				extra = append(extra, h)
			}
		}
	}
	return strings.Join(append(defaults, extra...), ",")
}

func (r realCommandRunner) Run(shellCmd string) (int, error) {
	cmd := exec.Command("sh", "-c", shellCmd)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if r.proxyURL != "" {
		cmd.Env = proxypassEnv(cmd.Environ(), r.proxyURL)
	}
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if status, ok := ee.Sys().(syscall.WaitStatus); ok {
				return status.ExitStatus(), nil
			}
			return 1, nil
		}
		return 0, fmt.Errorf("run %q: %w", shellCmd, err)
	}
	return 0, nil
}

type sysctlLoadChecker struct{}

func (sysctlLoadChecker) Load1() (float64, error) {
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return 0, fmt.Errorf("sysctl vm.loadavg: %w", err)
	}
	return parseLoadavg(string(out))
}

// parseLoadavg parses sysctl's `{ 1.23 1.45 1.60 }` output and returns the
// 1-minute load average (the first value).
func parseLoadavg(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0, fmt.Errorf("unexpected loadavg output: %q", s)
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parse loadavg %q: %w", fields[0], err)
	}
	return v, nil
}

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

func newRealTicker(d time.Duration) Ticker {
	return realTicker{time.NewTicker(d)}
}
