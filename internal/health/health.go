// Package health probes apps in the background and writes their up/down
// status to the database. Probes follow exponential backoff on failure so a
// long-down service isn't hammered, and concurrency is bounded so a single
// slow site can't starve the others.
package health

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/predsun/dashboard/internal/config"
	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/store"
)

const (
	// tickInterval is the wake-up interval for the scheduler. Probes themselves
	// are gated by next_check_at, so this controls only check-due granularity.
	tickInterval = 5 * time.Second

	// maxConcurrentProbes caps in-flight HTTP requests. A reasonable ceiling
	// for a personal VPS dashboard with at most a few dozen apps.
	maxConcurrentProbes = 10

	// maxRedirectDepth controls how far the client will chase 30x.
	maxRedirectDepth = 3
)

// Statuses persisted to the health_status table.
const (
	StatusUp      = "up"
	StatusDown    = "down"
	StatusUnknown = "unknown"
)

// Worker is the background scheduler + prober. One Worker per process.
type Worker struct {
	DB     *sql.DB
	Logger *slog.Logger

	// Tunables; zero-values fall back to defaults from config.
	Interval   time.Duration
	Timeout    time.Duration
	MaxBackoff time.Duration

	client *http.Client
	sem    chan struct{}
}

// NewWorker constructs a worker. Tunables are inherited from cfg at
// construction time; runtime changes to cfg are not picked up.
func NewWorker(db *sql.DB, logger *slog.Logger) *Worker {
	w := &Worker{
		DB:         db,
		Logger:     logger,
		Interval:   60 * time.Second,
		Timeout:    5 * time.Second,
		MaxBackoff: 30 * time.Minute,
		sem:        make(chan struct{}, maxConcurrentProbes),
	}
	w.buildClient()
	return w
}

// Configure overrides the defaults set in NewWorker, typically from config.
// Must be called before Run.
func (w *Worker) Configure(h config.HealthConfig) {
	if h.Interval > 0 {
		w.Interval = h.Interval
	}
	if h.Timeout > 0 {
		w.Timeout = h.Timeout
	}
	if h.MaxBackoff > 0 {
		w.MaxBackoff = h.MaxBackoff
	}
	w.buildClient()
}

func (w *Worker) buildClient() {
	w.client = &http.Client{
		Timeout: w.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirectDepth {
				return errors.New("redirect depth exceeded")
			}
			return nil
		},
	}
}

// Run blocks until ctx is cancelled, evaluating which apps are due every
// tickInterval. Probes happen in goroutines bounded by w.sem.
func (w *Worker) Run(ctx context.Context) {
	w.Logger.Info("health worker started",
		"interval", w.Interval, "timeout", w.Timeout, "max_backoff", w.MaxBackoff,
	)
	t := time.NewTicker(tickInterval)
	defer t.Stop()

	var probesWG sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			w.Logger.Info("health worker stopping; waiting for in-flight probes")
			probesWG.Wait()
			return
		case <-t.C:
			w.dispatch(ctx, &probesWG)
		}
	}
}

func (w *Worker) dispatch(ctx context.Context, wg *sync.WaitGroup) {
	now := time.Now().Unix()
	apps, err := (store.Apps{DB: w.DB}).DueForHealthCheck(ctx, now)
	if err != nil {
		w.Logger.Warn("listing due apps", "err", err)
		return
	}
	for _, a := range apps {
		select {
		case <-ctx.Done():
			return
		case w.sem <- struct{}{}:
			if wg != nil {
				wg.Add(1)
			}
			go func(app *models.App) {
				if wg != nil {
					defer wg.Done()
				}
				defer func() { <-w.sem }()
				w.probe(ctx, app)
			}(a)
		}
	}
}

// probe issues a single HTTP request and updates the row. Failures and
// successes both write the row so the next_check_at watermark advances
// either way.
func (w *Worker) probe(ctx context.Context, app *models.App) {
	target := app.HealthCheckURL
	if target == "" {
		target = app.URL
	}
	prev, _ := (store.Health{DB: w.DB}).Get(ctx, app.ID)

	ok := w.doRequest(ctx, target)
	now := time.Now()
	failures := 0
	if prev != nil {
		failures = prev.ConsecutiveFailures
	}

	status := StatusDown
	if ok {
		status = StatusUp
		failures = 0
	} else {
		failures++
	}

	nextCheckAt := w.computeNextCheck(now, ok, failures).Unix()
	lastCheckedAt := now.Unix()

	upsertErr := (store.Health{DB: w.DB}).Upsert(ctx, &models.HealthStatus{
		AppID:               app.ID,
		Status:              status,
		LastCheckedAt:       &lastCheckedAt,
		ConsecutiveFailures: failures,
		NextCheckAt:         &nextCheckAt,
	})
	if upsertErr != nil {
		w.Logger.Warn("upsert health", "app_id", app.ID, "err", upsertErr)
	}
}

// doRequest returns true on 2xx/3xx, false on any error or status >= 400.
// Uses GET (not HEAD) because some apps misimplement HEAD; the body is
// discarded.
func (w *Worker) doRequest(ctx context.Context, target string) bool {
	reqCtx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "dashboard-health-check/1.0")
	resp, err := w.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// Read & discard a small amount of body so the connection can be reused.
	_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

// computeNextCheck returns the absolute time of the next probe. On success
// it's now+interval; on failure it grows: now + min(interval * 2^failures, max).
// failures==1 means "this is the first failure"; backoff begins at 2x interval.
func (w *Worker) computeNextCheck(now time.Time, ok bool, failures int) time.Time {
	if ok {
		return now.Add(w.Interval)
	}
	backoff := w.Interval
	for i := 0; i < failures; i++ {
		backoff *= 2
		if backoff >= w.MaxBackoff {
			backoff = w.MaxBackoff
			break
		}
	}
	return now.Add(backoff)
}
