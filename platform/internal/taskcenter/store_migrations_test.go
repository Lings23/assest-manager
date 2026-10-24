package taskcenter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
)

func TestMigrationChecksumsIgnoreCheckoutLineEndingsButRejectContentChanges(t *testing.T) {
	lf := []byte("CREATE TABLE tasks (id uuid);\n-- migration\n")
	crlf := bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
	if migrationChecksum(lf) != migrationChecksum(crlf) {
		t.Fatal("Git line endings changed semantic migration checksum")
	}
	for _, raw := range [][]byte{lf, crlf} {
		if !migrationChecksumMatches(lf, fmt.Sprintf("%x", sha256.Sum256(raw))) || !migrationChecksumMatches(crlf, fmt.Sprintf("%x", sha256.Sum256(raw))) {
			t.Fatal("legacy raw checksum rejected")
		}
	}
	if migrationChecksumMatches(append(lf, []byte("DROP TABLE tasks;\n")...), migrationChecksum(lf)) {
		t.Fatal("changed SQL accepted")
	}
}

func TestPostgresMigrationLineEndingCompatibilityAndChecksumEnforcement(t *testing.T) {
	s := executionTestStore(t)
	url := os.Getenv("TASKCENTER_TEST_DATABASE_URL")
	if url == "" {
		url = os.Getenv("TASK_TEST_DATABASE_URL")
	}
	data, err := migrations.ReadFile("migrations/002_amqp_execution.sql")
	if err != nil {
		t.Fatal(err)
	}
	canonical := migrationChecksum(data)
	legacyCRLF := fmt.Sprintf("%x", sha256.Sum256(bytes.ReplaceAll(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))))
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `UPDATE task_report.schema_migrations SET checksum=$1 WHERE version='002_amqp_execution.sql'`, canonical)
	})
	if _, err = s.pool.Exec(t.Context(), `UPDATE task_report.schema_migrations SET checksum=$1 WHERE version='002_amqp_execution.sql'`, legacyCRLF); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckSchema(t.Context()); err != nil {
		t.Fatalf("runtime rejected legacy line-ending checksum: %v", err)
	}
	if err = Migrate(t.Context(), url, false); err != nil {
		t.Fatalf("repeat migration after checkout change: %v", err)
	}
	var stored string
	if err = s.pool.QueryRow(t.Context(), `SELECT checksum FROM task_report.schema_migrations WHERE version='002_amqp_execution.sql'`).Scan(&stored); err != nil || stored != canonical {
		t.Fatalf("canonical registration=%s %v", stored, err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE task_report.schema_migrations SET checksum='changed-content' WHERE version='002_amqp_execution.sql'`); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckSchema(t.Context()); err == nil {
		t.Fatal("runtime accepted changed migration")
	}
	if err = Migrate(t.Context(), url, false); err == nil {
		t.Fatal("migrator accepted changed migration")
	}
}
