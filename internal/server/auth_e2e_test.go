package server

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/predsun/dashboard/internal/auth"
	"github.com/predsun/dashboard/internal/config"
	"github.com/predsun/dashboard/internal/db"
	"github.com/predsun/dashboard/web"
)

func newTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := config.Defaults()
	cfg.DataDir = dir
	cfg.TrustedProxies = nil

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	srv, err := New(cfg, conn, logger)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	staticFS, err := fs.Sub(web.FS, "static")
	if err != nil {
		t.Fatalf("static subfs: %v", err)
	}
	httpsrv := httptest.NewServer(srv.routes(staticFS))
	t.Cleanup(httpsrv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		// Don't follow redirects — we want to assert on the 303s.
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}
	return httpsrv, client
}

func TestSetupThenLoginFlow(t *testing.T) {
	srv, client := newTestServer(t)

	// 1) GET /setup — wizard renders, sets CSRF cookie.
	resp, err := client.Get(srv.URL + "/setup")
	if err != nil {
		t.Fatalf("get /setup: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/setup status: got %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `name="csrf_token"`) {
		t.Fatalf("/setup body missing csrf token field: %s", body)
	}
	csrf := readCookie(client.Jar, srv.URL, auth.CSRFCookieName)
	if csrf == "" {
		t.Fatal("expected csrf cookie after GET /setup")
	}

	// 2) POST /setup with valid credentials → 303 to /, session set.
	form := url.Values{
		"csrf_token":       {csrf},
		"username":         {"admin"},
		"password":         {"correcthorse"},
		"password_confirm": {"correcthorse"},
		"seed_examples":    {"on"},
	}
	req := mustReq(t, http.MethodPost, srv.URL+"/setup", form, csrf)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("post /setup: %v", err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("/setup post status: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("/setup post redirect: got %q", loc)
	}
	if readCookie(client.Jar, srv.URL, auth.SessionCookieName) == "" {
		t.Fatal("expected session cookie after successful setup")
	}

	// 3) GET / with session → 200 (placeholder body for M5; real dashboard in M7).
	resp, err = client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/ as logged-in user: got %d, body=%s", resp.StatusCode, readBody(t, resp))
	}

	// 4) POST /logout → 303 to /login, session cookie cleared.
	csrf = readCookie(client.Jar, srv.URL, auth.CSRFCookieName)
	resp, err = client.Do(mustReq(t, http.MethodPost, srv.URL+"/logout", url.Values{"csrf_token": {csrf}}, csrf))
	if err != nil {
		t.Fatalf("post /logout: %v", err)
	}
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("/logout: got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// 5) GET / without session → 303 to /login.
	resp, err = client.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("/ anonymous: got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// 6) Bad password → 401 with form re-rendered.
	csrf = readCookie(client.Jar, srv.URL, auth.CSRFCookieName)
	form = url.Values{
		"csrf_token": {csrf},
		"username":   {"admin"},
		"password":   {"wrong"},
	}
	resp, err = client.Do(mustReq(t, http.MethodPost, srv.URL+"/login", form, csrf))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad password should 401, got %d", resp.StatusCode)
	}

	// 7) Correct password → 303 to /, new session.
	form.Set("password", "correcthorse")
	resp, err = client.Do(mustReq(t, http.MethodPost, srv.URL+"/login", form, csrf))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("login success: got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestSetupGateRedirectsBeforeFirstUser(t *testing.T) {
	srv, client := newTestServer(t)
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("anonymous GET / pre-setup: got %d", resp.StatusCode)
	}
	if !strings.HasSuffix(resp.Header.Get("Location"), "/setup") {
		t.Fatalf("expected redirect to /setup, got %q", resp.Header.Get("Location"))
	}
}

func mustReq(t *testing.T, method, target string, form url.Values, csrf string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-CSRF-Token", csrf)
	return r
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readCookie(jar http.CookieJar, srvURL, name string) string {
	u, _ := url.Parse(srvURL)
	for _, c := range jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}
