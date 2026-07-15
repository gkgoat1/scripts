// Package proxypass provides a temporary, loopback-only HTTP(S) passthrough
// proxy intended for use by parent tools that want child traffic forwarded
// through themselves so an existing firewall can intercept and tag child
// connections separately from parent connections.
package proxypass

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	// DefaultDialTimeout is the initial TCP dial timeout. It is deliberately
	// short so firewall prompts can be approved or denied per connection
	// rather than causing an unbounded backlog.
	DefaultDialTimeout = 15 * time.Second
)

// Server is a loopback passthrough proxy. It binds to 127.0.0.1 on a random
// port and forwards HTTP/HTTPS traffic without inspecting or modifying it.
type Server struct {
	url      *url.URL
	listener net.Listener
	server   *http.Server
	wg       sync.WaitGroup
}

// Start begins listening on a loopback address and serving proxy requests.
// The returned server is bound to the parent context; cancelling ctx stops the
// listener and begins graceful shutdown of active connections.
func Start(ctx context.Context) (*Server, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind loopback listener: %w", err)
	}

	addr := listener.Addr().(*net.TCPAddr)
	u := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", addr.Port),
	}

	s := &Server{
		url:      u,
		listener: listener,
	}

	baseCtx := func(net.Listener) context.Context { return ctx }
	s.server = &http.Server{
		Handler:           s,
		IdleTimeout:       5 * time.Minute,
		ReadHeaderTimeout: 30 * time.Second,
		BaseContext:       baseCtx,
		ErrorLog:          log.New(io.Discard, "", 0),
	}

	go s.serve()
	return s, nil
}

// URL returns the http://127.0.0.1:port address clients should use as
// HTTP_PROXY/HTTPS_PROXY.
func (s *Server) URL() *url.URL {
	return s.url
}

// URLString returns the proxy URL as a string.
func (s *Server) URLString() string {
	return s.url.String()
}

// Addr returns the bound listen address for logging.
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Close stops accepting new connections and waits briefly for active ones.
func (s *Server) Close() error {
	err := s.server.Close()
	s.wg.Wait()
	return err
}

// Shutdown gracefully closes the listener and waits for active connections.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.server.Shutdown(ctx)
	s.wg.Wait()
	return err
}

func (s *Server) serve() {
	_ = s.server.Serve(s.listener)
}

// ServeHTTP routes requests to the correct passthrough handler. We use a
// single handler rather than http.ServeMux because Go 1.22+'s ServeMux does
// not route authority-form CONNECT targets (host:port) to the wildcard "/"
// pattern, returning 404 instead.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	s.handleHTTP(w, r)
}

// handleHTTP forwards plain http:// requests to the origin server unchanged.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Host == "" {
		http.Error(w, "non-proxy request", http.StatusBadRequest)
		return
	}

	destConn, err := dialTCP(r.Context(), r.URL.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer destConn.Close()

	// Build an outbound request we can canonicalize. We don't actually use
	// Go's HTTP client here because we want to keep raw byte streaming and
	// avoid any internal timeout or keep-alive behavior that would defeat the
	// backoff dialer. But we still need to preserve the original Host header.
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if r.Host != "" {
		outReq.Host = r.Host
	}
	hopByHop := map[string]bool{
		"Connection":          true,
		"Proxy-Connection":    true,
		"Keep-Alive":          true,
		"Proxy-Authenticate":  true,
		"Proxy-Authorization": true,
		"Te":                  true,
		"Trailer":             true,
		"Transfer-Encoding":   true,
		"Upgrade":             true,
	}
	for key, values := range r.Header {
		if hopByHop[key] {
			continue
		}
		for _, v := range values {
			outReq.Header.Add(key, v)
		}
	}

	// Apply connection hop-by-hop handling: close after this single transaction
	// so the simple ReadResponse path stays in sync. Long-lived proxy
	// connections use CONNECT tunneling anyway.
	outReq.Header.Set("Connection", "close")

	if err := outReq.Write(destConn); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	br := bufio.NewReader(destConn)
	resp, err := http.ReadResponse(br, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// handleConnect handles HTTPS proxy CONNECT tunneling.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	destConn, err := dialTCP(r.Context(), r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.WriteHeader(http.StatusOK)

	clientConn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		destConn.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.wg.Add(2)
	doneA := make(chan struct{})
	go func() {
		defer s.wg.Done()
		defer close(doneA)
		io.Copy(destConn, clientConn)
		destConn.SetReadDeadline(time.Now())
	}()
	go func() {
		defer s.wg.Done()
		io.Copy(clientConn, destConn)
		clientConn.Close()
		clientConn.SetReadDeadline(time.Now())
		<-doneA
	}()
}

// dialTCP attempts to establish a TCP connection with a short, bounded timeout
// and exponential backoff. This gives interactive firewalls time to approve or
// deny each outbound connection individually instead of queueing an unbounded
// backlog of pending requests.
func dialTCP(ctx context.Context, host string) (net.Conn, error) {
	maxAttempts := 6
	baseDelay := 100 * time.Millisecond
	for attempt := 0; attempt < maxAttempts; attempt++ {
		conn, err := (&net.Dialer{Timeout: DefaultDialTimeout}).DialContext(ctx, "tcp", host)
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if nerr, ok := err.(net.Error); ok && !nerr.Timeout() {
			// Refusal, DNS failure, and other hard errors should surface
			// immediately so the caller doesn't retry indefinitely.
			return nil, err
		}
		delay := baseDelay * (1 << attempt)
		if delay > 5*time.Second {
			delay = 5 * time.Second
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, fmt.Errorf("dial %s: exceeded %d attempts", host, maxAttempts)
}

// Env returns a slice of environment variables that configure a child process
// to use the proxy, merged with the supplied base environment. The proxy is
// injected into HTTP_PROXY and HTTPS_PROXY; NO_PROXY is appended to any
// existing value.
func (s *Server) Env(base []string) []string {
	if s == nil {
		return base
	}
	proxyURL := s.URLString()
	out := append([]string(nil), base...)
	out = setEnv(out, "HTTP_PROXY", proxyURL)
	out = setEnv(out, "HTTPS_PROXY", proxyURL)
	existing := getEnv(out, "NO_PROXY")
	noProxy := mergeNoProxy(existing)
	out = setEnv(out, "NO_PROXY", noProxy)
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

func getEnv(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			return e[len(prefix):]
		}
	}
	return ""
}

func mergeNoProxy(existing string) string {
	defaults := []string{"localhost", "127.0.0.1", "::1", "*.local"}
	seen := make(map[string]bool)
	for _, h := range defaults {
		seen[h] = true
	}
	var out []string
	if existing != "" {
		for _, h := range splitNoProxy(existing) {
			if !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	out = append(defaults, out...)
	return joinNoProxy(out)
}

func splitNoProxy(s string) []string {
	parts := []string{}
	for _, p := range splitString(s, ",") {
		p = stringTrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func joinNoProxy(parts []string) string {
	var b []byte
	for i, p := range parts {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, p...)
	}
	return string(b)
}

func stringTrimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func splitString(s, sep string) []string {
	var parts []string
	sepLen := len(sep)
	if sepLen == 0 {
		parts = append(parts, s)
		return parts
	}
	start := 0
	for i := 0; i <= len(s)-sepLen; {
		if s[i:i+sepLen] == sep {
			parts = append(parts, s[start:i])
			start = i + sepLen
			i = start
			continue
		}
		i++
	}
	parts = append(parts, s[start:])
	return parts
}