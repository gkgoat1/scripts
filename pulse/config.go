package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Job is one configured periodic job.
type Job struct {
	Name     string
	Interval time.Duration
	Command  string   // passed verbatim to `sh -c`
	MaxLoad1 *float64 // nil = no load gate; else skip firing when 1-min load avg exceeds this
}

// ParseConfig parses the pulse job-stanza format from r: blank-line-delimited
// stanzas of "key: value" lines, "#"-prefixed comment lines ignored.
func ParseConfig(r io.Reader) ([]Job, error) {
	var jobs []Job
	var cur *Job
	seen := map[string]bool{}

	finish := func() error {
		if cur == nil {
			return nil
		}
		if cur.Name == "" {
			return fmt.Errorf("job stanza missing required 'job' field")
		}
		if cur.Interval <= 0 {
			return fmt.Errorf("job %q: missing or non-positive 'interval'", cur.Name)
		}
		if cur.Command == "" {
			return fmt.Errorf("job %q: missing required 'command' field", cur.Name)
		}
		if seen[cur.Name] {
			return fmt.Errorf("job %q: duplicate job name", cur.Name)
		}
		seen[cur.Name] = true
		jobs = append(jobs, *cur)
		cur = nil
		return nil
	}

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			if err := finish(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("invalid config line (expected 'key: value'): %q", line)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		if key == "job" {
			if err := finish(); err != nil {
				return nil, err
			}
			cur = &Job{Name: val}
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("config line %q before any 'job:' stanza", line)
		}
		switch key {
		case "interval":
			d, err := time.ParseDuration(val)
			if err != nil {
				return nil, fmt.Errorf("job %q: invalid interval %q: %w", cur.Name, val, err)
			}
			cur.Interval = d
		case "command":
			cur.Command = val
		case "max-load1":
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return nil, fmt.Errorf("job %q: invalid max-load1 %q: %w", cur.Name, val, err)
			}
			cur.MaxLoad1 = &f
		default:
			return nil, fmt.Errorf("job %q: unknown config key %q", cur.Name, key)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := finish(); err != nil {
		return nil, err
	}

	if len(jobs) == 0 {
		return nil, fmt.Errorf("no jobs defined")
	}
	return jobs, nil
}

// LoadConfig opens path and parses it via ParseConfig.
func LoadConfig(path string) ([]Job, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %s: %w", path, err)
	}
	defer f.Close()
	jobs, err := ParseConfig(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return jobs, nil
}

// defaultConfigPath returns ~/.config/pulse/jobs.
func defaultConfigPath() string {
	return filepath.Join(os.Getenv("HOME"), ".config", "pulse", "jobs")
}
