package servicekit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func containsPoolLimit(value string) bool { return strings.Contains(value, "pool_max_conns") }

func sqlChecksum(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))))
}

// MigrateSchema serializes even first-time schema creation across deploy jobs.
func MigrateSchema(ctx context.Context, url, schema string, sources fs.FS) error {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return err
	}
	config.MaxConns = 1
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return err
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "asset-platform/migrate/"+schema); err != nil {
		return err
	}
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+ident+`; CREATE TABLE IF NOT EXISTS `+ident+`.schema_migrations(version text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT clock_timestamp())`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(sources, "migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := fs.ReadFile(sources, "migrations/"+entry.Name())
		if err != nil {
			return err
		}
		var checksum string
		err = tx.QueryRow(ctx, `SELECT checksum FROM `+ident+`.schema_migrations WHERE version=$1`, entry.Name()).Scan(&checksum)
		if err == nil {
			if checksum != sqlChecksum(data) {
				return fmt.Errorf("%s migration %s checksum changed", schema, entry.Name())
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("%s migration %s: %w", schema, entry.Name(), err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO `+ident+`.schema_migrations(version,checksum) VALUES($1,$2)`, entry.Name(), sqlChecksum(data)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func CheckSchema(ctx context.Context, pool *pgxpool.Pool, schema string, sources fs.FS) error {
	entries, err := fs.ReadDir(sources, "migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := fs.ReadFile(sources, "migrations/"+entry.Name())
		if err != nil {
			return err
		}
		var checksum string
		if err = pool.QueryRow(ctx, `SELECT checksum FROM `+pgx.Identifier{schema}.Sanitize()+`.schema_migrations WHERE version=$1`, entry.Name()).Scan(&checksum); err != nil {
			return fmt.Errorf("%s schema missing/outdated: run --migrate-only: %w", schema, err)
		}
		if checksum != sqlChecksum(data) {
			return fmt.Errorf("%s migration %s checksum mismatch", schema, entry.Name())
		}
	}
	return nil
}
