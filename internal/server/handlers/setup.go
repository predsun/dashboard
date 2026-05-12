package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/predsun/dashboard/internal/auth"
	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/store"
)

// SetupRenderer is the subset of *server.Renderer that Setup needs. Declared
// as an interface so handler tests can stub it without pulling the renderer
// in.
type SetupRenderer interface {
	Render(w http.ResponseWriter, r *http.Request, status int, name string, data any)
}

// Setup serves the first-run wizard. The middleware chain has already gated
// every other route to this page until setup_complete=1 is written.
type Setup struct {
	DB           *sql.DB
	Render       SetupRenderer
	IssueSession func(ctx context.Context, w http.ResponseWriter, userID int64) (string, error)
}

type setupView struct {
	Username string
	Error    string
}

func (h Setup) Get(w http.ResponseWriter, r *http.Request) {
	if h.alreadyComplete(r.Context()) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.Render.Render(w, r, http.StatusOK, "setup", setupView{})
}

func (h Setup) Post(w http.ResponseWriter, r *http.Request) {
	if h.alreadyComplete(r.Context()) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, "could not read form", "")
		return
	}

	username := r.PostFormValue("username")
	pw := r.PostFormValue("password")
	pwConfirm := r.PostFormValue("password_confirm")
	seed := r.PostFormValue("seed_examples") != ""

	if username == "" {
		h.fail(w, r, "Username is required.", username)
		return
	}
	if pw != pwConfirm {
		h.fail(w, r, "Passwords do not match.", username)
		return
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		if errors.Is(err, auth.ErrPasswordTooShort) {
			h.fail(w, r, "Password must be at least 8 characters.", username)
			return
		}
		h.fail(w, r, "Could not hash password.", username)
		return
	}

	// Wrap the entire wizard payload in a transaction so a half-applied setup
	// can't leave the install in an unusable state.
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		h.fail(w, r, "database busy, try again", username)
		return
	}
	defer func() { _ = tx.Rollback() }() // safe no-op after Commit

	users := store.Users{DB: tx}
	if n, _ := users.Count(r.Context()); n > 0 {
		h.fail(w, r, "Setup already completed.", username)
		return
	}
	user, err := users.Create(r.Context(), username, hash)
	if err != nil {
		h.fail(w, r, "Could not create user.", username)
		return
	}
	if seed {
		if err := seedExampleApps(r.Context(), store.Apps{DB: tx}, store.Categories{DB: tx}); err != nil {
			h.fail(w, r, "Could not seed example apps.", username)
			return
		}
	}
	settings := store.Settings{DB: tx}
	if err := settings.Set(r.Context(), models.SettingSetupComplete, "1"); err != nil {
		h.fail(w, r, "Could not finalize setup.", username)
		return
	}
	// Sensible defaults for the rest of the settings table — only set if not
	// already present, so re-running the wizard (if ever supported) doesn't
	// stomp user choices.
	for _, kv := range [][2]string{
		{models.SettingTheme, "auto"},
		{models.SettingGlassmorphism, "0"},
	} {
		if _, err := settings.Get(r.Context(), kv[0]); errors.Is(err, store.ErrNotFound) {
			_ = settings.Set(r.Context(), kv[0], kv[1])
		}
	}
	if err := tx.Commit(); err != nil {
		h.fail(w, r, "Could not commit setup.", username)
		return
	}

	// Sign the admin in immediately so they land on the dashboard, not /login.
	if _, err := h.IssueSession(r.Context(), w, user.ID); err != nil {
		// Setup is recorded; degrade gracefully to the login page.
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h Setup) alreadyComplete(ctx context.Context) bool {
	v, _ := (store.Settings{DB: h.DB}).GetOrDefault(ctx, models.SettingSetupComplete, "0")
	return v == "1"
}

func (h Setup) fail(w http.ResponseWriter, r *http.Request, msg, username string) {
	h.Render.Render(w, r, http.StatusBadRequest, "setup", setupView{
		Username: username,
		Error:    msg,
	})
}

// seedExampleApps drops three plausible-looking entries so the empty dashboard
// shows shape rather than a single "you have no apps" CTA on first login.
func seedExampleApps(ctx context.Context, apps store.Apps, cats store.Categories) error {
	monitoring := &models.Category{Name: "Monitoring", SortOrder: 0}
	if err := cats.Create(ctx, monitoring); err != nil {
		return err
	}
	media := &models.Category{Name: "Media", SortOrder: 1}
	if err := cats.Create(ctx, media); err != nil {
		return err
	}
	mID, dID := monitoring.ID, media.ID

	examples := []*models.App{
		{Name: "Grafana", URL: "https://grafana.example.com", Description: "Metrics", CategoryID: &mID, SortOrder: 0},
		{Name: "Uptime Kuma", URL: "https://uptime.example.com", Description: "Uptime monitor", CategoryID: &mID, SortOrder: 1},
		{Name: "Jellyfin", URL: "https://media.example.com", Description: "Media server", CategoryID: &dID, SortOrder: 2},
	}
	for _, a := range examples {
		if err := apps.Create(ctx, a); err != nil {
			return err
		}
	}
	return nil
}
