package migrations

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conductor.db")
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func version(t *testing.T, db *sql.DB) (int, bool) {
	t.Helper()
	var v int
	var dirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&v, &dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	return v, dirty
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name,
	).Scan(&n); err != nil {
		t.Fatalf("inspect %s: %v", name, err)
	}
	return n > 0
}

func TestRunCreatesTheBaseline(t *testing.T) {
	db := openTemp(t)
	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, table := range []string{
		"usage_records", "request_logs", "provider_health_records", "cost_rates",
		"model_status_records", "model_probe_meta", "routing_traces",
		"request_attempts", "tasks", "task_steps", "task_events",
		"task_tool_calls", "shadow_observations",
	} {
		if !tableExists(t, db, table) {
			t.Errorf("table %s was not created by the baseline", table)
		}
	}
	if v, dirty := version(t, db); v != BaselineVersion || dirty {
		t.Fatalf("version = %d dirty = %v, want %d clean", v, dirty, BaselineVersion)
	}
}

func TestRunIsIdempotent(t *testing.T) {
	db := openTemp(t)
	if err := Run(db); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := Run(db); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if v, dirty := version(t, db); v != BaselineVersion || dirty {
		t.Fatalf("version = %d dirty = %v, want %d clean", v, dirty, BaselineVersion)
	}
}

func TestRunBaselinesALegacyDatabaseWithoutTouchingIt(t *testing.T) {
	db := openTemp(t)
	// A database created by AutoMigrate before versioning: known tables, no
	// schema_migrations, one row that must survive.
	if _, err := db.Exec(`CREATE TABLE usage_records (id text PRIMARY KEY)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO usage_records (id) VALUES ('row-1')`); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	if err := Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var id string
	if err := db.QueryRow(`SELECT id FROM usage_records`).Scan(&id); err != nil {
		t.Fatalf("read preserved row: %v", err)
	}
	if id != "row-1" {
		t.Fatalf("row = %q, want row-1 (data must not be touched)", id)
	}
	if v, dirty := version(t, db); v != BaselineVersion || dirty {
		t.Fatalf("version = %d dirty = %v, want baseline %d clean", v, dirty, BaselineVersion)
	}
	// The baseline was stamped, not executed: a table that only 0001 creates
	// must not appear on a legacy database.
	if tableExists(t, db, "tasks") {
		t.Fatal("baseline was executed on a legacy database; it must only be stamped")
	}
}
