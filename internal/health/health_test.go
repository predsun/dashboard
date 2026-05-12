package health

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/predsun/dashboard/internal/db"
	"github.com/predsun/dashboard/internal/models"
	"github.com/predsun/dashboard/internal/store"
)

func newTestWorker(t *testing.T) (*Worker, *sql.DB, func()) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	w := NewWorker(conn, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	// Tighter timings so tests stay snappy.
	w.Interval = 100 * time.Millisecond
	w.MaxBackoff = 2 * time.Second
	w.Timeout = 200 * time.Millisecond
	w.buildClient()

	cleanup := func() { _ = conn.Close() }
	return w, conn, cleanup
}

func mustCreateApp(t *testing.T, conn *sql.DB, url string) *models.App {
	t.Helper()
	a := &models.App{Name: "test", URL: url, HealthCheckEnabled: true}
	if err := (store.Apps{DB: conn}).Create(context.Background(), a); err != nil {
		t.Fatalf("create app: %v", err)
	}
	return a
}

func mustGetHealth(t *testing.T, conn *sql.DB, appID int64) *models.HealthStatus {
	t.Helper()
	h, err := (store.Health{DB: conn}).Get(context.Background(), appID)
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	return h
}

func TestProbeMarksUpForOK(t *testing.T) {
	w, conn, cleanup := newTestWorker(t)
	defer cleanup()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	app := mustCreateApp(t, conn, srv.URL)
	w.probe(context.Background(), app)

	st := mustGetHealth(t, conn, app.ID)
	if st.Status != StatusUp {
		t.Fatalf("expected up, got %q", st.Status)
	}
	if st.ConsecutiveFailures != 0 {
		t.Fatalf("expected 0 failures, got %d", st.ConsecutiveFailures)
	}
	if st.NextCheckAt == nil || *st.NextCheckAt <= time.Now().Unix() {
		t.Fatalf("expected next_check_at in the future")
	}
}

func TestProbeMarksDownAndBacksOff(t *testing.T) {
	w, conn, cleanup := newTestWorker(t)
	defer cleanup()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	app := mustCreateApp(t, conn, srv.URL)

	w.probe(context.Background(), app)
	st := mustGetHealth(t, conn, app.ID)
	if st.Status != StatusDown {
		t.Fatalf("first probe: expected down, got %q", st.Status)
	}
	if st.ConsecutiveFailures != 1 {
		t.Fatalf("expected 1 failure, got %d", st.ConsecutiveFailures)
	}
	first := *st.NextCheckAt

	w.probe(context.Background(), app)
	st = mustGetHealth(t, conn, app.ID)
	if st.ConsecutiveFailures != 2 {
		t.Fatalf("expected 2 failures, got %d", st.ConsecutiveFailures)
	}
	if *st.NextCheckAt <= first {
		t.Fatalf("next_check_at should advance: first=%d second=%d", first, *st.NextCheckAt)
	}
}

func TestProbeRecoversAfterSuccess(t *testing.T) {
	w, conn, cleanup := newTestWorker(t)
	defer cleanup()

	healthy := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy {
			w.WriteHeader(200)
		} else {
			http.Error(w, "boom", 500)
		}
	}))
	defer srv.Close()

	app := mustCreateApp(t, conn, srv.URL)

	w.probe(context.Background(), app)
	w.probe(context.Background(), app)
	st := mustGetHealth(t, conn, app.ID)
	if st.ConsecutiveFailures != 2 {
		t.Fatalf("expected 2 failures, got %d", st.ConsecutiveFailures)
	}

	healthy = true
	w.probe(context.Background(), app)
	st = mustGetHealth(t, conn, app.ID)
	if st.Status != StatusUp {
		t.Fatalf("expected up after recovery, got %q", st.Status)
	}
	if st.ConsecutiveFailures != 0 {
		t.Fatalf("expected failures reset after success, got %d", st.ConsecutiveFailures)
	}
}

func TestProbeTimesOut(t *testing.T) {
	w, conn, cleanup := newTestWorker(t)
	defer cleanup()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	app := mustCreateApp(t, conn, srv.URL)
	start := time.Now()
	w.probe(context.Background(), app)
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("probe should have timed out fast, took %v", elapsed)
	}
	st := mustGetHealth(t, conn, app.ID)
	if st.Status != StatusDown {
		t.Fatalf("timeout should mark down, got %q", st.Status)
	}
}

func TestComputeNextCheckBackoffCaps(t *testing.T) {
	w := &Worker{Interval: time.Minute, MaxBackoff: 5 * time.Minute}
	now := time.Unix(1_000_000, 0)
	got := w.computeNextCheck(now, false, 10)
	want := now.Add(5 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("expected cap at %v, got %v", want, got)
	}
}

func TestDispatchHonorsDueWatermark(t *testing.T) {
	w, conn, cleanup := newTestWorker(t)
	defer cleanup()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()

	app := mustCreateApp(t, conn, srv.URL)

	// First dispatch — row absent, app is due. After it returns, status=up
	// and next_check_at is in the future.
	w.dispatch(context.Background(), nil)
	// Race-free wait by polling for the row: we have at most 1 in-flight
	// goroutine launched without a WaitGroup here, so give it a moment.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		st, err := (store.Health{DB: conn}).Get(context.Background(), app.ID)
		if err == nil && st.Status == StatusUp {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("dispatch did not result in an up status within deadline")
}
