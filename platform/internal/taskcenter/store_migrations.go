package taskcenter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Git checkouts may normalize SQL files between LF and CRLF. Register a
// canonical checksum, and recognize legacy raw checksums without rerunning DDL.
func migrationChecksum(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))))
}
func migrationChecksumMatches(data []byte, registered string) bool {
	canonical := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	return registered == migrationChecksum(data) || registered == fmt.Sprintf("%x", sha256.Sum256(data)) || registered == fmt.Sprintf("%x", sha256.Sum256(bytes.ReplaceAll(canonical, []byte("\n"), []byte("\r\n"))))
}

// Migrate runs only from the explicit migrate-only command, never at runtime.
func Migrate(ctx context.Context, databaseURL string, cutover bool) error {
	config, err := pgxpool.ParseConfig(databaseURL)
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
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('asset-platform/migrate/task_report'))`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS task_report;
 CREATE TABLE IF NOT EXISTS task_report.schema_migrations(version text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT clock_timestamp());
 LOCK TABLE task_report.schema_migrations IN EXCLUSIVE MODE`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		data, readErr := migrations.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			return readErr
		}
		checksum := migrationChecksum(data)
		var existing string
		rows, queryErr := tx.Query(ctx, `SELECT checksum FROM task_report.schema_migrations WHERE version=$1`, entry.Name())
		if queryErr != nil {
			return queryErr
		}
		found := rows.Next()
		if found {
			queryErr = rows.Scan(&existing)
		}
		rows.Close()
		if queryErr != nil {
			return queryErr
		}
		if rows.Err() != nil {
			return rows.Err()
		}
		if found {
			if !migrationChecksumMatches(data, existing) {
				return fmt.Errorf("task migration %s checksum changed", entry.Name())
			}
			if existing != checksum {
				if _, err = tx.Exec(ctx, `UPDATE task_report.schema_migrations SET checksum=$2 WHERE version=$1`, entry.Name(), checksum); err != nil {
					return err
				}
			}
			continue
		}
		if _, err = tx.Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("task migration %s: %w", entry.Name(), err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO task_report.schema_migrations(version,checksum) VALUES($1,$2)`, entry.Name(), checksum); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if cutover {
		return (&Store{pool: pool}).Cutover(ctx)
	}
	return nil
}
func (s *Store) CheckSchema(ctx context.Context) error {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT to_regclass('task_report.schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("task schema missing: run task-report-service --migrate-only before startup")
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		var checksum string
		if err := s.pool.QueryRow(ctx, `SELECT checksum FROM task_report.schema_migrations WHERE version=$1`, entry.Name()).Scan(&checksum); err != nil {
			return fmt.Errorf("task schema outdated: run --migrate-only: %w", err)
		}
		data, _ := migrations.ReadFile("migrations/" + entry.Name())
		if !migrationChecksumMatches(data, checksum) {
			return fmt.Errorf("task schema checksum mismatch: %s", entry.Name())
		}
	}
	return nil
}
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Cutover requires that all old API/worker instances have already stopped.
// Only old (generation zero) tasks are normalized; repeating it is safe.
func (s *Store) Cutover(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Old unsent notifications for terminal tasks need no replacement execution.
	if _, err = tx.Exec(ctx, `UPDATE task_report.outbox o SET superseded_at=clock_timestamp(),claimed_by=NULL,claim_token=NULL,claim_until=NULL
	 FROM task_report.tasks t WHERE o.aggregate_id=t.id AND o.topic='task.requested' AND o.dispatch_version IS NULL
	 AND o.published_at IS NULL AND o.superseded_at IS NULL AND t.state IN ('succeeded','cancelled','dead_letter')`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, baseSelect+` WHERE dispatch_version=0 AND state IN ('pending','failed','running') FOR UPDATE`)
	if err != nil {
		return err
	}
	var tasks []Task
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			rows.Close()
			return e
		}
		tasks = append(tasks, t)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, t := range tasks {
		if _, err = tx.Exec(ctx, `UPDATE task_report.outbox SET superseded_at=clock_timestamp(),claimed_by=NULL,claim_token=NULL,claim_until=NULL WHERE aggregate_id=$1 AND topic='task.requested' AND published_at IS NULL AND superseded_at IS NULL`, t.ID); err != nil {
			return err
		}
		executable := t.Kind == "export" || (t.Kind == "import" && boolInput(t.Result, "confirmed"))
		if !executable {
			if t.State == Running {
				if _, err = tx.Exec(ctx, `UPDATE task_report.tasks SET state='failed',last_error='preflight interrupted during AMQP cutover',updated_at=clock_timestamp() WHERE id=$1`, t.ID); err != nil {
					return err
				}
			}
			continue
		}
		t, err = scanTask(tx.QueryRow(ctx, `UPDATE task_report.tasks SET state=CASE WHEN attempt>=max_attempts THEN 'dead_letter' ELSE 'failed' END,
   dispatch_version=1,next_run_at=clock_timestamp(),worker_id=NULL,execution_token=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+columns, t.ID))
		if err != nil {
			return err
		}
		if t.State == DeadLetter {
			err = deadTask(ctx, tx, t, "attempts exhausted during AMQP cutover")
		} else {
			err = enqueueTask(ctx, tx, t)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
