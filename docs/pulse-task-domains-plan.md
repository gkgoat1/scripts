# Plan: committed Pulse task domains and explicit run authorities

## Goal

Extend `pulse` beyond its present periodic-job model without turning it into a
second arbitrary-command launcher.  The result must support six **disjoint
committed task domains**:

1. **scheduled** — the current fixed-interval job model;
2. **user** — a task that runs only after an explicit interactive user request;
3. **service** — a continuously running child process, deliberately not a
   periodic schedule, with bounded failure-only restart;
4. **rapid-service** — a continuously running child that deliberately respawns
   as fast as it exits, including successful exits, but only through daemon
   supervision of its exact committed definition;
5. **stoppable-service** — a continuously running supervised child which an
   interactively confirmed user may temporarily stop while one wrapped client
   subprocess executes; and
6. **disabled** — a retained task definition that has no execution authority.

The last domain is useful for preserving a known-good but currently problematic
command without deleting it or making it runnable.  A scheduled task is
user-disabled by default: it cannot be converted into a manual action merely
by passing its name to a command-line flag.  Likewise, a user task is
schedule-disabled, a service is neither a ticker job nor a manually
spawnable command.  `rapid-service` is deliberately the only domain with
unbounded immediate respawn authority, and only a `stoppable-service` can
receive the narrow temporary-stop authority described below.

The names are intentionally domains rather than a pair of ambiguous boolean
flags.  A task belongs to exactly one domain, and its domain is part of its
commitment.  This makes a config/proof for one execution authority unusable in
another one.

## Non-goals and security boundary

- Do not add `pulse run 'arbitrary shell text'`, an environment-variable command
  override, a generic RPC execution endpoint, or a way to select a task's
  command line at invocation time.
- Do not use task names as shell text.  A request selects one exact
  already-parsed, committed task ID.
- Do not treat a Merkle commitment as user presence.  It authorizes a *specific
  configuration*, not every process on the machine to trigger that
  configuration whenever it wants.
- This is defence against an untrusted/buggy ordinary program repeatedly
  invoking a convenient local helper.  A hostile program with the same user,
  unrestricted ability to rewrite binaries/configuration, control the real
  terminal, or unload/replace the LaunchAgent remains outside the protections
  already documented for `agentcommit` and the process-control interposers.

The existing anchor is still the trust root.  If the anchor is not installed,
`pulse` keeps its existing backwards-compatible behaviour for legacy
scheduled configs.  The new domains must **not** silently operate unrestricted
when the anchor is absent: creating any v2-domain config requires adopting the
anchor first.

## Task model

Introduce a v2 task definition with an explicit `domain` and only the fields
valid for that domain.  Use a new config path (for example
`~/.config/pulse/tasks`) rather than changing the interpretation of the
existing `~/.config/pulse/jobs` file in place.  Existing jobs remain legacy
scheduled jobs until an operator deliberately migrates and recommits them.

Illustrative format (the exact parser format can remain stanza-based):

```text
# Periodic task: scheduler authority only.
task: trim-restart
domain: scheduled
interval: 30m
command: llmtrim stop && llmtrim start
max-load1: 4.0

# On-demand task: an interactive, confirmed user request only.
task: inspect-trim
domain: user
command: llmtrim status

# One long-lived child: no ticker and no `pulse run` access.
task: local-indexer
domain: service
command: indexer --foreground
restart: on-failure
restart-min-delay: 30s
restart-max-delay: 10m
restart-max-attempts: 3

# Explicitly authorized rapid respawn: preserves a legitimate spam-loop.
task: event-drainer
domain: rapid-service
command: event-drainer --one-batch
restart: always
restart-min-delay: 0s

# A supervised service that may be paused only during one confirmed wrapper.
task: dev-database
domain: stoppable-service
command: local-db --foreground
restart: on-failure
restart-min-delay: 5s
restart-max-delay: 5m
restart-max-attempts: 3
pause-max-duration: 2h

# Preserve the known command but grant no execution authority.
task: old-repair
domain: disabled
command: old-repair --unsafe
reason: incompatible with current data layout
```

### Validation rules

- `task`, `domain`, and `command` are required.  Task IDs are unique across the
  whole v2 config, not merely within a domain.
- `domain` is exactly one of `scheduled`, `user`, `service`,
  `stoppable-service`, or `disabled`.  Unknown domains fail parsing; a typo
  must never fall back to a runnable mode.
- `scheduled` requires a positive `interval`; it alone may specify
  `max-load1`.  It rejects service and user-confirmation keys.
- `user` rejects `interval`, restart, and service keys.  It is never included
  in ticker startup or `-once`.
- `service` rejects interval/load and pause keys and is started once as a
  supervised child.  It cannot be selected by a user command.  Its complete
  restart policy is required and bounded.
- `stoppable-service` has the same required, bounded service restart policy,
  plus a positive, finite `pause-max-duration`.  It rejects interval/load and
  user-command fields.  It is the only domain eligible for a temporary stop
  lease; ordinary `service` is deliberately not implicitly stoppable.
- `rapid-service` requires exactly `restart: always` and exactly
  `restart-min-delay: 0s`; it rejects max-delay/max-attempts, pause, interval,
  load, and user-command fields.  This intentionally permits an unbounded
  immediate respawn loop, including after a zero exit.  It is a separate,
  conspicuous domain so it cannot arise by weakening ordinary service policy.
- `disabled` rejects all run-policy fields.  It is loaded and committed, but
  never handed to a runner under any condition.
- Reject duplicate keys, empty values where a value is required, unknown keys,
  and non-finite or negative numeric settings.  No partially valid config
  should start a subset of tasks.

`disabled` deliberately retains the command in the config and commitment.  It
therefore has an auditable identity and can be re-enabled only by changing its
domain and producing a new operator-visible anchor root.

## Runtime authorities

Split the current monolithic scheduler into explicit dispatchers.  Each accepts
only its domain's typed task collection; do not rely on callers remembering to
filter a common `[]Task`.

| Domain | Starts it | Does not start it |
|---|---|---|
| `scheduled` | the resident Pulse daemon after its interval expires; an administrative `-once-scheduled` smoke test | `pulse run`, service supervisor |
| `user` | `pulse run <task-id>` after an interactive confirmation | tickers, `-once`, service supervisor |
| `service` | resident daemon startup, then its committed bounded restart policy | tickers, `pulse run`, `-once`, stop-wrapper client |
| `rapid-service` | resident daemon startup, then its committed `always`/zero-delay respawn loop | tickers, `pulse run`, `-once`, stop-wrapper client, any client request |
| `stoppable-service` | resident daemon startup, then its committed bounded restart policy; a confirmed temporary-stop lease releases it back to supervision | tickers, `pulse run`, `-once`, an unconfirmed/direct stop request |
| `disabled` | nobody | every dispatcher |

### Scheduled dispatcher

This is the current `Scheduler` behaviour, limited to `scheduled` tasks.  It
performs commitment verification immediately before every fire, then applies
the committed load gate.  Rename the broad `-once` flag to
`-once-scheduled` (retain `-once` as a documented deprecated alias during one
migration release) and make it select scheduled tasks only.  It must neither
start user tasks nor services.

### User dispatcher

Provide a narrow client command:

```sh
pulse run <user-task-id>
```

It is a request by opaque task ID only.  It must not accept `--command`,
arguments to append, stdin-provided task definitions, domain overrides, or a
wildcard/all-tasks option.

Before a spawn, the daemon/client must:

1. load the v2 config and identify an exact `user` task;
2. read the anchor and proof fresh and verify that task's *user-domain* leaf;
3. open `/dev/tty` for both challenge output and input; fail closed if absent;
4. display the task ID and the committed command, generate a fresh
   cryptographically random confirmation PIN, and require the exact PIN from
   that controlling TTY; and
5. execute the exact committed command once only after successful confirmation.

Use the same `/dev/tty` and fresh challenge principles as
`docs/process-control-confirmation-pin.md`, but keep the implementation and
messages domain-specific.  The confirmation is intentionally per execution:
a background process can invoke `pulse run` repeatedly, but it cannot answer a
new challenge through a pipe or scripted stdin.  Failed/cancelled/no-TTY
requests must not spawn a child and should be rate-limited in logging to avoid
log flooding.

A future non-terminal UI may be supported only through a separate,
user-presence-capable approval protocol.  It must not turn the Unix socket or
an environment token into unattended authority.

### Stoppable-service wrapper

A `stoppable-service` is a service with one narrowly scoped extra authority:
an interactively confirmed user can suspend its supervision **only while one
explicit wrapper command is running as a subprocess of the requesting Pulse
client**.  It is not a general “stop service” command, and it does not turn an
ordinary service into a user-controlled one.

Expose that authority only through an argv-preserving wrapper form:

```sh
pulse with-service-stopped <stoppable-service-id> -- <wrapped-cli> [arg ...]
```

The `--` separator and at least one wrapped argv element are mandatory.  The
client executes that exact argv directly with `exec.Command` (not `sh -c`), as
its child/process group; Pulse never receives a shell command string, never
runs the wrapper in the daemon, and never accepts a detached/background
execution option.  The wrapper is intentionally not committed: it is the
interactive user's one-off CLI invocation, not a new daemon execution
authority.  Its complete argv, escaped unambiguously, is displayed in the
confirmation prompt so the user approves both the named service pause and the
specific work to be done while it is paused.

The protocol must bind the pause to the actual client subprocess rather than a
client assertion:

1. The client connects to the daemon and passes an open controlling-terminal
   FD over the private Unix socket using `SCM_RIGHTS`; no TTY or FD-passing
   failure is a denial.
2. The daemon loads the task, requires exactly the `stoppable-service` domain,
   freshly verifies its full leaf, and writes a fresh cryptographic PIN plus
   the task ID and wrapper argv to that passed TTY FD.  The daemon itself reads
   the matching PIN from that FD.  A socket client must never be trusted to
   claim it already confirmed a challenge.
3. Only after confirmation, the daemon stops and reaps the currently managed
   service process group, suppresses restart for a single lease, and returns a
   one-use lease bound to that live socket connection and task ID.
4. The client spawns the wrapper as its subprocess and keeps the lease
   connection open until `Wait` returns.  It must close the lease before exit
   on every normal/error path.  The daemon resumes supervision immediately
   when the wrapper-complete message is accepted, the connection closes, or
   the committed `pause-max-duration` expires—whichever happens first.

The daemon must not accept a PID, an arbitrary “keep stopped” heartbeat, a
renewal, a `--detach` flag, or a claimed exit status as proof that the wrapper
is still running.  A client can close its connection early, but that only
causes the safe outcome—prompt service restart.  A malicious client that holds
an open connection can delay restart only until the committed maximum pause;
it cannot cause another command to run through Pulse.  On daemon shutdown,
the lease is discarded and normal shutdown semantics apply; it must not leave
a persistent stopped state.

A stop request during a service restart race serializes with the service state
machine: after confirmation, it either terminates/reaps the one managed child
or records the lease before the next spawn.  There must be no interval in
which a restart can escape a valid lease, and no concurrent leases for one
task.  A second request for a leased task is denied rather than extending or
stacking pauses.

### Service supervisor

A `service` or `stoppable-service` task represents one foreground process
intended to remain running; it is not a short command repeatedly fired as fast
as it exits.  At daemon startup, verify the appropriate service-domain leaf
and start one child.  Track it by task ID and refuse a second concurrent
instance.

Restart behaviour is security-relevant and must be constrained by committed
policy.  For `service` and `stoppable-service`, use `on-failure` only, an
exponential delay bounded by the committed minimum/maximum, and a small
committed maximum attempt count.  Once exhausted, mark the service failed and
require an operator config change/recommit or a future separately-confirmed
recovery action.

`rapid-service` is the explicit exception, intended to migrate legitimate
existing `while true`/spam-loop workloads without losing the commitment model.
It restarts after **every** exit, including exit 0, with the committed zero
delay.  The daemon is its exclusive runner: no CLI request, scheduler tick,
or child-controlled flag can create the loop, alter its command, or borrow this
authority for another task.  Its separate domain, committed `restart: always`
and committed zero delay make the high-rate execution capability visible in the
anchor change and prevent a normal service from being silently transformed into
a rapid loop.  It must emit a clearly marked startup/audit warning containing
its task ID and committed command, plus rate-limited aggregate exit/restart
telemetry; do not log one unbounded line per iteration.

Never implement unbounded immediate respawn for `service` or
`stoppable-service`: that would recreate the “random program spams suspending
operations” failure mode by a policy edit that looks like ordinary service
configuration.

On Pulse shutdown, stop accepting new work, terminate/reap managed children
according to a documented grace period, and do not restart them.  Use process
ownership/process-group handling so a shell service does not leave an
untracked child behind.

## Commitment design

The present leaf (`Tool: "pulse"`, `ID: job name`, payload only `command`) is
not sufficient: it permits an otherwise valid command leaf to be reinterpreted
as a different trigger authority, and it does not bind interval or restart
rate.  Add dedicated domain-separated task leaves.

Define stable tool namespaces, for example:

```text
Tool: "pulse/task/scheduled"
Tool: "pulse/task/user"
Tool: "pulse/task/service"
Tool: "pulse/task/rapid-service"
Tool: "pulse/task/stoppable-service"
Tool: "pulse/task/disabled"
ID:   <task-id>
Kind: command
```

Each `Task.CommitLeaf()` must also include a canonical `domain` field in its
length-prefixed payload.  This intentionally duplicates the namespace: the
`Tool` value prevents tree/proof key confusion and the payload makes an
incorrect `CommitLeaf` call fail as well.  Commit all behaviour that changes
whether, when, or how often a command can run:

| Domain | Committed payload fields |
|---|---|
| `scheduled` | `domain`, `command`, `interval`, and the explicit presence/value of `max-load1` |
| `user` | `domain`, `command`, and confirmation protocol version/policy |
| `service` | `domain`, `command`, restart mode, minimum/maximum delay, maximum attempts, and shutdown grace period |
| `rapid-service` | `domain`, `command`, `restart: always`, `restart-min-delay: 0s`, rapid-loop protocol version, and shutdown grace period |
| `stoppable-service` | `domain`, `command`, restart mode, minimum/maximum delay, maximum attempts, shutdown grace period, pause protocol version, and `pause-max-duration` |
| `disabled` | `domain`, `command`, and `reason` |

The scheduling fields are intentionally now committed, unlike legacy Pulse
jobs.  Changing `30m` to `1ms`, removing a load gate, or changing a restart
limit is an execution-rate/availability change and must receive the same
operator-visible recommit as changing the command.

Revise proof-sidecar indexing before adding the new leaves.  `ProofFile.Entries`
currently uses `Leaf.ID` (a job name), which is insufficient should IDs ever
be shared across domains.  Key entries by the complete canonical `Leaf.Key()`
(or a dedicated unambiguous `tool + domain + ID` encoding), and have every
verifier request its own full key.  Do not use a map lookup by raw task name or
fall back to another domain's proof entry.

`agentcommit commit` must parse v2 tasks, append all six domains' leaves to
the same anchored Merkle tree, and write the v2 proof sidecar atomically.  A
single root is desirable; separate domains are distinguished inside that root
by their leaf identities, not by four independent anchors that could get out
of sync.  The output should report counts per domain, including disabled
entries, so an operator can see what remains executable.

For the transition, retain legacy `pulse` job leaves and their sidecar until
migration is complete.  Their tool identity remains unchanged so existing
roots keep verifying.  Never reinterpret a legacy leaf as a v2 leaf or use a
legacy proof to verify a v2 task.

## Daemon/client shape and anti-spam controls

Keep one resident daemon as the only component that owns scheduled timers and
service children.  It should hold an exclusive, non-blocking runtime lock; a
second daemon exits without executing any task.  The `run` command is a
client, not a second scheduler: it connects to the daemon over a per-user Unix
socket in a `0700` runtime directory and submits only the selected ID.  The
daemon performs the fresh verification and the TTY challenge itself (or passes
an already-open terminal FD with a narrowly specified protocol).

The socket's filesystem permissions are useful hygiene but are not presented
as authentication against the same UID.  The interactive challenge is the
actual barrier for user-domain dispatch.  Design the protocol so a client
cannot claim “already confirmed”, send a command string, request another
domain, or ask the daemon to run several times.

For scheduled, service, rapid-service, and stoppable-service domains, random
client programs have no dispatch endpoint at all.  Their only execution paths
are the daemon's committed timer or service lifecycle, except for the
intentionally narrow, confirmed temporary-stop lease of a stoppable service.
In particular, only the daemon's normal lifecycle can cause a rapid-service to
loop: a random program cannot ask Pulse to initiate, accelerate, or redirect
such a loop.  The exclusive lock prevents a casual second `pulse`
invocation from duplicating that work while the daemon is healthy.  Document
the same-UID adversary limitation rather than making a false promise that a
lock file can defeat a malicious process able to remove it.

Keep process-control interposition in force for child commands.  A user-domain
confirmation does not automatically relax the committed `kill`/`pkill`/
`killall`/`osascript` allowlist.  Any desired interaction between Pulse's
confirmed dispatch and those wrappers must be a separately designed,
explicitly scoped capability—not an environment-variable bypass that a child
can forward or forge.

## Migration and compatibility

1. Add v2 parser/types and commitment helpers without changing legacy
   `pulse/config.Job`, `jobs`, or legacy `-once` behaviour.
2. Add `pulse tasks validate` / `agentcommit` diagnostics that display the
   canonical domain, task ID, and exact committed policy before an operator
   enables the daemon.
3. Provide a migration command that converts each legacy job to a v2
   `scheduled` stanza, preserving interval/load settings.  It writes a new
   file; it does not overwrite the legacy config.
4. Require `agentcommit commit` and anchor reload after migration.  Start v2
   domains only when both the v2 proof sidecar and an installed anchor verify.
5. Run legacy and v2 scheduled dispatchers only during the explicitly
   documented migration window, with a startup warning if a command appears in
   both.  Prefer refusing duplicate command/task IDs to silently double-run.
6. Remove the legacy compatibility path only in a separately announced major
   version; leaving an uncommitted fallback would defeat the new authority
   separation.

## Implementation work

1. **Types/parser** — add a `pulse/tasks` package with `Domain`, typed task
   policy structs, strict stanza parsing, canonical validation, and tests for
   illegal field/domain combinations.  Model `rapid-service` as a distinct
   type, not a boolean on the generic service policy.
2. **Leaves/proofs** — add domain-specific `CommitLeaf` methods; change proof
   sidecar entry identity to full leaf keys; update `agentcommit/commit.go`,
   installation scripts, and reports.  Maintain a versioned sidecar decoder so
   old proof files fail safely for v2 rather than being misread.
3. **Verification** — generalize `pulse/verify.go` to verify the typed v2
   task's complete leaf on every execution decision.  Missing, malformed, or
   cross-domain proof entries skip/deny execution.  Anchor read failures remain
   fail closed after adoption.
4. **Dispatchers** — factor `Scheduler.fire` into a shared exact-command
   execution primitive plus domain-specific scheduled/user/service
   dispatchers.  Make its input typed and private enough that a caller cannot
   bypass the domain gate with a plain `Job`.
5. **Stop-wrapper protocol** — implement `with-service-stopped`, Unix FD
   passing, daemon-owned TTY confirmation, single-use connection-bound leases,
   process-group stop/reap, and race-free resumption.  Spawn the user-supplied
   argv only in the client as a direct child; the daemon never shells or
   executes it.
6. **Daemon protocol** — implement the lock, socket lifecycle, small
   allowlisted request schema, cancellation handling, and structured audit
   logs.  Avoid placing the socket in a shared world-writable directory without
   a private parent.
7. **Service management** — implement one-child tracking, bounded restart
   state, lease-aware restart suppression, clean shutdown/reaping, a persistent
   failed state with clear operator diagnostics, and a separate daemon-only
   rapid-service loop with aggregate rate-limited telemetry (not per-iteration
   logs).
8. **Migration** — provide an explicit conversion for a legacy spam loop to a
   `rapid-service` stanza.  It must require an operator review/recommit and
   clearly report the high-rate authority; never infer this domain merely from
   a short legacy interval or a command's exit behavior.
9. **CLI/docs/install** — update `pulse/main.go` commands and help,
   `docs/pulse.md`, `docs/agentcommit.md`, `install-pulse.sh`, and LaunchAgent
   arguments.  Ensure the LaunchAgent starts only the daemon; it must never
   invoke a user task or a broad `-once` mode.

## Test plan

### Parser and commitments

- Accept one valid task of every domain; reject unknown domains, duplicate IDs,
  mixed-domain fields, missing required policy, invalid durations, and a
  disabled task with run settings.
- Demonstrate that equal task ID/command values in all six domains have six
  different leaf keys and hashes.
- Verify that a proof for `scheduled/x` cannot validate `user/x`, `service/x`,
  `rapid-service/x`, `stoppable-service/x`, or `disabled/x`; likewise reject
  payload domain, interval/load, confirmation, restart-policy, rapid-loop, and
  pause-policy tampering.
- Confirm disabled tasks are included in the root/proof sidecar but have no
  runner call.
- Test old and new sidecar versions independently, including malformed or
  missing sidecars and anchor read failures.

### Dispatch and abuse resistance

- Scheduled tasks fire only from their ticker; `pulse run scheduled-id` is
  rejected even with a valid proof.
- User tasks do not fire on ticker or scheduled `-once`; `pulse run user-id`
  refuses a missing TTY, wrong PIN, EOF, repeated client requests, unknown ID,
  and every non-user domain.
- A successful user confirmation runs exactly the committed command once;
  prove that appended arguments, supplied command text, and a client-declared
  domain never reach a shell.
- A `rapid-service` restarts after both success and failure without delay only
  when its exact committed rapid-domain leaf verifies.  Prove no `service`,
  `stoppable-service`, scheduled task, user task, or socket client can obtain
  that behavior by configuration field reuse, a CLI flag, or a client request.
  Assert rate-limited aggregate telemetry under a high iteration count.
- A `stoppable-service` may be selected only by
  `with-service-stopped`; direct stop requests and normal `pulse run` are
  rejected.  It verifies its leaf, refuses a missing/wrong/EOF TTY challenge,
  displays wrapper argv, and never supplies that argv to a shell or daemon.
- During a granted lease the service is stopped/reaped exactly once; the wrapper
  is a direct client child; service supervision resumes on wrapper completion,
  client/socket loss, or the committed deadline.  Test race cases at service
  exit/restart, denial of a second lease, and a client that tries to retain a
  socket past its pause deadline.
- A second daemon cannot acquire the runtime lock or fire a duplicate schedule.
- Service starts once, never overlaps itself, applies committed backoff,
  stops after its committed retry limit, and is reaped on daemon shutdown.
  Include a fast-failing service regression test to prove no tight respawn
  loop is possible.
- Re-run commitment verification immediately before every scheduled fire,
  user dispatch, service start/service restart, rapid-service start/restart,
  stoppable-service start/restart, and temporary-stop lease; a changed
  config/proof must prevent the next spawn or stop without stopping unrelated
  valid tasks.
- Regression-test the current legacy periodic behaviour until its planned
  removal, including `-once`, load gates, proxy environment injection, and
  SIGINT/SIGTERM shutdown.

## Acceptance criteria

The feature is ready only when an operator can inspect one anchor root and see
all six task domains committed; a committed command cannot be moved between
those domains without a new root; only a fresh interactive confirmation can
run a user task; a rapid loop can arise only from an explicitly committed
`rapid-service` under daemon supervision; a stoppable service can be paused
only for the lifetime of one confirmed client child and never indefinitely;
disabled tasks have no code path to a runner; and neither a client loop nor a
policy change to an ordinary service can turn a fixed committed action into an
unbounded stream of process-suspending operations.