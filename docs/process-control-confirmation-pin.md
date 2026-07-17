# Process-control confirmation PIN

## Purpose

The `kill`, `pkill`, `killall`, and `osascript` interposers use this component when an invocation
is not permitted by the committed command allowlist. Its purpose is to add a small, explicit
human-presence barrier before a potentially damaging process-control action is delegated.

It is intentionally **not** a CAPTCHA, an identity check, or a complete malware containment
mechanism. It is designed to be low-friction for a person already using an interactive terminal,
while defeating the simple failure mode of malware that continuously writes a fixed confirmation
string to a command's input.

## Protocol

1. The interposer attempts allowlist and commitment verification.
2. An allowlisted invocation with a valid commitment delegates without a PIN.
3. For every other invocation, the interposer opens `/dev/tty` read/write. It fails closed if no
   controlling terminal is available.
4. It samples a fresh uniformly random integer in `[0, 1,000,000)` from Go's `crypto/rand` reader
   and renders it as a zero-padded, six-digit PIN.
5. It displays that PIN on the controlling terminal and reads one line from that same terminal.
6. Delegation occurs only if the entered line exactly matches the displayed six digits.

A new PIN is generated for every protected invocation. It is not saved, reused, accepted from an
environment variable, or configurable as a static secret.

## Why emit the PIN

Earlier confirmation schemes that asked a user to choose a value and repeat it can be satisfied
by a process blindly sending the same line forever. The emitted challenge changes for each
invocation, so a fixed-input loop has only a one-in-a-million chance per attempt and cannot
reliably automate approval without observing and interpreting the terminal output.

Using `/dev/tty`, rather than the wrapped command's stdin, means `yes`, a pipe, redirected stdin,
or a parent process writing to the child pipe cannot provide the answer. A process with access to
the actual controlling terminal (or one able to modify/replace the interposer) is outside this
component's protection boundary.

## Security and usability boundaries

- **Fail closed:** missing TTY, random-source failure, or a mismatched PIN denies the command.
- **No password semantics:** the PIN is a short-lived challenge, not a credential and not proof of
  a particular user's identity.
- **Visible action:** the challenge is printed only after the interposer reports why the action was
  not automatically permitted.
- **No echo suppression:** PINs are confirmation challenges rather than secrets. Keeping terminal
  echo makes the interaction simple and avoids terminal-state recovery risks.
- **No bypass flag:** `--no-interpose` is never honored by these four process-control wrappers.
- **Defense in depth only:** PATH interposition does not stop a hostile program that invokes an
  absolute binary path, changes PATH, controls the terminal, or changes the installed files.
  Use appropriate OS-level controls for adversarial local-code containment.

## Implementation and tests

- Implementation: [`interpose/wrappers/protected_command.go`](../interpose/wrappers/protected_command.go)
- Unit tests: [`interpose/wrappers/protected_command_test.go`](../interpose/wrappers/protected_command_test.go)
- Allowlist/commitment policy: [`interpose/policy/command/`](../interpose/policy/command/)

Run:

```sh
go test ./interpose/...
```