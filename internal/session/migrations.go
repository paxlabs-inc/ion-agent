package session

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/paxlabs-inc/ion-agent/migrations"
)

type migration struct {
	version int
	name    string
	sql     string
}

func applyMigrations(ctx context.Context, db *sql.DB, now time.Time) error {
	available, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, item := range available {
		applied, checkErr := migrationApplied(ctx, db, item.version)
		if checkErr != nil {
			return checkErr
		}
		if applied {
			continue
		}
		if applyErr := applyMigration(ctx, db, item, now); applyErr != nil {
			return applyErr
		}
	}
	return nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		return nil, fmt.Errorf("session: list migrations: %w", err)
	}
	available := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		prefix, _, found := strings.Cut(entry.Name(), "_")
		if !found {
			return nil, fmt.Errorf("session: invalid migration name %q", entry.Name())
		}
		version, parseErr := strconv.Atoi(prefix)
		if parseErr != nil || version <= 0 {
			return nil, fmt.Errorf("session: invalid migration version %q", prefix)
		}
		content, readErr := fs.ReadFile(migrations.Files, entry.Name())
		if readErr != nil {
			return nil, fmt.Errorf("session: read migration %q: %w", entry.Name(), readErr)
		}
		available = append(available, migration{
			version: version,
			name:    entry.Name(),
			sql:     string(content),
		})
	}
	sort.Slice(available, func(left, right int) bool {
		return available[left].version < available[right].version
	})
	return available, nil
}

func migrationApplied(ctx context.Context, db *sql.DB, version int) (bool, error) {
	var exists int
	err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM sqlite_master
		 WHERE type = 'table' AND name = 'schema_migrations'`,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("session: inspect migration table: %w", err)
	}
	if exists == 0 {
		return false, nil
	}

	var count int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		version,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("session: inspect migration version: %w", err)
	}
	return count == 1, nil
}

func applyMigration(ctx context.Context, db *sql.DB, item migration, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("session: begin migration %q: %w", item.name, err)
	}
	committed := false
	defer func() {
		if !committed {
			// Best-effort rollback after the primary migration error.
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, item.sql); err != nil {
		return fmt.Errorf("session: execute migration %q: %w", item.name, err)
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
		item.version,
		toMicros(now),
	); err != nil {
		return fmt.Errorf("session: record migration %q: %w", item.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("session: commit migration %q: %w", item.name, err)
	}
	committed = true
	return nil
}
