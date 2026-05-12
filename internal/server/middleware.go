package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/predsun/dashboard/internal/auth"
	"github.com/predsun/dashboard/internal/models"
)

// ctxKey is the type used for all request-scoped context values defined here.
type ctxKey int

const (
	ctxKeyCSPNonce ctxKey = iota
	ctxKeyClientIP
	ctxKeySession
	ctxKeyCSRFToken
)

// CSPNonce returns the per-request CSP nonce. Templates inject it into the
// single inline <script> tag we allow (the CSRF bootstrap).
func CSPNonce(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyCSPNonce).(string)
	return v
}

// ClientIP returns the request's client IP after trusted-proxy normalization.
func ClientIP(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyClientIP).(string)
	return v
}

// Session returns the authenticated session, or nil if anonymous.
func Session(ctx context.Context) *models.Session {
	v, _ := ctx.Value(ctxKeySession).(*models.Session)
	return v
}

// CSRFToken returns the current CSRF token, populated by CSRFGate. Templates
// embed this in form fields and the JS layer mirrors it in the X-CSRF-Token
// header on XHR.
func CSRFToken(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyCSRFToken).(string)
	return v
}

// statusWriter captures the status code for logging without buffering the body.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.status == 0 {
		sw.status = http.StatusOK
	}
	n, err := sw.ResponseWriter.Write(b)
	sw.bytes += n
	return n, err
}

// Recover converts any handler panic into a 500. Stack trace goes to the log,
// not the response body.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic in handler",
						"path", r.URL.Path,
						"method", r.Method,
						"panic", fmt.Sprint(rec),
						"stack", string(debug.Stack()),
					)
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AccessLog records a single line per request: method, path, status, duration, ip.
func AccessLog(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			logger.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"bytes", sw.bytes,
				"dur_ms", time.Since(start).Milliseconds(),
				"ip", ClientIP(r.Context()),
			)
		})
	}
}

// TrustedProxy normalizes RemoteAddr against X-Forwarded-* headers, but only
// when the connecting peer is in one of the trusted CIDRs. Stashes the result
// at ctxKeyClientIP.
func TrustedProxy(trusted []*net.IPNet) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := normalizeClientIP(r, trusted)
			ctx := context.WithValue(r.Context(), ctxKeyClientIP, ip)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func normalizeClientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if len(trusted) == 0 {
		return host
	}
	peer := net.ParseIP(host)
	if peer == nil {
		return host
	}
	for _, n := range trusted {
		if n.Contains(peer) {
			// Trust the leftmost untrusted hop in X-Forwarded-For.
			xff := r.Header.Get("X-Forwarded-For")
			for _, part := range strings.Split(xff, ",") {
				p := strings.TrimSpace(part)
				if p == "" {
					continue
				}
				return p
			}
			break
		}
	}
	return host
}

// SecurityHeaders sets baseline hardening headers and generates a per-request
// CSP nonce. We allow `style-src 'unsafe-inline'` because Tailwind's @apply
// emits inline-equivalent styles that hash-based CSP would have to ratchet;
// scripts have no such concession.
func SecurityHeaders(tls bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nonce, err := nonceB64(16)
			if err != nil {
				http.Error(w, "nonce error", http.StatusInternalServerError)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyCSPNonce, nonce)

			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "interest-cohort=()")
			// CSP notes:
			//   - 'unsafe-eval' on script-src is required by Alpine.js's standard
			//     build (it uses Function() for x-data expressions). Switching to
			//     @alpinejs/csp removes the need; deferred to M13 polish so we
			//     don't have to rewrite every template right now.
			//   - 'unsafe-inline' on style-src covers Tailwind-emitted inline
			//     style attributes from interactive utility classes.
			//   - frame-ancestors 'none' prevents clickjacking.
			h.Set("Content-Security-Policy",
				"default-src 'self'; "+
					"script-src 'self' 'unsafe-eval' 'nonce-"+nonce+"'; "+
					"style-src 'self' 'unsafe-inline'; "+
					"img-src 'self' data:; "+
					"font-src 'self'; "+
					"connect-src 'self'; "+
					"frame-ancestors 'none'; "+
					"base-uri 'self'; "+
					"form-action 'self'")
			if tls {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func nonceB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(b), nil
}

// CSRFGate enforces the double-submit token on every state-changing method.
// Safe methods (GET, HEAD, OPTIONS) pass through but EnsureCSRFCookie has
// already given the browser a token for the next mutation.
func CSRFGate(secureTLS bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Seed cookie for safe methods so the browser has a token to send back.
			tok, err := auth.EnsureCSRFCookie(w, r, secureTLS)
			if err != nil {
				http.Error(w, "csrf init failed", http.StatusInternalServerError)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyCSRFToken, tok)
			r = r.WithContext(ctx)
			if !isStateChanging(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if err := auth.VerifyCSRF(r); err != nil {
				http.Error(w, "forbidden: csrf", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// SessionLoader looks up the request's session (if any) and stashes it in ctx.
// Anonymous requests carry a nil session — downstream auth gates decide what
// that means.
func SessionLoader(sm *auth.SessionManager) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess := sm.Load(r.Context(), r)
			if sess != nil {
				ctx := context.WithValue(r.Context(), ctxKeySession, sess)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAuth returns 401 (XHR) or redirects to /login (browser) when no
// session is present.
func RequireAuth() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if Session(r.Context()) == nil {
				if wantsJSON(r) {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func wantsJSON(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// SetupGate redirects to /setup until the setup wizard has completed, except
// for the static asset and health endpoints (which must always be reachable
// so reverse proxies and load balancers work even pre-setup).
func SetupGate(isComplete func() bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			if isComplete() ||
				p == "/setup" ||
				p == "/healthz" ||
				p == "/readyz" ||
				strings.HasPrefix(p, "/static/") {
				next.ServeHTTP(w, r)
				return
			}
			http.Redirect(w, r, "/setup", http.StatusTemporaryRedirect)
		})
	}
}

// ResponseError writes a JSON error response with the supplied status.
func ResponseError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, msg)
}

