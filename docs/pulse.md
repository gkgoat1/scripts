# `pulse` job scheduler (v1)

`pulse` runs a set of named, independently-scheduled shell commands, each on its own interval,
until interrupted. It exists to replace ad hoc `while true; do sleep N; ...; done` loops with a
small, testable, configurable tool — the motivating case is periodically recovering a wedged
local daemon (e.g. `killall -9 llmtrim; llmtrim stop && llmtrim start`), but `pulse` itself knows
nothing about `llmtrim`; that's just one configured job.

## Public API

```go
type Job struct {
    Name     string
    Interval time.Duration
    Command  string   // passed verbatim to `sh -c`
    MaxLoad1 *float64 // nil = no load gate
}

func ParseConfig(r io.Reader) ([]Job, error)
func LoadConfig(path string) ([]Job, error)

type CommandRunner interface{ Run(shellCmd string) (exitCode int, err error) }
type LoadChecker  interface{ Load1() (float64, error) }
type Ticker       interface{ C() <-chan time.Time; Stop() }

type Scheduler struct { /* ... */ }
func NewScheduler(runner CommandRunner, loadCheck LoadChecker, newTicker func(time.Duration) Ticker, out, err io.Writer) *Scheduler
func (s *Scheduler) Run(ctx context.Context, jobs []Job)
```

## Config file format

Default location: `~/.config/pulse/jobs` (override with `-config`). Blank-line-delimited
stanzas of `key: value` lines; `#`-prefixed lines are comments.

```
job: llmtrim-restart
interval: 30m
command: killall -9 llmtrim; llmtrim stop && llmtrim start
max-load1: 4.0
```

| Key | Required | Meaning |
|---|---|---|
| `job` | yes | Unique job name. |
| `interval` | yes | `time.ParseDuration` string (e.g. `30m`, `90s`); must be > 0. |
| `command` | yes | Run verbatim via `sh -c`, so `&&`/`;`/pipes work as written. |
| `max-load1` | no | Skip this tick's run if the 1-minute load average exceeds this value. Absent means no gate at all. |

## Behavior details

- Each job runs on its own ticker, independent of the others.
- The load gate is fail-closed: if the load check itself errors, the job is skipped for that
  tick (logged as `[error]`) rather than run — don't add load to a machine we can't measure.
- A failed or nonzero-exit job is only logged (`[done] <job>: exit N`); there is no retry or
  backoff. The next scheduled tick decides again independently.
- On SIGINT/SIGTERM, no new ticks are honored; a job that's mid-command finishes it, then exits.
  `pulse` prints `[stop] pulse: shutdown complete` only once every job has returned.
- `-once` fires every job a single time immediately (still honoring the load gate) and exits —
  useful for smoke-testing a config before relying on it.

## Install

### Manual (foreground)

```sh
./pulse.sh -once -config ~/.config/pulse/jobs   # smoke-test a config
./pulse.sh -config ~/.config/pulse/jobs          # run until Ctrl-C
```

### Persistent (LaunchAgent)

```sh
./install-pulse.sh
```

Builds `pulse` to `~/.local/bin/pulse`, creates `~/.config/pulse/jobs` with a commented
template if it doesn't already exist, and — once that config has at least one job defined —
installs and loads a LaunchAgent (`com.gkgoat.scripts.pulse`) that starts `pulse` at login and
restarts it on a crash (but not on a clean exit, e.g. from `launchctl unload`). On first run
with an empty template, the installer stops short of loading the agent (an empty config would
make `pulse` exit immediately and launchd would crash-loop it) — edit the config, then re-run
`./install-pulse.sh` to load it.

Logs: `~/Library/Logs/com.gkgoat.scripts.pulse/{stdout,stderr}.log`.

To remove:

```sh
./install-pulse.sh --uninstall
```

Unloads the LaunchAgent, deletes its plist, and removes the installed binary (your config file
at `~/.config/pulse/jobs` is left in place).

The plist install/remove logic lives in `installer/launchagent.sh`, a sourceable shell library
(`launchagent_install` / `launchagent_remove`) styled after `installer/rcblock.sh`, that future
install scripts in this repo can reuse for their own persistent services.

## Out of scope for v1

- Cron-expression scheduling (only fixed intervals).
- Any load-based policy beyond a single per-job ceiling (no historical averaging, no backoff
  curves, no 5/15-minute lookahead).
- Retry/backoff on job failure.
- Multi-node or distributed scheduling.

## Tests

```sh
go test ./pulse/...
bats installer/tests/launchagent.bats
make test
```
