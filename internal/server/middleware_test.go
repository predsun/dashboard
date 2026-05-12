package server

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTrustedProxyHonoursXFFWhenPeerTrusted(t *testing.T) {
	got := ""
	h := TrustedProxy([]*net.IPNet{mustCIDR(t, "10.0.0.0/8")})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = ClientIP(r.Context())
	}))

	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.RemoteAddr = "10.1.2.3:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if got != "203.0.113.7" {
		t.Fatalf("trusted peer should yield XFF head, got %q", got)
	}
}

func TestTrustedProxyIgnoresXFFFromUntrustedPeer(t *testing.T) {
	got := ""
	h := TrustedProxy([]*net.IPNet{mustCIDR(t, "10.0.0.0/8")})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = ClientIP(r.Context())
	}))

	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.RemoteAddr = "198.51.100.4:55555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if got != "198.51.100.4" {
		t.Fatalf("untrusted peer XFF must be ignored, got %q", got)
	}
}

func TestTrustedProxyEmptyListUsesRemoteAddr(t *testing.T) {
	got := ""
	h := TrustedProxy(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = ClientIP(r.Context())
	}))
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.RemoteAddr = "203.0.113.9:443"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != "203.0.113.9" {
		t.Fatalf("no trusted CIDRs should mean RemoteAddr, got %q", got)
	}
}

func TestSecurityHeadersSetAndCSPNonceUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		SecurityHeaders(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			seen[CSPNonce(r.Context())] = true
		})).ServeHTTP(w, r)

		resp := w.Result()
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("missing nosniff: %v", resp.Header)
		}
		csp := resp.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "'nonce-") {
			t.Errorf("CSP missing self / nonce: %q", csp)
		}
		if resp.Header.Get("Strict-Transport-Security") != "" {
			t.Errorf("HSTS should be absent when tls=false")
		}
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 unique nonces, got %d", len(seen))
	}
}

func TestCSRFGateBlocksUnverifiedPost(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/x", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := CSRFGate(false)(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("POST without csrf must 403, got %d", w.Code)
	}
}

func TestCSRFGateAllowsGet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := CSRFGate(false)(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET should pass csrf gate, got %d", w.Code)
	}
	// And a CSRF cookie should be on the way to seed the browser.
	if !cookieSet(w.Result().Cookies(), "dashboard_csrf") {
		t.Errorf("CSRF cookie should be seeded on GET")
	}
}

func cookieSet(cs []*http.Cookie, name string) bool {
	for _, c := range cs {
		if c.Name == name && c.Value != "" {
			return true
		}
	}
	return false
}
