// Command docker-engine-gateway-api is a minimal, dependency-free reverse proxy
// that places a Bearer-token authentication layer in front of the local Docker
// Engine API exposed on a Unix domain socket.
//
// The Docker socket is root-equivalent: anyone able to talk to it can take over
// the host. This gateway lets an operator expose the Docker API over the network
// behind a shared secret, using only the Go standard library.
//
// Copyright 2026 James Roi Dela Cruz. Licensed under the Apache License,
// Version 2.0 (the "License"); you may not use this file except in compliance
// with the License. You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Build metadata, overridden at link time via -ldflags "-X main.version=… …".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const (
	defaultPort   = "8080"
	defaultSocket = "/var/run/docker.sock"

	// upstreamHost is a sentinel hostname used to populate the proxied request's
	// URL and Host header. The custom transport dials the Unix socket directly and
	// ignores this value, but the reverse proxy still needs a syntactically valid
	// upstream URL.
	upstreamHost = "docker.local"
)

// config holds the runtime configuration, sourced entirely from environment
// variables so the binary stays 12-factor friendly and container-native.
type config struct {
	Port   string // GATEWAY_PORT
	Socket string // DOCKER_SOCKET
	Token  string // GATEWAY_AUTH_TOKEN / GATEWAY_AUTH_TOKEN_FILE

	MaxFailures   int           // GATEWAY_AUTH_MAX_FAILURES
	FailureWindow time.Duration // GATEWAY_AUTH_FAILURE_WINDOW
	TrustProxy    bool          // GATEWAY_TRUST_PROXY
	LogLevel      slog.Level    // GATEWAY_LOG_LEVEL
	LogHealthz    bool          // GATEWAY_LOG_HEALTHZ
}

// loadConfig reads configuration from the environment, applies defaults, and
// validates required values. It refuses to start without an auth token so the
// gateway can never accidentally run as an open proxy onto docker.sock.
func loadConfig() (config, error) {
	token, err := resolveToken()
	if err != nil {
		return config{}, err
	}
	return config{
		Port:          getenv("GATEWAY_PORT", defaultPort),
		Socket:        getenv("DOCKER_SOCKET", defaultSocket),
		Token:         token,
		MaxFailures:   getenvInt("GATEWAY_AUTH_MAX_FAILURES", 10),
		FailureWindow: getenvDuration("GATEWAY_AUTH_FAILURE_WINDOW", time.Minute),
		TrustProxy:    getenvBool("GATEWAY_TRUST_PROXY", false),
		LogLevel:      parseLevel(getenv("GATEWAY_LOG_LEVEL", "info")),
		LogHealthz:    getenvBool("GATEWAY_LOG_HEALTHZ", false),
	}, nil
}

// resolveToken loads the shared secret from GATEWAY_AUTH_TOKEN_FILE (preferred,
// avoids leaking the secret through the process environment) or GATEWAY_AUTH_TOKEN.
// Exactly one must yield a non-empty value.
func resolveToken() (string, error) {
	inline := os.Getenv("GATEWAY_AUTH_TOKEN")
	file := os.Getenv("GATEWAY_AUTH_TOKEN_FILE")
	switch {
	case inline != "" && file != "":
		return "", errors.New("set only one of GATEWAY_AUTH_TOKEN or GATEWAY_AUTH_TOKEN_FILE, not both")
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("reading GATEWAY_AUTH_TOKEN_FILE %q: %w", file, err)
		}
		tok := strings.TrimSpace(string(b))
		if tok == "" {
			return "", fmt.Errorf("GATEWAY_AUTH_TOKEN_FILE %q is empty", file)
		}
		return tok, nil
	case strings.TrimSpace(inline) != "":
		return inline, nil
	default:
		return "", errors.New("GATEWAY_AUTH_TOKEN or GATEWAY_AUTH_TOKEN_FILE is required; refusing to start an unauthenticated proxy onto docker.sock")
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// newLogger returns a structured JSON logger writing to stderr.
func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// gateway bundles the runtime components: configuration, logger, the auth
// throttle, and the Docker reverse proxy.
type gateway struct {
	cfg      config
	log      *slog.Logger
	throttle *throttle
	proxy    *httputil.ReverseProxy
}

func newGateway(cfg config, logger *slog.Logger) *gateway {
	g := &gateway{
		cfg: cfg,
		log: logger,
		throttle: &throttle{
			maxFailures: cfg.MaxFailures,
			window:      cfg.FailureWindow,
			clients:     make(map[string]*clientState),
		},
	}
	g.proxy = g.newReverseProxy()
	return g
}

// newDockerTransport builds an http.Transport that funnels every outbound
// connection to the Docker daemon's Unix domain socket, regardless of the
// request's host or port. Explicit timeouts bound idle and slow connections
// without breaking long-lived Docker streaming endpoints.
func newDockerTransport(socketPath string) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return &http.Transport{
		// DialContext ignores the dialed network/address and always connects to
		// the configured Unix socket.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:          64,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// The Docker daemon socket speaks HTTP/1.1; do not negotiate HTTP/2.
		ForceAttemptHTTP2: false,
	}
}

// newReverseProxy wires up the standard-library reverse proxy that forwards
// authenticated requests to the Docker daemon over the Unix socket transport.
func (g *gateway) newReverseProxy() *httputil.ReverseProxy {
	target := &url.URL{Scheme: "http", Host: upstreamHost}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = newDockerTransport(g.cfg.Socket)

	// Flush every write straight to the client instead of buffering. The proxy
	// otherwise only streams responses it recognizes as event-streams or with an
	// unknown Content-Length, so Docker's incremental output (/build progress,
	// image pull layers, logs) can arrive all at once at the end. A negative
	// interval forces an immediate flush after each write, regardless of the
	// upstream Content-Length. Hijacked connections (exec/attach) are unaffected.
	proxy.FlushInterval = -1

	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)

		// Pin the upstream identity for the Unix transport.
		req.URL.Scheme = "http"
		req.URL.Host = upstreamHost
		req.Host = upstreamHost

		// CRITICAL: strip the operator credential so the shared secret is never
		// forwarded to (or logged by) the Docker daemon.
		req.Header.Del("Authorization")

		// Do not advertise proxy/forwarding metadata to the daemon. Setting the
		// header to nil tells ReverseProxy.ServeHTTP not to populate it.
		req.Header["X-Forwarded-For"] = nil
		req.Header.Del("X-Forwarded-Host")
		req.Header.Del("X-Forwarded-Proto")
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		g.log.Error("proxy error", slog.String("error", err.Error()))
		writeJSONError(w, http.StatusBadGateway, "upstream Docker daemon unavailable")
	}
	return proxy
}

// validateToken compares the presented token against the expected token in
// constant time to avoid leaking information through timing side channels.
//
// crypto/subtle.ConstantTimeCompare already returns 0 for unequal lengths, but
// we additionally fold an explicit constant-time length check and combine the
// results without short-circuiting, so the work done never branches on where a
// mismatch occurs.
func validateToken(expected, presented string) bool {
	exp := []byte(expected)
	pres := []byte(presented)

	lengthsMatch := subtle.ConstantTimeEq(int32(len(pres)), int32(len(exp)))
	contentMatch := subtle.ConstantTimeCompare(pres, exp)

	return lengthsMatch == 1 && contentMatch == 1
}

// extractBearer parses an "Authorization: Bearer <token>" header value and
// returns the token. The scheme match is case-insensitive per RFC 6750.
func extractBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	return header[len(prefix):], true
}

// authThrottle enforces a valid Bearer token before passing the request to the
// wrapped handler, and rate-limits repeated authentication failures per client.
// It never logs the presented or expected token.
func (g *gateway) authThrottle(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := g.clientKey(r)

		if ok, retryAfter := g.throttle.allow(key); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
			g.log.Warn("auth throttled", slog.String("remote_addr", key))
			writeJSONError(w, http.StatusTooManyRequests, "too many failed authentication attempts; retry later")
			return
		}

		presented, ok := extractBearer(r.Header.Get("Authorization"))
		if !ok || !validateToken(g.cfg.Token, presented) {
			g.throttle.fail(key)
			w.Header().Set("WWW-Authenticate", `Bearer realm="docker-engine-gateway"`)
			writeJSONError(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}

		g.throttle.success(key)
		next.ServeHTTP(w, r)
	})
}

// clientKey identifies the client for throttling. By default it is the remote
// IP; when GATEWAY_TRUST_PROXY is set it uses the first X-Forwarded-For hop,
// which is correct only behind a trusted reverse proxy / TLS terminator.
func (g *gateway) clientKey(r *http.Request) string {
	if g.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i >= 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// handler builds the gateway's HTTP handler: an unauthenticated /healthz
// endpoint for orchestrator liveness probes, everything else routed through the
// throttling auth middleware into the Docker reverse proxy, all wrapped in
// structured access logging.
func (g *gateway) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", g.healthz)
	mux.Handle("/", g.authThrottle(g.proxy))
	return g.accessLog(mux)
}

func (g *gateway) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"status":"ok","version":%s}`, strconv.Quote(version))
}

// accessLog emits one structured JSON log line per request. It records status,
// size, and latency but never the Authorization header or token value.
func (g *gateway) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := newRequestID()
		w.Header().Set("X-Request-Id", rid)

		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		level := slog.LevelInfo
		if r.URL.Path == "/healthz" {
			if !g.cfg.LogHealthz {
				return
			}
			level = slog.LevelDebug
		}

		g.log.LogAttrs(r.Context(), level, "request",
			slog.String("request_id", rid),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int64("bytes", rec.bytes),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("remote_addr", g.clientKey(r)),
			slog.String("user_agent", r.UserAgent()),
		)
	})
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// responseRecorder wraps an http.ResponseWriter to capture the status code and
// number of bytes written. It deliberately also exposes Flush and Hijack so
// that Docker's streaming (/events, logs -f) and connection-upgrade endpoints
// (exec/attach) continue to work through the proxy.
type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("underlying ResponseWriter does not support hijacking")
}

// Unwrap lets net/http's ResponseController reach the underlying writer.
func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// throttle is an in-memory, per-client failed-authentication limiter.
type throttle struct {
	mu          sync.Mutex
	maxFailures int
	window      time.Duration
	clients     map[string]*clientState
}

type clientState struct {
	failures  int
	windowEnd time.Time
}

// allow reports whether the client may attempt authentication, and if not, how
// long until the window resets.
func (t *throttle) allow(key string) (bool, time.Duration) {
	if t.maxFailures <= 0 {
		return true, 0
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.clients[key]
	if st == nil {
		return true, 0
	}
	if now.After(st.windowEnd) {
		delete(t.clients, key)
		return true, 0
	}
	if st.failures >= t.maxFailures {
		return false, time.Until(st.windowEnd)
	}
	return true, 0
}

func (t *throttle) fail(key string) {
	if t.maxFailures <= 0 {
		return
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.clients[key]
	if st == nil || now.After(st.windowEnd) {
		st = &clientState{windowEnd: now.Add(t.window)}
		t.clients[key] = st
	}
	st.failures++
}

func (t *throttle) success(key string) {
	t.mu.Lock()
	delete(t.clients, key)
	t.mu.Unlock()
}

func (t *throttle) sweep() {
	now := time.Now()
	t.mu.Lock()
	for k, st := range t.clients {
		if now.After(st.windowEnd) {
			delete(t.clients, k)
		}
	}
	t.mu.Unlock()
}

// run periodically evicts expired entries so the map cannot grow unbounded.
func (t *throttle) run(stop <-chan struct{}) {
	interval := t.window
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.sweep()
		case <-stop:
			return
		}
	}
}

// writeJSONError writes a minimal JSON error body without leaking internal
// detail. msg is always a controlled, internally-defined string.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":` + strconv.Quote(msg) + `}`))
}

// checkSocket logs a warning if the Docker socket is missing or is not a socket.
// It does not abort: in some orchestrators the socket may be mounted slightly
// after the process starts, and per-request dials will surface any real failure.
func (g *gateway) checkSocket() {
	info, err := os.Stat(g.cfg.Socket)
	if err != nil {
		g.log.Warn("docker socket not accessible at startup",
			slog.String("socket", g.cfg.Socket), slog.String("error", err.Error()))
		return
	}
	if info.Mode()&os.ModeSocket == 0 {
		g.log.Warn("path exists but is not a unix domain socket", slog.String("socket", g.cfg.Socket))
	}
}

// healthcheck performs a local probe of /healthz and returns a process exit
// code. It powers the container HEALTHCHECK on the shell-less distroless image.
func healthcheck() int {
	port := getenv("GATEWAY_PORT", defaultPort)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck unhealthy: status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

func main() {
	var showVersion, runHealthcheck bool
	flag.BoolVar(&showVersion, "version", false, "print version information and exit")
	flag.BoolVar(&runHealthcheck, "healthcheck", false, "probe the local /healthz endpoint and exit (0=healthy)")
	flag.Parse()

	if showVersion {
		fmt.Printf("docker-engine-gateway-api %s (commit %s, built %s)\n", version, commit, date)
		return
	}
	if runHealthcheck {
		os.Exit(healthcheck())
	}

	cfg, err := loadConfig()
	if err != nil {
		newLogger(slog.LevelInfo).Error("configuration error", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	g := newGateway(cfg, logger)
	g.checkSocket()

	stopSweeper := make(chan struct{})
	go g.throttle.run(stopSweeper)
	defer close(stopSweeper)

	srv := &http.Server{
		Addr:    net.JoinHostPort("", cfg.Port),
		Handler: g.handler(),

		// ReadHeaderTimeout bounds the time to read request headers, defending
		// against slowloris-style attacks.
		ReadHeaderTimeout: 10 * time.Second,
		// ReadTimeout and WriteTimeout are intentionally left unbounded (0).
		// Docker streaming endpoints are long-lived in both directions:
		//   - uploads: `docker build`, `docker load`, image push bodies
		//   - downloads/streams: `/events`, `docker logs -f`, attach/exec hijack
		// A hard read/write deadline would sever these legitimate connections.
		// IdleTimeout below still reclaims idle keep-alive connections.
		ReadTimeout:  0,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,

		MaxHeaderBytes: 1 << 20, // 1 MiB
	}

	logger.Info("starting Docker Engine API gateway",
		slog.String("version", version),
		slog.String("addr", srv.Addr),
		slog.String("socket", cfg.Socket),
		slog.Bool("trust_proxy", cfg.TrustProxy),
	)

	shutdownDone := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		logger.Info("shutting down gracefully", slog.String("signal", sig.String()))

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown error", slog.String("error", err.Error()))
		}
		close(shutdownDone)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	<-shutdownDone
	logger.Info("shutdown complete")
}
