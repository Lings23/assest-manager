package asset

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestCreateBatchFastValidatesWholeChunkBeforeOpeningTransaction(t *testing.T) {
	store := &Store{}
	valid := map[string]any{"department_name": "Import test"}
	for _, rows := range [][]ImportRow{
		{{Key: "duplicate", Fields: valid}, {Key: "duplicate", Fields: valid}},
		{{Key: "valid", Fields: valid}, {Key: "invalid", Fields: map[string]any{}}},
	} {
		_, err := store.CreateBatchFast(t.Context(), "task", "responsible-department", "owner", "", rows)
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("expected validation failure before using the nil database pool, got %v", err)
		}
	}
}

func TestPostgresBatchFastConcurrentOverlappingReplay(t *testing.T) {
	store := openIdempotencyTestStore(t)
	taskID, ownerID := idempotencyTestUUID(t), idempotencyTestUUID(t)
	cleanupIdempotencyTestRows(t, store, taskID, "", ownerID, "responsible_department_assets")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	makeRows := func(keys ...string) []ImportRow {
		rows := make([]ImportRow, len(keys))
		for i, key := range keys {
			rows[i] = ImportRow{Key: key, Fields: map[string]any{"department_name": "Import " + key}}
		}
		return rows
	}
	chunks := [][]ImportRow{makeRows("c", "a", "b"), makeRows("d", "c", "b")}
	const workers = 8
	type outcome struct {
		result BatchResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, workers)
	for i := range workers {
		go func(rows []ImportRow) {
			<-start
			result, err := store.CreateBatchFast(ctx, taskID, "responsible-department", ownerID, "", rows)
			results <- outcome{result, err}
		}(chunks[i%len(chunks)])
	}
	close(start)
	var created, skipped int
	for range workers {
		out := <-results
		if out.err != nil {
			t.Errorf("concurrent chunk: %v", out.err)
		}
		created += out.result.Created
		skipped += out.result.Skipped
	}
	if t.Failed() {
		t.FailNow()
	}
	if created != 4 || skipped != workers*3-4 {
		t.Fatalf("union of four keys: created=%d skipped=%d", created, skipped)
	}
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.import_idempotency WHERE task_id=$1`, taskID, 4)
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.responsible_department_assets WHERE owner_id=$1`, ownerID, 4)
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.asset_versions WHERE changed_by=$1`, ownerID, 4)
	// Each committed reservation must point to its asset and exactly one initial snapshot.
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.import_idempotency i JOIN asset.responsible_department_assets a ON a.id=i.asset_id::text JOIN asset.asset_versions v ON v.asset_type=i.asset_type AND v.asset_id=a.id AND v.version=1 WHERE i.task_id=$1 AND v.action='import'`, taskID, 4)
	replay, err := store.CreateBatchFast(ctx, taskID, "responsible-department", ownerID, "", makeRows("d", "c", "b", "a"))
	if err != nil || replay != (BatchResult{Skipped: 4}) {
		t.Fatalf("committed replay: result=%+v err=%v", replay, err)
	}
}

func TestPostgresBatchFastRollbackReleasesAllReservations(t *testing.T) {
	store := openIdempotencyTestStore(t)
	taskID, ownerID := idempotencyTestUUID(t), idempotencyTestUUID(t)
	cleanupIdempotencyTestRows(t, store, taskID, "", ownerID, "software_statistics_assets")
	rows := []ImportRow{
		{Key: "a", Fields: map[string]any{"department_name": "Valid", "report_year": 2026, "pur_os_dom_amt": 100}},
		// The schema accepts a finite positive number, but PostgreSQL numeric(18,2)
		// rejects it after the idempotency reservations have been inserted.
		{Key: "b", Fields: map[string]any{"department_name": "Overflow", "report_year": 2026, "pur_os_dom_amt": 1e30}},
	}
	_, err := store.CreateBatchFast(t.Context(), taskID, "software-statistics", ownerID, "", rows)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "22003" {
		t.Fatalf("expected database numeric overflow, got %v", err)
	}
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.import_idempotency WHERE task_id=$1`, taskID, 0)
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.software_statistics_assets WHERE owner_id=$1`, ownerID, 0)
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.asset_versions WHERE changed_by=$1`, ownerID, 0)
	rows[1].Fields["pur_os_dom_amt"] = 200
	result, err := store.CreateBatchFast(t.Context(), taskID, "software-statistics", ownerID, "", rows)
	if err != nil || result != (BatchResult{Created: 2}) {
		t.Fatalf("retry after rollback: result=%+v err=%v", result, err)
	}
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.import_idempotency WHERE task_id=$1`, taskID, 2)
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.asset_versions WHERE changed_by=$1`, ownerID, 2)
}

func TestPostgresLegacyConcurrentReplay(t *testing.T) {
	store := openIdempotencyTestStore(t)
	legacyID, ownerID := idempotencyTestUUID(t), idempotencyTestUUID(t)
	cleanupIdempotencyTestRows(t, store, "", legacyID, ownerID, "responsible_department_assets")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	const workers = 8
	type outcome struct {
		record  Record
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan outcome, workers)
	for range workers {
		go func() {
			<-start
			record, created, err := store.CreateLegacy(ctx, "responsible-department", legacyID, ownerID, "", true, map[string]any{"department_name": "Legacy test"})
			results <- outcome{record, created, err}
		}()
	}
	close(start)
	var assetID string
	var createdCount int
	for range workers {
		out := <-results
		if out.err != nil {
			t.Errorf("concurrent migration: %v", out.err)
			continue
		}
		if assetID == "" {
			assetID = out.record.ID
		}
		if out.record.ID != assetID || out.record.DeletedAt == nil || out.record.Version != 1 {
			t.Errorf("replay did not preserve the winning deleted asset: %+v", out.record)
		}
		if out.created {
			createdCount++
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	if createdCount != 1 {
		t.Fatalf("expected one winning migration, got %d", createdCount)
	}
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.legacy_migrations WHERE legacy_type='responsible-department' AND legacy_id=$1`, legacyID, 1)
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.responsible_department_assets WHERE owner_id=$1`, ownerID, 1)
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.asset_versions WHERE changed_by=$1 AND action='migration_deleted'`, ownerID, 1)
	record, created, err := store.CreateLegacy(ctx, "responsible-department", legacyID, ownerID, "", false, map[string]any{"department_name": "Changed replay"})
	if err != nil || created || record.ID != assetID || record.DeletedAt == nil || record.Fields["department_name"] != "Legacy test" {
		t.Fatalf("committed mapping replay: record=%+v created=%v err=%v", record, created, err)
	}
}

func TestPostgresLegacyRollbackReleasesReservation(t *testing.T) {
	store := openIdempotencyTestStore(t)
	legacyID, ownerID := idempotencyTestUUID(t), idempotencyTestUUID(t)
	cleanupIdempotencyTestRows(t, store, "", legacyID, ownerID, "responsible_department_assets")
	_, _, err := store.CreateLegacy(t.Context(), "responsible-department", legacyID, ownerID, "", false, map[string]any{"department_name": strings.Repeat("x", 101)})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "22001" {
		t.Fatalf("expected database varchar length failure, got %v", err)
	}
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.legacy_migrations WHERE legacy_type='responsible-department' AND legacy_id=$1`, legacyID, 0)
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.responsible_department_assets WHERE owner_id=$1`, ownerID, 0)
	assertIdempotencyTestCount(t, store, `SELECT count(*) FROM asset.asset_versions WHERE changed_by=$1`, ownerID, 0)
	record, created, err := store.CreateLegacy(t.Context(), "responsible-department", legacyID, ownerID, "", false, map[string]any{"department_name": "Recovered migration"})
	if err != nil || !created || record.Version != 1 {
		t.Fatalf("retry after rollback: record=%+v created=%v err=%v", record, created, err)
	}
}

func openIdempotencyTestStore(t *testing.T) *Store {
	t.Helper()
	databaseURL := os.Getenv("ASSET_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASSET_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	store, err := OpenStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store
}

func idempotencyTestUUID(t *testing.T) string {
	t.Helper()
	id, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func assertIdempotencyTestCount(t *testing.T, store *Store, query string, value any, want int) {
	t.Helper()
	var got int
	if err := store.pool.QueryRow(t.Context(), query, value).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s: got %d, want %d", query, got, want)
	}
}

func cleanupIdempotencyTestRows(t *testing.T, store *Store, taskID, legacyID, ownerID, table string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		type cleanupQuery struct {
			sql string
			arg string
		}
		queries := []cleanupQuery{
			{`DELETE FROM asset.asset_versions WHERE changed_by=$1`, ownerID},
			{`DELETE FROM asset.` + quoteIdentifier(table) + ` WHERE owner_id=$1`, ownerID},
		}
		if taskID != "" {
			queries = append(queries, cleanupQuery{`DELETE FROM asset.import_idempotency WHERE task_id=$1`, taskID})
		}
		if legacyID != "" {
			queries = append(queries, cleanupQuery{`DELETE FROM asset.legacy_migrations WHERE legacy_type='responsible-department' AND legacy_id=$1`, legacyID})
		}
		for _, query := range queries {
			if _, err := store.pool.Exec(ctx, query.sql, query.arg); err != nil {
				t.Errorf("clean up idempotency test: %v", err)
			}
		}
	})
}
