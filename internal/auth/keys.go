package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/store"
)

// EnsurePersistentKeys reads (or generates) the session and CSRF secrets from
// the settings table. They live in the DB rather than on disk so the SQLite
// file alone is enough to restore service. The file inherits the data dir's
// 0750 perms; the install script tightens that further to dashboard:dashboard.
//
// The keys returned by this function aren't currently used as MACs (sessions
// are random opaque IDs stored server-side and CSRF uses double-submit), but
// having them ready makes it cheap to swap in signed cookies later.
func EnsurePersistentKeys(ctx context.Context, db *sql.DB) (sessionKey, csrfKey []byte, err error) {
	s := store.Settings{DB: db}

	sessionKey, err = loadOrCreate(ctx, s, models.SettingSessionKey)
	if err != nil {
		return nil, nil, fmt.Errorf("session key: %w", err)
	}
	csrfKey, err = loadOrCreate(ctx, s, models.SettingCSRFKey)
	if err != nil {
		return nil, nil, fmt.Errorf("csrf key: %w", err)
	}
	return sessionKey, csrfKey, nil
}

func loadOrCreate(ctx context.Context, s store.Settings, key string) ([]byte, error) {
	v, err := s.Get(ctx, key)
	if err == nil && v != "" {
		decoded, decErr := hex.DecodeString(v)
		if decErr == nil && len(decoded) == 32 {
			return decoded, nil
		}
		// Fall through and regenerate if stored value is corrupt / wrong length.
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	if err := s.Set(ctx, key, hex.EncodeToString(buf)); err != nil {
		return nil, err
	}
	return buf, nil
}
