package asset

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"asset-platform/internal/assetschema"
)

func TestAssetFilterSQLDeniesEmptyScopeAndBindsSearch(t *testing.T) {
	schema := assetschema.Types["responsible-department"]
	cases := []struct {
		filter ListFilter
		want   string
		args   []any
	}{
		{ListFilter{}, "t.deleted_at IS NULL AND FALSE", nil},
		{ListFilter{Global: true}, "t.deleted_at IS NULL", nil},
		{ListFilter{IncludeDeleted: true}, "FALSE", nil},
		{ListFilter{OwnerID: "owner", DepartmentIDs: []string{"ignored"}}, "t.deleted_at IS NULL AND t.owner_id = $4", []any{"owner"}},
		{ListFilter{DepartmentIDs: []string{"dept", "child"}}, "t.deleted_at IS NULL AND t.department_id = ANY($4)", []any{[]string{"dept", "child"}}},
	}
	for _, tc := range cases {
		where, args := assetFilterSQL(schema, tc.filter, 3)
		if where != tc.want || !reflect.DeepEqual(args, tc.args) {
			t.Fatalf("filter=%+v: %s %#v", tc.filter, where, args)
		}
	}
	where, args := assetFilterSQL(schema, ListFilter{OwnerID: "owner", Search: "  ' OR TRUE --  "}, 0)
	if strings.Contains(where, "OR TRUE") || len(args) != 2 || args[1] != "%' OR TRUE --%" || !strings.Contains(where, "ILIKE $2") {
		t.Fatalf("unbound search: %s %#v", where, args)
	}
}

func TestInternalCountsRequiresCredentialsAndRejectsUnknownFilters(t *testing.T) {
	mux := http.NewServeMux()
	NewInternalHTTPHandler(nil, "secret").Register(mux)
	for _, tc := range []struct {
		token, body string
		status      int
	}{
		{"", `{"global":true}`, 403},
		{"wrong", `{"global":true}`, 403},
		{"secret", `{"include_deleted":true}`, 400},
		{"secret", `{"global":true} {}`, 400},
	} {
		r := httptest.NewRequest("POST", "/internal/v1/assets/counts", strings.NewReader(tc.body))
		r.Header.Set("X-Internal-Token", tc.token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("got %d want %d", w.Code, tc.status)
		}
	}
}
