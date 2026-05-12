package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/store"
)

const (
	SessionCookieName = "dashboard_session"
	SessionTTL        = 30 * 24 * time.Hour // 30 days; can revoke server-side
)

// SessionManager creates, loads, and revokes browser sessions.
// Sessions are persisted in SQLite so they survive restarts.
type SessionManager struct {
	DB        *sql.DB
	SecureTLS bool // sets Secure attribute on cookies when serving TLS
}

func NewSessionManager(db *sql.DB, secure bool) *SessionManager {
	return &SessionManager{DB: db, SecureTLS: secure}
}

// Issue creates a new session for userID, writes the cookie, and returns the ID.
func (m *SessionManager) Issue(ctx context.Context, w http.ResponseWriter, userID int64) (string, error) {
	id, err := randomHex(32)
	if err != nil {
		return "", err
	}
	if _, err := (store.Sessions{DB: m.DB}).Create(ctx, id, userID, SessionTTL); err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.SecureTLS,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(SessionTTL.Seconds()),
	})
	return id, nil
}

// Load returns the session matching the request cookie, or nil if absent /
// expired / invalid. Distinguishing the error type isn't useful to callers —
// any failure should drop them back to "anonymous".
func (m *SessionManager) Load(ctx context.Context, r *http.Request) *models.Session {
	c, err := r.Cookie(SessionCookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	sess, err := (store.Sessions{DB: m.DB}).Get(ctx, c.Value)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return nil
	}
	return sess
}

// Revoke deletes the session server-side and clears the cookie client-side.
func (m *SessionManager) Revoke(ctx context.Context, w http.ResponseWriter, sessionID string) error {
	err := (store.Sessions{DB: m.DB}).Delete(ctx, sessionID)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.SecureTLS,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	return err
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
