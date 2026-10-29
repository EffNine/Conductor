package migrations

import (
	"database/sql"
	"path/filepath"
	"strings"
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
	if v, dirty := version(t, db); v != LatestVersion || dirty {
		t.Fatalf("version = %d dirty = %v, want %d clean", v, dirty, LatestVersion)
	}
	if !tableExists(t, db, "users") || !tableExists(t, db, "api_keys") {
		t.Fatal("0002 did not create the identity tables")
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
	if v, dirty := version(t, db); v != LatestVersion || dirty {
		t.Fatalf("version = %d dirty = %v, want %d clean", v, dirty, LatestVersion)
	}
}

func TestRunBaselinesALegacyDatabaseWithoutTouchingIt(t *testing.T) {
	db := openTemp(t)
	// A real pre-versioning database: the full 0001 schema, no migration
	// history, and a row that must survive. (AutoMigrate always created every
	// table, so a partial schema is not a supported case.)
	applyBaseline(t, db)
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
	if v, dirty := version(t, db); v != LatestVersion || dirty {
		t.Fatalf("version = %d dirty = %v, want %d clean", v, dirty, LatestVersion)
	}
	// The baseline was stamped, not executed (no duplicate-table error), and
	// 0002 ran on top: identity exists and the legacy row is attributed.
	if !tableExists(t, db, "users") {
		t.Fatal("0002 did not run on the legacy database")
	}
	var userID string
	if err := db.QueryRow(`SELECT user_id FROM usage_records WHERE id = 'row-1'`).Scan(&userID); err != nil {
		t.Fatalf("read attribution: %v", err)
	}
	if userID != "default" {
		t.Fatalf("legacy row user_id = %q, want default", userID)
	}
}

// applyBaseline executes the embedded 0001 schema without going through
// golang-migrate, producing the exact state a pre-versioning deployment has.
func applyBaseline(t *testing.T, db *sql.DB) {
	t.Helper()
	data, err := files.ReadFile("sql/0001_baseline.up.sql")
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	for _, statement := range strings.Split(string(data), ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("apply baseline statement: %v", err)
		}
	}
}
