// Package tasks parses Pulse v2 task definitions.  V2 task domains make the
// authority to execute a command explicit and part of its commitment.
package tasks

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gkgoat1/scripts/commitment"
)

type Domain string

const (
	Scheduled        Domain = "scheduled"
	User             Domain = "user"
	Service          Domain = "service"
	RapidService     Domain = "rapid-service"
	StoppableService Domain = "stoppable-service"
	Disabled         Domain = "disabled"
)

const confirmationProtocol = "tty-pin-v1"
const rapidProtocol = "daemon-rapid-v1"
const pauseProtocol = "tty-lease-v1"

// Task is one v2 task.  Fields are validated strictly by Domain before a Task
// is returned from ParseConfig.
type Task struct {
	ID                 string
	Domain             Domain
	Command            string
	Interval           time.Duration
	MaxLoad1           *float64
	Restart            string
	RestartMinDelay    time.Duration
	RestartMaxDelay    time.Duration
	RestartMaxAttempts int
	ShutdownGrace      time.Duration
	PauseMaxDuration   time.Duration
	Reason             string
}

func (t Task) CommitLeaf() commitment.Leaf {
	fields := map[string]string{"domain": string(t.Domain), "command": t.Command}
	switch t.Domain {
	case Scheduled:
		fields["interval"] = t.Interval.String()
		if t.MaxLoad1 == nil {
			fields["max-load1"] = "absent"
		} else {
			fields["max-load1"] = strconv.FormatFloat(*t.MaxLoad1, 'g', -1, 64)
		}
	case User:
		fields["confirmation-protocol"] = confirmationProtocol
	case Service:
		serviceFields(fields, t)
	case RapidService:
		fields["restart"] = "always"
		fields["restart-min-delay"] = "0s"
		fields["rapid-protocol"] = rapidProtocol
		fields["shutdown-grace"] = t.ShutdownGrace.String()
	case StoppableService:
		serviceFields(fields, t)
		fields["pause-protocol"] = pauseProtocol
		fields["pause-max-duration"] = t.PauseMaxDuration.String()
	case Disabled:
		fields["reason"] = t.Reason
	}
	return commitment.Leaf{Tool: "pulse/task/" + string(t.Domain), ID: t.ID, Kind: commitment.KindCommand, Payload: commitment.EncodeKV(fields)}
}

func serviceFields(fields map[string]string, t Task) {
	fields["restart"] = t.Restart
	fields["restart-min-delay"] = t.RestartMinDelay.String()
	fields["restart-max-delay"] = t.RestartMaxDelay.String()
	fields["restart-max-attempts"] = strconv.Itoa(t.RestartMaxAttempts)
	fields["shutdown-grace"] = t.ShutdownGrace.String()
}

// ParseConfig parses blank-line-delimited key:value task stanzas.
func ParseConfig(r io.Reader) ([]Task, error) {
	var out []Task
	seen := map[string]bool{}
	var values map[string]string
	finish := func() error {
		if values == nil {
			return nil
		}
		t, err := parseTask(values)
		if err != nil {
			return err
		}
		if seen[t.ID] {
			return fmt.Errorf("task %q: duplicate task ID", t.ID)
		}
		seen[t.ID] = true
		out = append(out, t)
		values = nil
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
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("invalid task config line (expected 'key: value'): %q", line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "task" && values != nil {
			if err := finish(); err != nil {
				return nil, err
			}
		}
		if values == nil {
			values = map[string]string{}
		}
		if _, duplicate := values[key]; duplicate {
			return nil, fmt.Errorf("task stanza: duplicate key %q", key)
		}
		values[key] = value
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := finish(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no tasks defined")
	}
	return out, nil
}

func parseTask(v map[string]string) (Task, error) {
	for key := range v {
		switch key {
		case "task", "domain", "command", "interval", "max-load1", "restart", "restart-min-delay", "restart-max-delay", "restart-max-attempts", "shutdown-grace", "pause-max-duration", "reason":
		default:
			return Task{}, fmt.Errorf("task %q: unknown config key %q", v["task"], key)
		}
	}
	t := Task{ID: v["task"], Domain: Domain(v["domain"]), Command: v["command"]}
	if t.ID == "" || t.Domain == "" || t.Command == "" {
		return Task{}, fmt.Errorf("task stanza requires non-empty task, domain, and command")
	}
	has := func(k string) bool { _, ok := v[k]; return ok }
	no := func(keys ...string) error {
		for _, k := range keys {
			if has(k) {
				return fmt.Errorf("task %q (%s): key %q is not valid for this domain", t.ID, t.Domain, k)
			}
		}
		return nil
	}
	parseDuration := func(key string, required bool) (time.Duration, error) {
		raw, ok := v[key]
		if !ok {
			if required {
				return 0, fmt.Errorf("task %q (%s): missing %q", t.ID, t.Domain, key)
			}
			return 0, nil
		}
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 || (required && d == 0) {
			return 0, fmt.Errorf("task %q (%s): invalid %s %q", t.ID, t.Domain, key, raw)
		}
		return d, nil
	}
	parseService := func() error {
		if t.Restart = v["restart"]; t.Restart != "on-failure" {
			return fmt.Errorf("task %q (%s): restart must be %q", t.ID, t.Domain, "on-failure")
		}
		var err error
		if t.RestartMinDelay, err = parseDuration("restart-min-delay", true); err != nil {
			return err
		}
		if t.RestartMaxDelay, err = parseDuration("restart-max-delay", true); err != nil {
			return err
		}
		if t.RestartMaxDelay < t.RestartMinDelay {
			return fmt.Errorf("task %q (%s): restart-max-delay is below restart-min-delay", t.ID, t.Domain)
		}
		n, err := strconv.Atoi(v["restart-max-attempts"])
		if err != nil || n <= 0 {
			return fmt.Errorf("task %q (%s): restart-max-attempts must be positive", t.ID, t.Domain)
		}
		t.RestartMaxAttempts = n
		t.ShutdownGrace, err = parseDuration("shutdown-grace", false)
		if err != nil {
			return err
		}
		if t.ShutdownGrace == 0 {
			t.ShutdownGrace = 30 * time.Second
		}
		return nil
	}
	switch t.Domain {
	case Scheduled:
		if err := no("restart", "restart-min-delay", "restart-max-delay", "restart-max-attempts", "shutdown-grace", "pause-max-duration", "reason"); err != nil {
			return Task{}, err
		}
		var err error
		if t.Interval, err = parseDuration("interval", true); err != nil {
			return Task{}, err
		}
		if raw, ok := v["max-load1"]; ok {
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
				return Task{}, fmt.Errorf("task %q: invalid max-load1 %q", t.ID, raw)
			}
			t.MaxLoad1 = &f
		}
	case User:
		if err := no("interval", "max-load1", "restart", "restart-min-delay", "restart-max-delay", "restart-max-attempts", "shutdown-grace", "pause-max-duration", "reason"); err != nil {
			return Task{}, err
		}
	case Service:
		if err := no("interval", "max-load1", "pause-max-duration", "reason"); err != nil {
			return Task{}, err
		}
		if err := parseService(); err != nil {
			return Task{}, err
		}
	case StoppableService:
		if err := no("interval", "max-load1", "reason"); err != nil {
			return Task{}, err
		}
		if err := parseService(); err != nil {
			return Task{}, err
		}
		var err error
		if t.PauseMaxDuration, err = parseDuration("pause-max-duration", true); err != nil {
			return Task{}, err
		}
	case RapidService:
		if err := no("interval", "max-load1", "restart-max-delay", "restart-max-attempts", "pause-max-duration", "reason"); err != nil {
			return Task{}, err
		}
		if v["restart"] != "always" || v["restart-min-delay"] != "0s" {
			return Task{}, fmt.Errorf("task %q (rapid-service): requires restart: always and restart-min-delay: 0s", t.ID)
		}
		var err error
		t.ShutdownGrace, err = parseDuration("shutdown-grace", false)
		if err != nil {
			return Task{}, err
		}
		if t.ShutdownGrace == 0 {
			t.ShutdownGrace = 30 * time.Second
		}
	case Disabled:
		if t.Reason = v["reason"]; t.Reason == "" {
			return Task{}, fmt.Errorf("task %q (disabled): missing %q", t.ID, "reason")
		}
		if err := no("interval", "max-load1", "restart", "restart-min-delay", "restart-max-delay", "restart-max-attempts", "shutdown-grace", "pause-max-duration"); err != nil {
			return Task{}, err
		}
	default:
		return Task{}, fmt.Errorf("task %q: unknown domain %q", t.ID, t.Domain)
	}
	return t, nil
}

func LoadConfig(path string) ([]Task, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open task config %s: %w", path, err)
	}
	defer f.Close()
	ts, err := ParseConfig(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return ts, nil
}
func DefaultConfigPath() string { return filepath.Join(os.Getenv("HOME"), ".config", "pulse", "tasks") }
