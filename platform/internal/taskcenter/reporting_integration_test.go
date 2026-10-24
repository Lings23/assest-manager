package taskcenter

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"asset-platform/internal/asset"
	"asset-platform/internal/assetschema"
	"asset-platform/internal/iam"
)

type reportingAuthorizer struct {
	auth  iam.AuthorizationContext
	calls int
}

func (a *reportingAuthorizer) Authorize(_ context.Context, _, action string) (iam.AuthorizationContext, error) {
	a.calls++
	if action != "asset:read" {
		panic("unexpected reporting permission")
	}
	return a.auth, nil
}

type reportingAssets struct {
	workerAssets
	counts []asset.Count
	query  AssetQuery
	calls  int
}

func (a *reportingAssets) Counts(_ context.Context, q AssetQuery) ([]asset.Count, error) {
	a.calls++
	a.query = q
	return a.counts, nil
}

func TestPostgresReportingAuthorizesOnceAndPreservesSnapshots(t *testing.T) {
	s := executionTestStore(t)
	user := uuid()
	auth := &reportingAuthorizer{auth: iam.AuthorizationContext{Principal: iam.Principal{UserID: user, DepartmentID: "department", DataScope: iam.ScopeOwn}}}
	assets := &reportingAssets{}
	var names []string
	for name := range assetschema.Types {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		assets.counts = append(assets.counts, asset.Count{AssetType: name, Total: int64(i * 13)})
	}
	service := NewService(s, auth, assets, nil)
	counts, err := service.AssetCounts(t.Context(), "Bearer test")
	if err != nil || !reflect.DeepEqual(counts, assets.counts) || auth.calls != 1 || assets.calls != 1 || assets.query.OwnerID != user || assets.query.Global || len(assets.query.DepartmentIDs) > 0 {
		t.Fatalf("counts=%v err=%v authorization_calls=%d asset_calls=%d scope=%+v", counts, err, auth.calls, assets.calls, assets.query)
	}
	rows, err := s.pool.Query(t.Context(), `SELECT asset_type,total FROM task_report.permission_counts WHERE requester_id=$1 ORDER BY asset_type`, user)
	if err != nil {
		t.Fatal(err)
	}
	var snapshots []asset.Count
	for rows.Next() {
		var count asset.Count
		if err = rows.Scan(&count.AssetType, &count.Total); err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, count)
	}
	rows.Close()
	if err = rows.Err(); err != nil || !reflect.DeepEqual(snapshots, counts) {
		t.Fatalf("snapshots=%v err=%v", snapshots, err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DELETE FROM task_report.permission_counts WHERE requester_id=$1`, user)
	})
}
