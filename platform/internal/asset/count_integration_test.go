package asset

import (
	"os"
	"testing"

	"asset-platform/internal/assetschema"
)

// This read-only comparison is also runnable against a restored D1 snapshot.
// It compares actual exported IDs/counts, rather than duplicating COUNT SQL.
func TestPostgresCountExportParity(t *testing.T) {
	dsn := os.Getenv("ASSET_COUNT_PARITY_DATABASE_URL")
	if dsn == "" {
		t.Skip("ASSET_COUNT_PARITY_DATABASE_URL is not configured")
	}
	s, err := OpenStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var owner, dept string
	if err := s.pool.QueryRow(t.Context(), `SELECT owner_id,coalesce(department_id::text,'') FROM asset.responsible_department_assets WHERE deleted_at IS NULL AND department_id IS NOT NULL LIMIT 1`).Scan(&owner, &dept); err != nil {
		t.Fatal(err)
	}
	filters := map[string]ListFilter{
		"global": {Global: true}, "own": {OwnerID: owner},
		"department":  {DepartmentIDs: []string{dept}},
		"descendants": {DepartmentIDs: diagnosticDescendants(t, s.pool, dept)},
		"empty":       {}, "invisible_owner": {OwnerID: "no-such-owner"},
		"deleted": {Global: true, IncludeDeleted: true}, "search": {Global: true, Search: "HALF-A"},
	}
	for name, filter := range filters {
		t.Run(name, func(t *testing.T) {
			counts, err := s.Counts(t.Context(), filter)
			if err != nil || len(counts) != len(assetschema.Types) {
				t.Fatalf("counts=%v err=%v", counts, err)
			}
			for i, count := range counts {
				if i > 0 && counts[i-1].AssetType >= count.AssetType {
					t.Fatal("counts are not uniquely sorted")
				}
				individual, err := s.Count(t.Context(), count.AssetType, filter)
				if err != nil || individual != count.Total {
					t.Fatalf("%s single=%d batch=%d err=%v", count.AssetType, individual, count.Total, err)
				}
				filter.Internal, filter.Limit, filter.Offset = true, 5000, 0
				total := int64(0)
				for {
					page, err := s.ExportPage(t.Context(), count.AssetType, filter, false)
					if err != nil {
						t.Fatal(err)
					}
					total += int64(len(page.Items))
					if page.Done {
						break
					}
					filter.Offset = page.NextOffset
				}
				if total != count.Total {
					t.Fatalf("%s export=%d count=%d", count.AssetType, total, count.Total)
				}
				t.Logf("%s exported=%d count=%d", count.AssetType, total, count.Total)
			}
		})
	}
}
