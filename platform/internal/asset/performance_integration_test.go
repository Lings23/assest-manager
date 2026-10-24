package asset

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"asset-platform/internal/assetschema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type capturedQuery struct {
	SQL      string
	Args     []any
	Started  time.Time
	Duration time.Duration
}
type performanceTracer struct{ last capturedQuery }

func diagnosticDescendants(t *testing.T, pool *pgxpool.Pool, fallback string) []string {
	t.Helper()
	var departments []string
	// Prefer a proper subtree with children, rather than accidentally selecting
	// a leaf whose descendants case would be identical to department scope.
	err := pool.QueryRow(t.Context(), `WITH RECURSIVE parent AS (
	 SELECT p.id FROM iam.departments p WHERE EXISTS(SELECT 1 FROM iam.departments c WHERE c.parent_id=p.id)
	 ORDER BY (p.parent_id IS NULL),p.id LIMIT 1
	), d AS (
	 SELECT id FROM iam.departments WHERE id=COALESCE((SELECT id FROM parent),$1)
	 UNION ALL SELECT c.id FROM iam.departments c JOIN d ON c.parent_id=d.id
	) SELECT array_agg(id::text ORDER BY id) FROM d`, fallback).Scan(&departments)
	if err != nil {
		t.Fatal(err)
	}
	if len(departments) < 2 {
		t.Fatal("D1 diagnostic requires a department subtree with children")
	}
	return departments
}

func (p *performanceTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, q pgx.TraceQueryStartData) context.Context {
	if strings.Contains(q.SQL, "SELECT to_jsonb(t)") || strings.Contains(q.SQL, "AS asset_type, count(*)") {
		p.last = capturedQuery{SQL: q.SQL, Args: append([]any(nil), q.Args...), Started: time.Now()}
	}
	return ctx
}
func (p *performanceTracer) TraceQueryEnd(_ context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if !p.last.Started.IsZero() {
		p.last.Duration = time.Since(p.last.Started)
	}
}

// Opt-in diagnostics use the exact SQL/bound parameters emitted by Store, not a
// separately maintained SQL approximation. Run only on an isolated D1 restore.
func TestPostgresPerformancePlans(t *testing.T) {
	dsn, dest := os.Getenv("ASSET_PERF_DATABASE_URL"), os.Getenv("ASSET_PERF_EVIDENCE_DIR")
	if dsn == "" || dest == "" {
		t.Skip("ASSET_PERF_DATABASE_URL and ASSET_PERF_EVIDENCE_DIR are not configured")
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	trace := &performanceTracer{}
	config.ConnConfig.Tracer = trace
	config.MaxConns = 1 // Diagnostics are intentionally sequential, not a load test.
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := &Store{pool: pool}
	var owner, dept string
	if err = pool.QueryRow(t.Context(), `SELECT owner_id,department_id FROM asset.responsible_department_assets WHERE deleted_at IS NULL AND department_id IS NOT NULL ORDER BY id LIMIT 1`).Scan(&owner, &dept); err != nil {
		t.Fatal(err)
	}
	departments := diagnosticDescendants(t, pool, dept)
	filters := map[string]ListFilter{"global": {Global: true}, "own": {OwnerID: owner}, "department": {DepartmentIDs: []string{dept}}, "descendants": {DepartmentIDs: departments}}
	results := make([]any, 0)
	save := func(name string, captured capturedQuery, population ...int64) {
		var plan json.RawMessage
		if err := pool.QueryRow(t.Context(), "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+captured.SQL, captured.Args...).Scan(&plan); err != nil {
			t.Fatal(err)
		}
		entry := map[string]any{"name": name, "sql": captured.SQL, "arguments": captured.Args, "observed_query_ms": float64(captured.Duration) / float64(time.Millisecond), "plan": plan}
		entry["measurement"] = "Sequential bound-parameter diagnostic. EXPLAIN Execution Time is server time; observed_query_ms is pgx Conn time through Rows.Close, including consumption/decoding and excluding pool acquisition. Not concurrent HTTP latency."
		if len(population) == 2 {
			entry["visible_rows"], entry["matching_rows"] = population[0], population[1]
		}
		data, err := json.MarshalIndent(entry, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dest, name+".json"), data, 0644); err != nil {
			t.Fatal(err)
		}
		results = append(results, entry)
	}
	for _, scope := range []string{"global", "own", "department", "descendants"} {
		filter := filters[scope]
		counts, countErr := s.Counts(t.Context(), filter)
		if countErr != nil {
			t.Fatal(countErr)
		}
		save(scope+"-counts", trace.last)
		if os.Getenv("ASSET_PERF_PHASE") == "p0" {
			for _, count := range counts {
				filter.Internal, filter.Limit, filter.Search = true, 5000, ""
				for offset := 0; ; offset += 5000 {
					filter.Offset = offset
					items, queryErr := s.List(t.Context(), count.AssetType, filter)
					if queryErr != nil {
						t.Fatal(queryErr)
					}
					save(fmt.Sprintf("%s-%s-export-%d", scope, count.AssetType, offset), trace.last)
					if len(items) < 5000 {
						break
					}
				}
			}
			continue
		}
		names := make([]string, 0, len(assetschema.Types))
		for name := range assetschema.Types {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			count, err := s.Count(t.Context(), name, filter)
			if err != nil {
				t.Fatal(err)
			}
			for _, limit := range []int{20, 100} {
				for _, point := range []struct {
					label  string
					offset int
				}{{"first", 0}, {"o1000", 1000}, {"o10000", 10000}, {"last", max(0, int(count)-limit)}} {
					label, offset := point.label, point.offset
					filter.Internal, filter.Limit, filter.Offset, filter.Search = true, limit, offset, ""
					if _, err = s.List(t.Context(), name, filter); err != nil {
						t.Fatal(err)
					}
					save(fmt.Sprintf("%s-%s-l%d-%s", scope, name, limit, label), trace.last)
				}
			}
			// Seed row indices and owner assignment are correlated: RARE1 can
			// match zero (or 20%) and HALF-A zero (or 100%) in an own scope.
			// Measure actual selectivity, with a visible unique marker fallback.
			filter.Limit, filter.Offset, filter.Search = 1, 0, ""
			first, err := s.List(t.Context(), name, filter)
			if err != nil {
				t.Fatal(err)
			}
			marker := ""
			pattern := regexp.MustCompile(`P20261015-100000-T[0-9]+-R[0-9]{7}`)
			if len(first) > 0 {
				for _, field := range assetschema.Types[name].Fields {
					if field.Searchable {
						if value, ok := first[0].Fields[field.Name].(string); ok {
							marker = pattern.FindString(value)
						}
						if marker != "" {
							break
						}
					}
				}
			}
			for _, point := range []struct{ label, search string }{{"none", "P20261015-100000-NOT-EXIST"}, {"rare", "RARE1"}, {"broad", "HALF-A"}} {
				label, search := point.label, point.search
				filter.Limit, filter.Offset, filter.Search = 20, 0, search
				matches, err := s.Count(t.Context(), name, filter)
				if err != nil {
					t.Fatal(err)
				}
				if label == "rare" && (matches == 0 || matches*20 > count) && marker != "" {
					filter.Search = marker
				} else if label == "broad" && matches == 0 && count > 0 {
					filter.Search = "P20261015-100000"
				}
				matches, err = s.Count(t.Context(), name, filter)
				if err != nil {
					t.Fatal(err)
				}
				if count > 0 && (label == "none" && matches != 0 || label == "rare" && matches == 0 || label == "broad" && matches*4 < count) {
					t.Fatalf("invalid selectivity scope=%s type=%s case=%s visible=%d matched=%d", scope, name, label, count, matches)
				}
				if _, err = s.List(t.Context(), name, filter); err != nil {
					t.Fatal(err)
				}
				save(fmt.Sprintf("%s-%s-search-%s", scope, name, label), trace.last, count, matches)
			}
			filter.Search = ""
		}
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dest, "plans.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("Saved %d plans from actual Store queries to %s", len(results), dest)
}
