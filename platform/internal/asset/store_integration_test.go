package asset

import (
	"context"
	"os"
	"testing"
)

func TestPostgresLifecycle(t *testing.T) {
	databaseURL := os.Getenv("ASSET_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASSET_TEST_DATABASE_URL is not configured")
	}
	store, err := OpenStore(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fields, err := ValidateCreate("responsible-department", map[string]any{
		"department_name": "集成测试部门",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(
		t.Context(), "responsible-department", "owner-1", "", "owner-1", fields,
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Version != 1 || created.Fields["department_name"] != "集成测试部门" {
		t.Fatalf("unexpected create result: %+v", created)
	}
	updated, err := store.Update(
		t.Context(), "responsible-department", created.ID, 1, "owner-1",
		map[string]any{"department_code": "TEST"},
	)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("unexpected update version: %d", updated.Version)
	}
	deleted, err := store.SoftDelete(
		t.Context(), "responsible-department", created.ID, 2, "owner-1",
	)
	if err != nil || deleted.DeletedAt == nil || deleted.Version != 3 {
		t.Fatalf("soft delete: record=%+v err=%v", deleted, err)
	}
	restored, err := store.Restore(
		t.Context(), "responsible-department", created.ID, 3, "owner-1",
	)
	if err != nil || restored.DeletedAt != nil || restored.Version != 4 {
		t.Fatalf("restore: record=%+v err=%v", restored, err)
	}
	versions, err := store.ListVersions(t.Context(), "responsible-department", created.ID)
	if err != nil || len(versions) != 4 {
		t.Fatalf("versions=%d err=%v", len(versions), err)
	}
}
