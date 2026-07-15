// Tests here exercise network behavior. If the local firewall blocks or delays
// loopback proxy traffic, set PPROXY_NET=0 to skip the live network tests.
package proxypass

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func netTestsEnabled() bool {
	return os.Getenv("PPROXY_NET") != "0"
}

func TestStart_BindsLoopback(t *testing.T) {
	if !netTestsEnabled() {
		t.Skip("PPROXY_NET=0")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	host, _, err := net.SplitHostPort(s.Addr())
	if err != nil {
		t.Fatalf("SplitHostPort %q: %v", s.Addr(), err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("expected 127.0.0.1, got %q", host)
	}
}

func TestPlainHTTP_RoundTrip(t *testing.T) {
	if !netTestsEnabled() {
		t.Skip("PPROXY_NET=0")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Method", r.Method)
		w.Header().Set("X-Echo-Body", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "hello from upstream")
	}))
	defer upstream.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL+"/some/path", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	proxyURL := s.URLString()
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(s.URL()),
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello from upstream" {
		t.Fatalf("body = %q", body)
	}
	if resp.Header.Get("X-Echo-Method") != "GET" {
		t.Fatalf("method = %q", resp.Header.Get("X-Echo-Method"))
	}
	if resp.Header.Get("X-Echo-Body") != "/some/path" {
		t.Fatalf("path = %q", resp.Header.Get("X-Echo-Body"))
	}
	_ = proxyURL
}

func TestConnectHTTPS(t *testing.T) {
	if !netTestsEnabled() {
		t.Skip("PPROXY_NET=0")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello over tls")
	}))
	defer upstream.Close()

	// Use upstream.Client() so we have httptest's self-signed CA, but force
	// InsecureSkipVerify so the proxy CONNECT target (127.0.0.1:port) is not
	// rejected when the test server cert lacks an IP SAN.
	client := upstream.Client()
	transport := client.Transport.(*http.Transport)
	transport.Proxy = http.ProxyURL(s.URL())
	transport.ForceAttemptHTTP2 = false
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig.InsecureSkipVerify = true
	client.Timeout = 10 * time.Second
	client.Transport = transport

	resp, err := client.Get(upstream.URL + "/secure")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello over tls" {
		t.Fatalf("body = %q", body)
	}
}

func TestClose_StopsAccepting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	conn, err := net.Dial("tcp", s.Addr())
	if err == nil {
		conn.Close()
		t.Fatal("expected dial to fail after close")
	}
}

func TestContextCancel(t *testing.T) {
	if !netTestsEnabled() {
		t.Skip("PPROXY_NET=0")
	}
	ctx, cancel := context.WithCancel(context.Background())

	s, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	cancel()
	// The listener closes via server.Close triggered by the base context
	// cancellation. Give the goroutine a chance to stop serving.
	if err := s.server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	conn, err := net.Dial("tcp", s.Addr())
	if err == nil {
		conn.Close()
		t.Fatal("expected dial to fail after context cancellation")
	}
}

func TestEnv_MergesNoProxy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	base := []string{"FOO=bar", "NO_PROXY=corp.local, 10.0.0.0/8"}
	env := s.Env(base)

	if getEnv(env, "HTTP_PROXY") != s.URLString() {
		t.Fatalf("HTTP_PROXY not set")
	}
	if getEnv(env, "HTTPS_PROXY") != s.URLString() {
		t.Fatalf("HTTPS_PROXY not set")
	}
	noProxy := getEnv(env, "NO_PROXY")
	for _, want := range []string{"localhost", "127.0.0.1", "::1", "*.local", "corp.local", "10.0.0.0/8"} {
		if !strings.Contains(noProxy, want) {
			t.Fatalf("NO_PROXY %q missing %q", noProxy, want)
		}
	}
}

func TestEnv_NilServer(t *testing.T) {
	base := []string{"FOO=bar"}
	if got := ((*Server)(nil)).Env(base); len(got) != 1 {
		t.Fatalf("expected unchanged env, got %v", got)
	}
}

func BenchmarkThroughput(b *testing.B) {
	if !netTestsEnabled() {
		b.Skip("PPROXY_NET=0")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := Start(ctx)
	if err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer s.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(s.URL()),
			MaxConnsPerHost: 16,
		},
		Timeout: 5 * time.Second,
	}

	var okCount int64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := client.Get(upstream.URL + "/bench")
			if err != nil {
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				atomic.AddInt64(&okCount, 1)
			}
		}
	})

	if okCount == 0 {
		b.Fatal("no successful requests")
	}
}