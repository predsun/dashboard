package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/predsun/dashboard/internal/auth"
	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/store"
)

// Auth serves /login and /logout. Rate limiting and session issuance happen
// here; the middleware chain has already attached the CSRF token and client IP
// to the request context.
type Auth struct {
	DB             *sql.DB
	Render         SetupRenderer // same minimal interface as Setup uses
	IssueSession   func(ctx context.Context, w http.ResponseWriter, userID int64) (string, error)
	RevokeSession  func(ctx context.Context, w http.ResponseWriter, sessionID string) error
	SessionFrom    func(r *http.Request) *models.Session
	ClientIPFrom   func(r *http.Request) string
	Limiter        *auth.LoginLimiter
	Logger         *slog.Logger
}

type loginView struct {
	Error string
}

func (h Auth) LoginGet(w http.ResponseWriter, r *http.Request) {
	if h.SessionFrom(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.Render.Render(w, r, http.StatusOK, "login", loginView{})
}

func (h Auth) LoginPost(w http.ResponseWriter, r *http.Request) {
	ip := h.ClientIPFrom(r)
	if ok, retry := h.Limiter.Allow(ip); !ok {
		w.Header().Set("Retry-After", retrySeconds(retry))
		h.Render.Render(w, r, http.StatusTooManyRequests, "login", loginView{
			Error: "Too many attempts. Please wait a few minutes.",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		h.failLogin(w, r, "Could not read form.")
		return
	}
	username := r.PostFormValue("username")
	pw := r.PostFormValue("password")

	user, err := (store.Users{DB: h.DB}).ByUsername(r.Context(), username)
	if err != nil {
		// Same response shape on missing user vs bad password — don't leak which.
		if !errors.Is(err, store.ErrNotFound) {
			h.Logger.Warn("login lookup error", "err", err)
		}
		h.failLogin(w, r, "Invalid username or password.")
		return
	}
	if err := auth.VerifyPassword(user.PasswordHash, pw); err != nil {
		h.failLogin(w, r, "Invalid username or password.")
		return
	}

	if _, err := h.IssueSession(r.Context(), w, user.ID); err != nil {
		h.Logger.Error("issue session", "err", err)
		h.failLogin(w, r, "Could not start session.")
		return
	}
	h.Limiter.Reset(ip)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h Auth) LogoutPost(w http.ResponseWriter, r *http.Request) {
	if sess := h.SessionFrom(r); sess != nil {
		_ = h.RevokeSession(r.Context(), w, sess.ID)
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h Auth) failLogin(w http.ResponseWriter, r *http.Request, msg string) {
	h.Render.Render(w, r, http.StatusUnauthorized, "login", loginView{Error: msg})
}

func retrySeconds(d time.Duration) string {
	s := int(d.Seconds())
	if s < 1 {
		s = 1
	}
	return strconv.Itoa(s)
}
