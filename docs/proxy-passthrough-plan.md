# Plan: opt-out child passthrough proxy for `gitall` and `pulse`

## Goal

Give `gitall` and `pulse` an optional, opt-out ability to expose a temporary
HTTP(S) proxy for their children. When enabled, the parent tool (`gitall` or
`pulse`) listens on a loopback port and injects `HTTP_PROXY`/`HTTPS_PROXY`
into every child process. The proxy does not inspect or rewrite traffic; it
forwards it through the parent instance, so a separate, existing firewall can
intercept and log **child** traffic separately from **parent** traffic without
creating a new firewall setup.

## Scope constraints

- **Existing firewall only.** This plan does not create, configure, or
  replace any firewall, packet filter, or proxy server.
- **Opt-out for gitall and pulse.** The feature is built into `gitall` and
  `pulse`; it is disabled by default and enabled by an explicit flag.
- **Temporary proxy.** The proxy starts with the parent process, binds only to
  loopback, and stops when the parent exits.
- **Passthrough semantics.** The parent does not decrypt, cache, or modify
  child HTTP/HTTPS requests. For HTTPS it performs `CONNECT` tunneling; for
  HTTP it forwards the raw request/response.

## Motivation

`gitall` spawns many concurrent `git` processes that talk to many remote
hosts. `pulse` spawns arbitrary periodic commands, some of which make HTTP(S)
requests. In an environment where outbound traffic is intercepted by an
existing firewall, all traffic appears to come from the user account running
`gitall`/`pulse`. With the passthrough proxy, the firewall can see and tag
**parent ↔ upstream** connections separately from **child ↔ parent**
connections, improving observability and tightening scope: children connect
inward to the parent, the parent connects outward.

## User-visible behavior

### Activation

```sh
# gitall: proxy every child git process
gitall -proxy push ...

# pulse: proxy every job command
pulse -proxy
```

Both tools accept `-proxy` as an opt-in flag. The flag takes no argument; the
proxy binds to a random loopback port chosen by the OS. Logging prints
something like:

```text
[proxy] gitall: child HTTP(S) proxy on 127.0.0.1:54321
```

### Environment variables injected into children

When `-proxy` is active, each child process receives:

```text
HTTP_PROXY=http://127.0.0.1:54321
HTTPS_PROXY=http://127.0.0.1:54321
NO_PROXY=localhost,127.0.0.1,::1,*.local
```

The parent does **not** modify the user’s own process environment, nor does it
set `http_proxy`/`https_proxy` globally.

### Opt-out / escape hatch

A child can bypass the proxy by clearing the proxy variables in its own
shell command, e.g.:

```sh
HTTPS_PROXY= HTTP_PROXY= curl https://example.com
```

No authentication or filtering is enforced by the parent; it merely forwards
what it receives.

## Architecture

### Process model

```text
┌─────────────────┐
│   gitall/pulse  │  parent process
│   -proxy        │  listens on 127.0.0.1:P
└────────┬────────┘
         │ loopback proxy traffic (taggable by firewall as child-facing)
┌────────▼────────┐
│   existing      │  firewall / network inspection layer
│   firewall      │
└────────┬────────┘
         │ parent ↔ upstream (taggable separately)
   [ upstream hosts ]
```

### Proxy implementation

A new internal package, `internal/proxypass`, provides a self-contained
passthrough HTTP/HTTPS proxy.

#### Responsibilities

- Bind a loopback TCP listener on `127.0.0.1:0`.
- Accept HTTP proxy requests.
- For `CONNECT host:port`: respond `200 Connection established`, then shuttle
  raw bytes between client and upstream.
- For plain `http://host/path`: read the incoming request, dial upstream, and
  stream both directions without rewriting bodies.
- Honor context cancellation: stop accepting new connections and close the
  listener when the parent context is cancelled.

#### Public API

```go
package proxypass

import "net/url"

type Server struct{ /* ... */ }

// Start binds a loopback listener and returns a server.
// The returned URL is safe to pass to HTTP_PROXY/HTTPS_PROXY.
func Start(ctx context.Context) (*Server, error)

// URL returns the http://127.0.0.1:port address of the proxy.
func (s *Server) URL() *url.URL

// Addr returns the bound listen address for logging.
func (s *Server) Addr() string

// Close stops accepting new connections and waits briefly for active ones.
func (s *Server) Close() error
```

#### Key implementation notes

- Use `net/http` server with a custom `Handler` for plain HTTP and an
  explicit `CONNECT` handler for tunneling.
- Do not add custom headers, signatures, or request rewriting.
- Do not buffer request/response bodies; use `io.Copy` goroutines.
- Keep `IdleTimeout` short (e.g. 60s) so abandoned connections do not delay
  parent shutdown.
- Bind to `127.0.0.1` (IPv4 loopback) only; optionally allow `[::1]` if
  `NO_PROXY` is adjusted, but IPv4 keeps the default `NO_PROXY` simple and
  predictable.

## Configuration surface

### New flags

| Tool   | Flag    | Default | Meaning |
|--------|---------|---------|----------|
| gitall | `-proxy` | false | Start a loopback passthrough proxy and inject it into child `git` processes |
| pulse  | `-proxy` | false | Start a loopback passthrough proxy and inject it into each job shell |

No new config-file keys are required. If future versions want to persist the
preference, they can add a key to `~/.config/interpose/config`, but v1 stays
flag-only so the behavior remains explicitly opt-in per invocation.

### Environment inherited by children

When `-proxy` is active, the parent augments the child environment with the
proxy variables. Existing values are **overridden** (because the parent owns
that invocation scope), except `NO_PROXY`, which is merged with any user value
that is already present.

Pseudocode for merging:

```go
func childEnv(parent, proxyURL string, base []string) []string {
    env := append([]string(nil), base...)
    env = setOrReplace(env, "HTTP_PROXY", proxyURL)
    env = setOrReplace(env, "HTTPS_PROXY", proxyURL)
    noProxy := envValue(env, "NO_PROXY")
    env = setOrReplace(env, "NO_PROXY", joinNoProxy(noProxy))
    return env
}
```

Default `NO_PROXY`: `localhost,127.0.0.1,::1,*.local`.

## Per-tool integration

### `gitall`

1. In `main()`, when `-proxy` is set, call `proxypass.Start(ctx)` after flag
   parsing and before discovering repos.
2. Print `[proxy] gitall: child proxy on <addr>`.
3. Pass the proxy URL into the `opts` struct.
4. In the `git()` helper, inject `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` into
   `cmd.Env` when `o.proxyURL` is non-empty.
5. Shutdown: the scheduler already waits for all repo goroutines; the proxy
   listener closes when the parent context is cancelled at the end of `main()`.

Because `gitall` discovers repos, fetches, merges, and pushes concurrently, the
proxy may see a high fan-in. The implementation is expected to handle one
connection per concurrent request; no central queue is needed beyond Go’s
network runtime.

### `pulse`

1. In `main()`, when `-proxy` is set, start `proxypass.Start(ctx)` before
   constructing the scheduler.
2. Print `[proxy] pulse: child proxy on <addr>`.
3. Extend `CommandRunner` or pass the proxy URL as a field on the scheduler.
4. In `realCommandRunner.Run`, set `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` in
   `cmd.Env` when the proxy URL is configured.
5. When the scheduler returns (on SIGINT/SIGTERM), the context cancels and the
   proxy closes.

Because each job runs in its own shell, the proxy variables will be inherited
by commands launched from that shell.

## Testing plan

### Unit tests for `internal/proxypass`

1. `TestStart_BindsLoopback`: server binds to `127.0.0.1`, not empty or `0.0.0.0`.
2. `TestPlainHTTP_RoundTrip`: a request through the proxy reaches an upstream
   `httptest.Server` and returns the real response unchanged.
3. `TestConnectHTTPS`: a `CONNECT` to a TLS `httptest.Server` establishes a
   tunnel and TLS handshake succeeds end-to-end.
4. `TestClose_StopsAccepting`: after `Close()`, new connections fail.
5. `TestContextCancel`: cancelling the parent context closes the listener.

### Integration tests

#### `gitall`

- Start a local HTTP upstream in a test repo configured with an `http://...`
  remote, run `gitall -proxy -n push`, and verify the child `git` process saw
  `HTTP_PROXY` pointing at the proxy by inspecting the logged address.
- Full end-to-end test: run `gitall -proxy push` against a local git HTTP
  server and confirm traffic passes through the loopback proxy.

#### `pulse`

- Configure a job that prints its environment to a temp file, run
  `pulse -proxy -once`, and assert `HTTP_PROXY`/`HTTPS_PROXY` are present.
- Configure a job that fetches a local HTTP endpoint; assert the request reached
  the proxy by checking connection counts or a custom request header injected
  only by the test server.

## Security and operational notes

1. **Loopback only.** The proxy must never bind to `0.0.0.0` or any routable
   interface. This keeps the attack surface minimal: only local children
   can reach it, and it disappears when the parent exits.
2. **No TLS termination.** The parent does not possess child certificates and
   does not perform MITM decryption. HTTPS is pure `CONNECT` tunneling.
3. **No caching.** Responses are streamed; no temporary files or memory caches.
4. **No authentication.** Authentication is expected to be handled upstream by
   the firewall or by git credential helpers outside the proxy.
5. **Sensitive env visibility.** Children can read `HTTP_PROXY`; this is
   normal and consistent with any transparent proxy configuration. Secrets
   must not be embedded in the proxy URL.
6. **Connection cleanup.** `IdleTimeout` plus context cancellation prevents
   long-lived abandoned connections from delaying shutdown.

## Rollout steps

1. Create `internal/proxypass` package and implement `Server` with unit tests.
2. Wire `-proxy` flag into `gitall`:
   - parse flag,
   - start proxy,
   - inject env into `git()` helper,
   - add integration test.
3. Wire `-proxy` flag into `pulse`:
   - parse flag,
   - start proxy,
   - inject env into `CommandRunner`,
   - add integration test.
4. Add `docs/gitall.md` and update `docs/pulse.md` with the new flag and behavior.
5. Run `make test` and fix any regressions.
6. (Optional) smoke-test manually with a local `mitmproxy` or `nc` upstream.

## Open questions / deferred decisions

1. Should `NO_PROXY` also include `169.254.x.x` (link-local) and `.internal`
   domains? Decide after testing against the existing firewall.
2. Should the proxy support SOCKS5 as well, or only HTTP CONNECT? Start with
   HTTP CONNECT; most tools understand `HTTP_PROXY`.
3. Should the proxy bind to `127.0.0.1` only, or also `[::1]` on IPv6-only
   systems? Default to IPv4; revisit if `NO_PROXY` becomes a maintenance
   burden.

## Out of scope

- Any new firewall rules, network namespaces, IP tables, or packet filters.
- TLS interception, certificate generation, or MITM.
- Proxy authentication.
- Persistent proxy process or system service.
- Caching, filtering, or access control.