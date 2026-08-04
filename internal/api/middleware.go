package api

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxJSONBody bounds a JSON request body.
//
// The router applies one MaxBytesHandler (the media upload limit, 100MB by
// default) to every authenticated route, so a 100MB body posted to
// /messages/send was accepted and buffered before a single field was parsed.
// A JSON command is kilobytes; 1 MiB is generous and still two orders of
// magnitude below the upload limit.
const maxJSONBody = 1 << 20

// readJSON decodes a size-limited JSON request body, writing the error
// response itself and returning false when it could not.
//
// The limit is applied here, at the decode site, rather than as a
// route-scoped middleware, because the route table lives in server.go. Every
// JSON handler must go through this rather than json.NewDecoder(r.Body)
// directly, or it inherits the 100MB upload limit.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, codeBodyTooLarge,
				fmt.Sprintf("JSON request body exceeds %d bytes", maxJSONBody))
			return false
		}
		// Decoder errors describe the caller's own bytes ("invalid character
		// 'x' after top-level value") and leak nothing about this server.
		writeError(w, http.StatusBadRequest, codeInvalidBody, err.Error())
		return false
	}
	return true
}

// Rate limiting defaults. Deliberately loose enough that an interactive
// operator or a normal integration never notices, and tight enough that a
// runaway loop or a credential-stuffing script is throttled.
const (
	defaultRateLimitRPS   = 20
	defaultRateLimitBurst = 40

	// rateLimitIdleTTL evicts a client's bucket after this long without a
	// request; without eviction the map is an unbounded memory leak keyed by
	// attacker-chosen source addresses.
	rateLimitIdleTTL = 10 * time.Minute
	// rateLimitSweepEvery bounds how often eviction scans the map, so a busy
	// server does not walk every bucket on every request.
	rateLimitSweepEvery = time.Minute
)

// rateLimiter is a per-client token bucket.
//
// Stdlib only (no golang.org/x/time/rate) to avoid a new dependency for
// twenty lines of arithmetic. Tokens accrue continuously rather than in fixed
// windows, so a client cannot burst 2x the limit across a window edge.
type rateLimiter struct {
	rps   float64
	burst float64

	mu        sync.Mutex
	buckets   map[string]*tokenBucket
	lastSweep time.Time
	// now is injectable so tests can advance time without sleeping.
	now func() time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rps, burst float64) *rateLimiter {
	return &rateLimiter{
		rps:     rps,
		burst:   burst,
		buckets: make(map[string]*tokenBucket),
		now:     time.Now,
	}
}

// rateLimiterFromEnv builds the limiter from the environment, returning nil
// (i.e. disabled) when WA_RATE_LIMIT_RPS is 0.
//
// Configured by environment rather than through internal/config because that
// config is shared with the library embedding path, where HTTP rate limiting
// is meaningless.
//
//	WA_RATE_LIMIT_RPS    sustained requests per second per client (default 20);
//	                     set to 0 to disable rate limiting entirely
//	WA_RATE_LIMIT_BURST  bucket depth, i.e. the largest instantaneous burst
//	                     allowed (default 40)
//
// A value that does not parse falls back to the default: refusing to start
// over a malformed tuning knob is worse than running with a sane one.
func rateLimiterFromEnv() *rateLimiter {
	rps := envFloat("WA_RATE_LIMIT_RPS", defaultRateLimitRPS)
	if rps <= 0 {
		slog.Warn("api rate limiting disabled", "reason", "WA_RATE_LIMIT_RPS is not positive")
		return nil
	}
	burst := envFloat("WA_RATE_LIMIT_BURST", defaultRateLimitBurst)
	if burst < 1 {
		burst = 1
	}
	return newRateLimiter(rps, burst)
}

func envFloat(key string, def float64) float64 {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		slog.Warn("ignoring malformed rate limit setting", "key", key, "value", raw, "default", def)
		return def
	}
	return v
}

// allow consumes one token for key, reporting whether the request may proceed.
func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweepLocked(now)

	b, ok := l.buckets[key]
	if !ok {
		// A new client starts with a full bucket, so a single well-behaved
		// request is never rejected.
		b = &tokenBucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += elapsed.Seconds() * l.rps
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *rateLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < rateLimitSweepEvery {
		return
	}
	l.lastSweep = now
	for key, b := range l.buckets {
		if now.Sub(b.last) > rateLimitIdleTTL {
			delete(l.buckets, key)
		}
	}
}

// middleware rejects over-rate requests with 429. A nil limiter is the
// disabled case and passes everything through.
func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientKey(r)) {
			slog.Warn("rate limited",
				"request_id", RequestIDFromContext(r.Context()),
				"remote", r.RemoteAddr, "method", r.Method, "path", r.URL.Path)
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, codeRateLimited,
				"too many requests, slow down and retry")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientKey identifies the caller for rate limiting.
//
// The remote address without its port, so a client opening many connections
// still shares one bucket. X-Forwarded-For is deliberately ignored: it is
// caller-controlled, and trusting it would let one source mint an unlimited
// number of buckets and bypass the limit entirely. Behind a trusted proxy
// this therefore limits per proxy — have the proxy do the per-client limiting
// in that topology.
func clientKey(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// apiKeyAuth returns middleware that rate-limits and then validates the
// Bearer token.
//
// The rate limiter is composed in here rather than registered as its own
// middleware because this is the one the authenticated route group already
// installs (the route table lives in server.go). Keeping the two together
// also guarantees the limiter can never cover the liveness/readiness probes,
// which a kubelet must always be able to reach.
//
// It runs BEFORE the key check on purpose: an unauthenticated flood is the
// one this server most needs to survive, and limiting only authenticated
// requests would leave the credential check itself unthrottled.
func apiKeyAuth(apiKey string) func(http.Handler) http.Handler {
	limiter := rateLimiterFromEnv()
	return func(next http.Handler) http.Handler {
		authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// An empty configured key would otherwise accept any token:
			// refuse every request instead of silently running open.
			if apiKey == "" {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "server has no API key configured")
				return
			}
			auth := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(auth, prefix) {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid Authorization header")
				return
			}
			token := auth[len(prefix):]
			// Constant-time: a plain != leaks the length of the matching
			// prefix through timing, which over many requests recovers a
			// long-lived key byte by byte.
			if subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid API key")
				return
			}
			next.ServeHTTP(w, r)
		})
		return limiter.middleware(authed)
	}
}

// requestLogger logs each request with method, path, status, duration and the
// correlation id, so a log line can be tied to the X-Request-Id the caller
// saw.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.Info("request",
			"request_id", RequestIDFromContext(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

// statusWriter records the response status so middleware can log and count
// it, and tracks whether anything has been committed so a later stage does
// not write a second header.
type statusWriter struct {
	http.ResponseWriter
	status int
	// wrote is true once the status line is on the wire, whether set
	// explicitly or implied by the first Write.
	wrote bool
}

func (sw *statusWriter) WriteHeader(code int) {
	if sw.wrote {
		// net/http would log "superfluous response.WriteHeader call" and
		// ignore it; swallowing it here keeps that noise out of the logs.
		return
	}
	sw.status = code
	sw.wrote = true
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	sw.wrote = true
	return sw.ResponseWriter.Write(b)
}

// Flush and Hijack are pass-throughs. Wrapping a ResponseWriter hides every
// optional interface the original implements, so without these a streaming
// or upgrading handler added later would silently lose the capability — the
// classic symptom being an SSE endpoint that buffers until the handler
// returns.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (sw *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := sw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("api: underlying ResponseWriter does not support hijacking")
	}
	// The connection leaves HTTP entirely; nothing downstream may write a
	// status to it afterwards.
	sw.wrote = true
	return h.Hijack()
}

// Unwrap lets http.ResponseController reach capabilities not listed above.
func (sw *statusWriter) Unwrap() http.ResponseWriter { return sw.ResponseWriter }

// recoverer catches panics and returns 500.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// http.ErrAbortHandler is the documented way for a handler to
			// abandon a response: net/http expects to see it, suppresses the
			// stack trace and closes the connection. Swallowing it here
			// turned a deliberate abort into a bogus 500 appended to a
			// half-written response.
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			slog.Error("panic recovered",
				"request_id", RequestIDFromContext(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"panic", rec,
				"stack", string(debug.Stack()),
			)
			// If the handler already committed a status, the response is
			// partly on the wire: a second WriteHeader only produces a
			// "superfluous WriteHeader" log and appends an error envelope to
			// a body the client already parsed as something else.
			if sw.wrote {
				return
			}
			writeError(sw, http.StatusInternalServerError, codeInternal, "internal server error")
		}()
		next.ServeHTTP(sw, r)
	})
}
