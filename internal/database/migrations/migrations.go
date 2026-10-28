// Package migrations owns the versioned schema history of Conductor's SQLite
// database.
//
// Before this package, the schema was whatever `gorm.AutoMigrate` inferred from
// the current models: there was no version, no record of what ran, and no way to
// apply a change that AutoMigrate cannot express (renames, data backfills,
// constraint changes). The migrations here are authoritative; `Database.Migrate`
// still runs AutoMigrate afterwards as an additive safety net while every table
// is migrated out of model inference.
//
// Baseline policy: a database created before versioning already has the schema
// that 0001 describes. `Run` detects that (no schema_migrations table, but known
// tables present) and stamps version 1 without executing 0001, so existing
// deployments are not re-created and no data is touched.
package migrations

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed sql/*.sql
var files embed.FS

// BaselineVersion is the version a pre-versioning database is stamped with.
const BaselineVersion = 1

// Run applies every pending migration to db.
//
// It is idempotent: a second call is a no-op, and a pre-versioning database is
// baselined at version 1 instead of being rebuilt.
func Run(db *sql.DB) error {
	// Detect a pre-versioning database BEFORE constructing the migrate
	// instance: the sqlite3 driver creates schema_migrations on construction,
	// which would make every legacy database look versioned.
	legacy, err := isLegacySchema(db)
	if err != nil {
		return err
	}

	src, err := iofs.New(files, "sql")
	if err != nil {
		return fmt.Errorf("migrations: source: %w", err)
	}
	driver, err := sqlite3.WithInstance(db, &sqlite3.Config{})
	if err != nil {
		return fmt.Errorf("migrations: driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite3", driver)
	if err != nil {
		return fmt.Errorf("migrations: init: %w", err)
	}

	if legacy {
		// Stamp the baseline without executing it: the tables it creates are
		// already there, and rebuilding them would drop user data.
		if err := m.Force(BaselineVersion); err != nil {
			return fmt.Errorf("migrations: baseline at version %d: %w", BaselineVersion, err)
		}
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrations: up: %w", err)
	}
	return nil
}

// isLegacySchema reports whether db has known tables but no migration history.
func isLegacySchema(db *sql.DB) (bool, error) {
	var versioned int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`,
	).Scan(&versioned); err != nil {
		return false, fmt.Errorf("migrations: inspect schema_migrations: %w", err)
	}
	if versioned > 0 {
		return false, nil
	}

	var known int
	if err := db.QueryRow(`
		SELECT count(*) FROM sqlite_master
		WHERE type = 'table'
		  AND name IN ('usage_records', 'request_logs', 'routing_traces', 'tasks', 'shadow_observations')
	`).Scan(&known); err != nil {
		return false, fmt.Errorf("migrations: inspect known tables: %w", err)
	}
	return known > 0, nil
}
