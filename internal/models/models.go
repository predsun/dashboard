// Package models defines the typed aggregates persisted by internal/store.
// Field tags match the JSON shape exposed over the API and used for the
// import/export format; the export format is deliberately stable.
package models

type App struct {
	ID                  int64  `json:"id"`
	Name                string `json:"name"`
	URL                 string `json:"url"`
	Description         string `json:"description"`
	IconPath            string `json:"icon_path"`
	CategoryID          *int64 `json:"category_id,omitempty"`
	SortOrder           int    `json:"sort_order"`
	HealthCheckEnabled  bool   `json:"health_check_enabled"`
	HealthCheckURL      string `json:"health_check_url"`
	CreatedAt           int64  `json:"created_at"`
	UpdatedAt           int64  `json:"updated_at"`
}

type Category struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
}

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"` // never serialized
	CreatedAt    int64  `json:"created_at"`
}

type Session struct {
	ID        string `json:"-"`
	UserID    int64  `json:"-"`
	ExpiresAt int64  `json:"-"`
	CreatedAt int64  `json:"-"`
}

// HealthStatus is one row of the health_status table.
type HealthStatus struct {
	AppID                int64  `json:"app_id"`
	Status               string `json:"status"` // up | down | unknown
	LastCheckedAt        *int64 `json:"last_checked_at,omitempty"`
	ConsecutiveFailures  int    `json:"consecutive_failures"`
	NextCheckAt          *int64 `json:"next_check_at,omitempty"`
}

// Settings keys. Kept as constants so misspellings show up at compile time.
const (
	SettingSetupComplete        = "setup_complete"
	SettingTheme                = "theme"
	SettingBackground           = "background"
	SettingGlassmorphism        = "glassmorphism_enabled"
	SettingSessionKey           = "session_key"
	SettingCSRFKey              = "csrf_key"
	SettingHealthInterval       = "health_interval_seconds"
	SettingHealthTimeout        = "health_timeout_seconds"
	SettingHealthMaxBackoff     = "health_max_backoff_seconds"
)
