// Tests here exercise network behavior. If the local firewall blocks or delays
// loopback proxy traffic, set PPROXY_NET=0 to skip the live network tests.
package proxypass

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testTimeout = 60 * time.Second

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
		Timeout: testTimeout,
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
	client.Timeout = testTimeout
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

// startTestProxy is a minimal raw-TCP upstream proxy used only by tests. It
// accepts HTTP proxy requests and CONNECT tunnels. Unlike httptest.Server, it
// does not need to hijack an http.ResponseWriter, which makes CONNECT handling
// more predictable.
func startTestProxy(t *testing.T, target *url.URL) (net.Listener, *[]string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test proxy: %v", err)
	}
	var mu sync.Mutex
	var requests []string
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				br := bufio.NewReader(conn)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				if req.Method == http.MethodConnect {
					mu.Lock()
					requests = append(requests, "CONNECT "+req.Host)
					mu.Unlock()
					destConn, err := net.Dial("tcp", req.Host)
					if err != nil {
						fmt.Fprintf(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
						return
					}
					defer destConn.Close()
					fmt.Fprintf(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
					if br.Buffered() > 0 {
						io.CopyN(destConn, br, int64(br.Buffered()))
					}
					var wg sync.WaitGroup
					wg.Add(2)
					go func() { defer wg.Done(); io.Copy(destConn, conn); destConn.Close() }()
					go func() { defer wg.Done(); io.Copy(conn, destConn); conn.Close() }()
					wg.Wait()
					return
				}
				mu.Lock()
				requests = append(requests, req.Method+" "+req.URL.String())
				if auth := req.Header.Get("Proxy-Authorization"); auth != "" {
					requests = append(requests, "AUTH "+auth)
				}
				mu.Unlock()
				// Body may already have bytes buffered in br; present a reader
				// that includes them so ReverseProxy can read the full body.
				req.Body = io.NopCloser(io.MultiReader(br, req.Body))
				w := &rawResponseWriter{conn: conn, header: make(http.Header)}
				rp := httputil.NewSingleHostReverseProxy(target)
				rp.ServeHTTP(w, req)
			}(conn)
		}
	}()
	t.Cleanup(func() { l.Close() })
	return l, &requests
}

type rawResponseWriter struct {
	conn        net.Conn
	header      http.Header
	code        int
	wroteHeader bool
}

func (w *rawResponseWriter) Header() http.Header { return w.header }

func (w *rawResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.code = code
	fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n", code, http.StatusText(code))
	w.header.Write(w.conn)
	fmt.Fprint(w.conn, "\r\n")
	w.wroteHeader = true
}

func (w *rawResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.conn.Write(p)
}

func TestUpstreamHTTP_ProxyUsed(t *testing.T) {
	if !netTestsEnabled() {
		t.Skip("PPROXY_NET=0")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Path", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "hello via upstream")
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)

	proxy, requests := startTestProxy(t, upstreamURL)

	s, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	t.Setenv("HTTP_PROXY", "http://"+proxy.Addr().String())

	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(s.URL())},
		Timeout:   testTimeout,
	}
	resp, err := client.Get(upstream.URL + "/foo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello via upstream" {
		t.Fatalf("body = %q", body)
	}
	if len(*requests) < 1 || !strings.Contains((*requests)[0], upstreamURL.Host) {
		t.Fatalf("expected proxy request for upstream host, got %v", *requests)
	}
}

func TestUpstreamCONNECT_RawTunnel(t *testing.T) {
	if !netTestsEnabled() {
		t.Skip("PPROXY_NET=0")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo: %v", err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				line, err := br.ReadString('\n')
				if err != nil {
					return
				}
				fmt.Fprint(c, line)
			}(conn)
		}
	}()

	// The helper needs a target URL for non-CONNECT requests; it is unused here.
	dummyTarget := &url.URL{Scheme: "http", Host: "127.0.0.1:1"}
	proxy, requests := startTestProxy(t, dummyTarget)

	s, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	t.Setenv("HTTPS_PROXY", "http://"+proxy.Addr().String())

	conn, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatalf("dial local proxy: %v", err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", echo.Addr().String(), echo.Addr().String())
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read connect response: %v", err)
	}
	if !strings.HasPrefix(line, "HTTP/1.1 200") && !strings.HasPrefix(line, "HTTP/1.0 200") {
		t.Fatalf("unexpected connect response: %q", line)
	}
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read headers: %v", err)
		}
		if l == "\r\n" || l == "\n" {
			break
		}
	}

	fmt.Fprint(conn, "ping\n")
	resp, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if resp != "ping\n" {
		t.Fatalf("echo = %q", resp)
	}

	want := "CONNECT " + echo.Addr().String()
	var saw bool
	for _, req := range *requests {
		if req == want {
			saw = true
			break
		}
	}
	if !saw {
		t.Fatalf("expected %s through upstream proxy, got %v", want, *requests)
	}
}

func TestNoProxy_Bypass(t *testing.T) {
	if !netTestsEnabled() {
		t.Skip("PPROXY_NET=0")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "direct")
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)

	proxy, requests := startTestProxy(t, upstreamURL)

	s, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	t.Setenv("HTTP_PROXY", "http://"+proxy.Addr().String())
	t.Setenv("NO_PROXY", upstreamURL.Hostname())

	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(s.URL())},
		Timeout:   testTimeout,
	}
	resp, err := client.Get(upstream.URL + "/skip")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "direct" {
		t.Fatalf("body = %q", body)
	}
	if len(*requests) != 0 {
		t.Fatalf("expected upstream proxy not to be used, got %d requests", len(*requests))
	}
}

func TestProxyAuth_Credentials(t *testing.T) {
	if !netTestsEnabled() {
		t.Skip("PPROXY_NET=0")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "auth-ok")
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)

	proxy, requests := startTestProxy(t, upstreamURL)

	s, err := Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Close()

	t.Setenv("HTTP_PROXY", "http://testuser:testpass@"+proxy.Addr().String())

	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(s.URL())},
		Timeout:   testTimeout,
	}
	resp, err := client.Get(upstream.URL + "/auth")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "auth-ok" {
		t.Fatalf("body = %q", body)
	}
	want := "AUTH " + proxyAuthHeader(url.UserPassword("testuser", "testpass"))
	var got string
	for _, req := range *requests {
		if strings.HasPrefix(req, "AUTH ") {
			got = req
			break
		}
	}
	if got != want {
		t.Fatalf("Proxy-Authorization = %q, want %q", got, want)
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
		Timeout: testTimeout,
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