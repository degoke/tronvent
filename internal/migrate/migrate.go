package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version text PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
);
`

// Up applies all unapplied *.up.sql files from migrationsFS in lexicographic order.
func Up(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS) error {
	if _, err := pool.Exec(ctx, migrationsTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.Glob(migrationsFS, "*.up.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)
	if len(entries) == 0 {
		return fmt.Errorf("no *.up.sql migrations found")
	}

	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}

	for _, name := range entries {
		version := strings.TrimSuffix(name, ".up.sql")
		if applied[version] {
			fmt.Printf("skip  %s (already applied)\n", version)
			continue
		}

		sqlBytes, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin %s: %w", version, err)
		}

		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO schema_migrations (version) VALUES ($1)
		`, version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", version, err)
		}
		fmt.Printf("apply %s\n", version)
	}
	return nil
}

func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan migration version: %w", err)
		}
		out[version] = true
	}
	return out, rows.Err()
}
