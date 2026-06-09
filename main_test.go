package main

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testLogger returns a logger that discards output, for quiet tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testConfig returns a config suitable for handler tests (no real socket).
func testConfig(token string) config {
	return config{
		Token:         token,
		Socket:        "/tmp/does-not-matter.sock",
		MaxFailures:   3,
		FailureWindow: time.Minute,
	}
}

func TestValidateToken(t *testing.T) {
	const secret = "hunter2-correct-horse"
	cases := []struct {
		name      string
		presented string
		want      bool
	}{
		{"exact match", secret, true},
		{"wrong content same length", "hunter2-correct-horsX", false},
		{"too short", "hunter2", false},
		{"too long", secret + "extra", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validateToken(secret, c.presented); got != c.want {
				t.Fatalf("validateToken(secret, %q) = %v, want %v", c.presented, got, c.want)
			}
		})
	}
}

func TestExtractBearer(t *testing.T) {
	cases := []struct {
		header  string
		wantTok string
		wantOK  bool
	}{
		{"Bearer abc123", "abc123", true},
		{"bearer abc123", "abc123", true}, // scheme is case-insensitive (RFC 6750)
		{"BEARER abc123", "abc123", true},
		{"Basic abc123", "", false},
		{"Bearer", "", false}, // no token
		{"Bearer ", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		tok, ok := extractBearer(c.header)
		if ok != c.wantOK || tok != c.wantTok {
			t.Fatalf("extractBearer(%q) = (%q, %v), want (%q, %v)", c.header, tok, ok, c.wantTok, c.wantOK)
		}
	}
}

func TestDirectorStripsAuthorization(t *testing.T) {
	g := newGateway(testConfig("secret"), testLogger())

	req := httptest.NewRequest(http.MethodGet, "http://example.com/version", nil)
	req.Header.Set("Authorization", "Bearer super-secret")
	req.Header.Set("X-Forwarded-For", "10.0.0.1")

	g.proxy.Director(req)

	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization header not stripped: %q", got)
	}
	if req.Host != upstreamHost {
		t.Fatalf("req.Host = %q, want %q", req.Host, upstreamHost)
	}
	if req.URL.Host != upstreamHost {
		t.Fatalf("req.URL.Host = %q, want %q", req.URL.Host, upstreamHost)
	}
	if req.URL.Scheme != "http" {
		t.Fatalf("req.URL.Scheme = %q, want http", req.URL.Scheme)
	}
}

func TestHealthzBypassesAuth(t *testing.T) {
	g := newGateway(testConfig("secret"), testLogger())

	rr := httptest.NewRecorder()
	g.handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ok") {
		t.Fatalf("/healthz body = %q", rr.Body.String())
	}
}

func TestUnauthorizedRequestsRejected(t *testing.T) {
	g := newGateway(testConfig("secret"), testLogger())
	handler := g.handler()

	cases := []struct {
		name string
		auth string
	}{
		{"missing header", ""},
		{"basic scheme", "Basic c2VjcmV0"},
		{"wrong token", "Bearer not-the-secret"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/version", nil)
			req.RemoteAddr = "203.0.113." + c.name[:1] + ":12345" // distinct IP per case
			if c.auth != "" {
				req.Header.Set("Authorization", c.auth)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rr.Code)
			}
			if rr.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("missing WWW-Authenticate challenge header")
			}
		})
	}
}

func TestThrottleBlocksAfterMaxFailures(t *testing.T) {
	g := newGateway(testConfig("secret"), testLogger())
	handler := g.handler()

	const ip = "198.51.100.7:5555"
	bad := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/version", nil)
		req.RemoteAddr = ip
		req.Header.Set("Authorization", "Bearer wrong")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	// MaxFailures (3) attempts return 401.
	for i := 0; i < 3; i++ {
		if rr := bad(); rr.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, rr.Code)
		}
	}
	// The next attempt is throttled.
	rr := bad()
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("throttled attempt: status = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header on 429")
	}
}

func TestThrottleResetsOnSuccess(t *testing.T) {
	g := newGateway(testConfig("secret"), testLogger())
	g.throttle.fail("1.2.3.4")
	g.throttle.fail("1.2.3.4")
	g.throttle.success("1.2.3.4")
	if ok, _ := g.throttle.allow("1.2.3.4"); !ok {
		t.Fatal("expected client to be allowed after a successful auth reset")
	}
}

// TestResponseRecorderImplementsStreamingInterfaces guards the streaming path:
// the access-log wrapper must remain a Flusher and Hijacker so /events,
// `docker logs -f`, and exec/attach keep working through the proxy.
func TestResponseRecorderImplementsStreamingInterfaces(t *testing.T) {
	var rw http.ResponseWriter = &responseRecorder{ResponseWriter: httptest.NewRecorder()}
	if _, ok := rw.(http.Flusher); !ok {
		t.Fatal("responseRecorder does not implement http.Flusher")
	}
	if _, ok := rw.(http.Hijacker); !ok {
		t.Fatal("responseRecorder does not implement http.Hijacker")
	}
}

func TestResolveTokenFromFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	if err := os.WriteFile(file, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GATEWAY_AUTH_TOKEN", "")
	t.Setenv("GATEWAY_AUTH_TOKEN_FILE", file)
	tok, err := resolveToken()
	if err != nil {
		t.Fatalf("resolveToken: %v", err)
	}
	if tok != "file-secret" {
		t.Fatalf("token = %q, want %q (trailing newline trimmed)", tok, "file-secret")
	}

	// Both set is an error.
	t.Setenv("GATEWAY_AUTH_TOKEN", "inline")
	if _, err := resolveToken(); err == nil {
		t.Fatal("expected error when both token sources are set")
	}

	// Neither set is an error.
	t.Setenv("GATEWAY_AUTH_TOKEN", "")
	t.Setenv("GATEWAY_AUTH_TOKEN_FILE", "")
	if _, err := resolveToken(); err == nil {
		t.Fatal("expected error when no token source is set")
	}
}

// TestProxyEndToEnd proves the full path: the custom Unix-socket DialContext
// reaches an upstream listening on a temp socket, an authorized request is
// forwarded, and the operator's bearer token is stripped before the upstream
// (standing in for dockerd) ever sees it.
func TestProxyEndToEnd(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "docker.sock")

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	var mu sync.Mutex
	var gotAuth, gotPath string

	upstream := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			gotAuth = r.Header.Get("Authorization")
			gotPath = r.URL.Path
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"Version":"99.9"}`)
		}),
	}
	go func() { _ = upstream.Serve(ln) }()
	defer upstream.Close()

	cfg := testConfig("s3cr3t")
	cfg.Socket = socketPath
	g := newGateway(cfg, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	req.Header.Set("Authorization", "Bearer s3cr3t")
	rr := httptest.NewRecorder()
	g.handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "99.9") {
		t.Fatalf("unexpected upstream body: %s", rr.Body.String())
	}
	if rr.Header().Get("X-Request-Id") == "" {
		t.Fatal("missing X-Request-Id response header")
	}

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "" {
		t.Fatalf("Authorization token leaked to upstream: %q", gotAuth)
	}
	if gotPath != "/version" {
		t.Fatalf("upstream path = %q, want /version", gotPath)
	}
}
