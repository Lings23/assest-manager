package asset

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	"asset-platform/internal/assetschema"
)

// Compare the complete records and ordering with the pre-optimization SQL on a
// frozen D1 restore. This is independent of the materialized-page implementation.
func TestPostgresListProjectionParity(t *testing.T) {
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
	if err = s.pool.QueryRow(t.Context(), `SELECT owner_id,department_id FROM asset.responsible_department_assets WHERE deleted_at IS NULL AND department_id IS NOT NULL ORDER BY id LIMIT 1`).Scan(&owner, &dept); err != nil {
		t.Fatal(err)
	}
	scopes := map[string]ListFilter{"global": {Global: true}, "own": {OwnerID: owner}, "department": {DepartmentIDs: []string{dept}}, "descendants": {DepartmentIDs: diagnosticDescendants(t, s.pool, dept)}}
	compare := func(name, assetType string, filter ListFilter, sortField, direction string) {
		t.Helper()
		schema := assetschema.Types[assetType]
		where, args := assetFilterSQL(schema, filter, 0)
		args = append(args, filter.Limit, filter.Offset)
		query := fmt.Sprintf(`SELECT to_jsonb(t) FROM asset.%s AS t WHERE %s ORDER BY t.%s %s,t.id ASC LIMIT $%d OFFSET $%d`, quoteIdentifier(schema.Table), where, quoteIdentifier(sortField), direction, len(args)-1, len(args))
		rows, err := s.pool.Query(t.Context(), query, args...)
		if err != nil {
			t.Fatal(err)
		}
		var want []Record
		for rows.Next() {
			var raw []byte
			if err = rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			record, err := decodeRecord(schema, raw)
			if err != nil {
				t.Fatal(err)
			}
			want = append(want, record)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		got, err := s.List(t.Context(), assetType, filter)
		if err != nil || len(got) != len(want) {
			t.Fatalf("%s type=%s rows=%d want=%d err=%v", name, assetType, len(got), len(want), err)
		}
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Fatalf("%s type=%s row=%d got_id=%s want_id=%s or record fields differ", name, assetType, i, got[i].ID, want[i].ID)
			}
		}
	}
	for assetType, schema := range assetschema.Types {
		for scope, base := range scopes {
			total, err := s.Count(t.Context(), assetType, base)
			if err != nil {
				t.Fatal(err)
			}
			for _, point := range []struct{ limit, offset int }{{20, 0}, {100, 0}, {20, 1000}, {20, 10000}, {20, max(0, int(total)-20)}, {100, max(0, int(total)-100)}} {
				filter := base
				filter.Limit, filter.Offset = point.limit, point.offset
				compare(fmt.Sprintf("%s-l%d-o%d", scope, point.limit, point.offset), assetType, filter, "updated_at", "DESC")
			}
			for _, search := range []string{"P20261015-100000-NOT-EXIST", "RARE1", "HALF-A", "中文", "%", "_"} {
				filter := base
				filter.Limit, filter.Search = 20, search
				compare(scope+"-search-"+search, assetType, filter, "updated_at", "DESC")
			}
			filter := base
			filter.IncludeDeleted, filter.Limit = true, 20
			compare(scope+"-include-deleted", assetType, filter, "updated_at", "DESC")
			// Optional fields exercise NULL ordering when present in the schema.
			var field assetschema.Field
			for _, candidate := range schema.Fields {
				if candidate.Sortable && (field.Name == "" || field.Required && !candidate.Required) {
					field = candidate
				}
			}
			if field.Name != "" {
				for _, descending := range []bool{false, true} {
					filter := base
					filter.Limit, filter.SortField, filter.SortDescending = 20, field.Name, descending
					direction := "ASC"
					if descending {
						direction = "DESC"
					}
					compare(scope+"-sort-"+field.Name+direction, assetType, filter, field.Name, direction)
				}
			}
		}
	}
}
