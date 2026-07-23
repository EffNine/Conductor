package database_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/config"
	"github.com/EffNine/conductor/internal/database"
	"github.com/EffNine/conductor/internal/health"
)

func TestModelStatusPersistenceRoundTrip(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	db, err := database.Connect(&config.DatabaseConfig{
		Driver:       "sqlite",
		DSN:          dsn,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err = db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := health.NewModelStatusStore(1, false)
	persist := health.NewDBStatusPersistence(db)
	store.SetPersistence(persist)

	store.RecordSuccess("nvidia_nim/good", "nvidia_nim", "good", 15)
	store.RecordFailure("nvidia_nim/bad", "nvidia_nim", "bad", "model not found", 404)
	store.MarkProviderFilterReady("nvidia_nim")
	store.MarkFilterReady()

	// Simulate process restart.
	store2 := health.NewModelStatusStore(1, false)
	n, err := health.RestoreModelStatusStore(store2, db)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if n != 2 {
		t.Fatalf("restored models = %d, want 2", n)
	}
	if !store2.FilterReady() {
		t.Fatal("filter ready should restore")
	}
	if !store2.ShouldAdvertise("nvidia_nim/good", "nvidia_nim") {
		t.Fatal("good should advertise after restore")
	}
	if store2.ShouldAdvertise("nvidia_nim/bad", "nvidia_nim") {
		t.Fatal("bad should stay hidden after restore")
	}
	st := store2.Get("nvidia_nim/good")
	if st == nil || st.LatencyMs != 15 || st.CheckedAt.IsZero() {
		t.Fatalf("unexpected restored status: %+v", st)
	}
	// Ensure CheckedAt survived as UTC-ish time.
	if time.Since(st.CheckedAt) > time.Minute {
		t.Fatalf("CheckedAt too old: %v", st.CheckedAt)
	}
}
