package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseConfigSingleJob(t *testing.T) {
	const src = `job: llmtrim-restart
interval: 30m
command: killall -9 llmtrim; llmtrim stop && llmtrim start
max-load1: 4.0
`
	jobs, err := ParseConfig(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
	j := jobs[0]
	if j.Name != "llmtrim-restart" {
		t.Errorf("Name = %q", j.Name)
	}
	if j.Interval != 30*time.Minute {
		t.Errorf("Interval = %v", j.Interval)
	}
	if j.Command != "killall -9 llmtrim; llmtrim stop && llmtrim start" {
		t.Errorf("Command = %q", j.Command)
	}
	if j.MaxLoad1 == nil || *j.MaxLoad1 != 4.0 {
		t.Errorf("MaxLoad1 = %v", j.MaxLoad1)
	}
}

func TestParseConfigMultipleStanzasWithComments(t *testing.T) {
	const src = `# comment before first job
job: a
interval: 1m
command: echo a

# comment between jobs
job: b
interval: 2m
command: echo b
`
	jobs, err := ParseConfig(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(jobs))
	}
	if jobs[0].Name != "a" || jobs[1].Name != "b" {
		t.Errorf("job order/names wrong: %+v", jobs)
	}
}

func TestParseConfigNoMaxLoadIsNil(t *testing.T) {
	const src = `job: a
interval: 1m
command: echo a
`
	jobs, err := ParseConfig(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if jobs[0].MaxLoad1 != nil {
		t.Errorf("MaxLoad1 = %v, want nil", jobs[0].MaxLoad1)
	}
}

func TestParseConfigMissingFields(t *testing.T) {
	cases := []string{
		"job: a\ncommand: echo a\n",           // missing interval
		"job: a\ninterval: 1m\n",              // missing command
		"interval: 1m\ncommand: echo a\n",     // missing job
	}
	for _, src := range cases {
		if _, err := ParseConfig(strings.NewReader(src)); err == nil {
			t.Errorf("ParseConfig(%q): want error, got nil", src)
		}
	}
}

func TestParseConfigDuplicateJobName(t *testing.T) {
	const src = `job: a
interval: 1m
command: echo a

job: a
interval: 2m
command: echo b
`
	if _, err := ParseConfig(strings.NewReader(src)); err == nil {
		t.Error("want error for duplicate job name, got nil")
	}
}

func TestParseConfigInvalidInterval(t *testing.T) {
	const src = `job: a
interval: not-a-duration
command: echo a
`
	if _, err := ParseConfig(strings.NewReader(src)); err == nil {
		t.Error("want error for invalid interval, got nil")
	}
}

func TestParseConfigNonPositiveInterval(t *testing.T) {
	const src = `job: a
interval: 0s
command: echo a
`
	if _, err := ParseConfig(strings.NewReader(src)); err == nil {
		t.Error("want error for non-positive interval, got nil")
	}
}

func TestParseConfigInvalidMaxLoad(t *testing.T) {
	const src = `job: a
interval: 1m
command: echo a
max-load1: not-a-float
`
	if _, err := ParseConfig(strings.NewReader(src)); err == nil {
		t.Error("want error for invalid max-load1, got nil")
	}
}

func TestParseConfigKeyBeforeJobStanza(t *testing.T) {
	const src = `command: echo a
job: a
interval: 1m
`
	if _, err := ParseConfig(strings.NewReader(src)); err == nil {
		t.Error("want error for key before any job: line, got nil")
	}
}

func TestParseConfigEmpty(t *testing.T) {
	if _, err := ParseConfig(strings.NewReader("")); err == nil {
		t.Error("want error for empty config, got nil")
	}
	if _, err := ParseConfig(strings.NewReader("# just a comment\n")); err == nil {
		t.Error("want error for comment-only config, got nil")
	}
}

func TestParseConfigCommandPreservesColons(t *testing.T) {
	const src = `job: a
interval: 1m
command: curl https://example.com:8080/path
`
	jobs, err := ParseConfig(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if jobs[0].Command != "curl https://example.com:8080/path" {
		t.Errorf("Command = %q", jobs[0].Command)
	}
}
