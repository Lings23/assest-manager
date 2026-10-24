package iam

import (
	"net/url"
	"os"
	"testing"
)

func TestPostgresRuntimeStartsWithReadOnlyDatabaseSession(t *testing.T) {
	dsn := os.Getenv("IAM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("IAM_TEST_DATABASE_URL is required")
	}
	if err := Migrate(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("default_transaction_read_only", "on")
	u.RawQuery = query.Encode()
	store, err := OpenPostgresStore(t.Context(), u.String())
	if err != nil {
		t.Fatal("runtime performed writes or DDL", err)
	}
	defer store.Close()
}
