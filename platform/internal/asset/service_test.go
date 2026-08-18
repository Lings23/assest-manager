package asset

import (
	"context"
	"errors"
	"testing"
	"time"

	"asset-platform/internal/iam"
)

type fakeAuthorizer struct {
	context iam.AuthorizationContext
	err     error
	actions []string
}

func (a *fakeAuthorizer) Authorize(_ context.Context, _ string, action string) (iam.AuthorizationContext, error) {
	a.actions = append(a.actions, action)
	return a.context, a.err
}

type fakeRepository struct {
	records       map[string]Record
	lastFilter    ListFilter
	createdFields map[string]any
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{records: make(map[string]Record)}
}

func (r *fakeRepository) List(_ context.Context, _ string, filter ListFilter) ([]Record, error) {
	r.lastFilter = filter
	var result []Record
	for _, record := range r.records {
		result = append(result, record)
	}
	return result, nil
}

func (r *fakeRepository) Get(_ context.Context, _, id string, _ bool) (Record, error) {
	record, ok := r.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return record, nil
}

func (r *fakeRepository) Create(
	_ context.Context, assetType, ownerID, departmentID, _ string, fields map[string]any,
) (Record, error) {
	r.createdFields = fields
	record := Record{
		ID: "asset-1", Type: assetType, OwnerID: ownerID, DepartmentID: departmentID,
		Version: 1, Fields: fields, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	r.records[record.ID] = record
	return record, nil
}

func (r *fakeRepository) Update(
	_ context.Context, assetType, id string, version int64, _ string, fields map[string]any,
) (Record, error) {
	record, ok := r.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	if record.Version != version {
		return Record{}, ErrConflict
	}
	for name, value := range fields {
		record.Fields[name] = value
	}
	record.Version++
	record.Type = assetType
	r.records[id] = record
	return record, nil
}

func (r *fakeRepository) SoftDelete(_ context.Context, _, id string, version int64, _ string) (Record, error) {
	record, ok := r.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	if record.Version != version {
		return Record{}, ErrConflict
	}
	now := time.Now()
	record.DeletedAt = &now
	record.Version++
	r.records[id] = record
	return record, nil
}

func (r *fakeRepository) Restore(_ context.Context, _, id string, version int64, _ string) (Record, error) {
	record, ok := r.records[id]
	if !ok || record.Version != version {
		return Record{}, ErrConflict
	}
	record.DeletedAt = nil
	record.Version++
	r.records[id] = record
	return record, nil
}

func (r *fakeRepository) ListVersions(context.Context, string, string) ([]Version, error) {
	return []Version{{Version: 1, Action: "create"}}, nil
}

func (r *fakeRepository) Close() {}

func TestCreateUsesIdentityAndSchemaValidation(t *testing.T) {
	repository := newFakeRepository()
	authorizer := &fakeAuthorizer{context: iam.AuthorizationContext{Principal: iam.Principal{
		UserID: "reporter-1", DepartmentID: "dept-1",
		Roles: []iam.Role{iam.RoleReporter}, DataScope: iam.ScopeOwn,
	}}}
	service := NewService(repository, authorizer)
	record, err := service.Create(t.Context(), "Bearer token", "responsible-department", "", map[string]any{
		"department_name": "信息中心",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.OwnerID != "reporter-1" || record.DepartmentID != "dept-1" ||
		repository.createdFields["department_name"] != "信息中心" {
		t.Fatalf("identity was not applied server-side: %+v", record)
	}
	if _, err := service.Create(t.Context(), "Bearer token", "responsible-department", "", map[string]any{
		"department_name": "信息中心", "owner_id": "attacker",
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("system field accepted: %v", err)
	}
}

func TestDataScopeIsAppliedToListAndDetail(t *testing.T) {
	repository := newFakeRepository()
	repository.records["asset-1"] = Record{
		ID: "asset-1", Type: "responsible-department", OwnerID: "other",
		DepartmentID: "dept-child", Version: 1,
		Fields: map[string]any{"department_name": "下属单位"},
	}
	authorizer := &fakeAuthorizer{context: iam.AuthorizationContext{
		Principal: iam.Principal{
			UserID: "reviewer", DepartmentID: "dept-root",
			Roles: []iam.Role{iam.RoleReviewer}, DataScope: iam.ScopeDescendants,
		},
		DepartmentIDs: []string{"dept-root", "dept-child"},
	}}
	service := NewService(repository, authorizer)
	if _, err := service.List(t.Context(), "Bearer token", "responsible-department", ListFilter{}); err != nil {
		t.Fatal(err)
	}
	if len(repository.lastFilter.DepartmentIDs) != 2 || repository.lastFilter.Global {
		t.Fatalf("department scope not applied: %+v", repository.lastFilter)
	}
	if _, err := service.Get(t.Context(), "Bearer token", "responsible-department", "asset-1", false); err != nil {
		t.Fatalf("authorized descendant was hidden: %v", err)
	}
	authorizer.context.DepartmentIDs = []string{"dept-root"}
	if _, err := service.Get(t.Context(), "Bearer token", "responsible-department", "asset-1", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("out-of-scope asset exposed: %v", err)
	}
}

func TestSensitiveFieldsAreMaskedForNonAuditors(t *testing.T) {
	repository := newFakeRepository()
	repository.records["asset-1"] = Record{
		ID: "asset-1", Type: "responsible-department", OwnerID: "reporter",
		Version: 1, Fields: map[string]any{
			"department_name": "信息中心", "head_phone": "13800000000",
		},
	}
	authorizer := &fakeAuthorizer{context: iam.AuthorizationContext{Principal: iam.Principal{
		UserID: "reporter", Roles: []iam.Role{iam.RoleReporter}, DataScope: iam.ScopeOwn,
	}}}
	service := NewService(repository, authorizer)
	record, err := service.Get(t.Context(), "token", "responsible-department", "asset-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if record.Fields["head_phone"] != "***" {
		t.Fatalf("sensitive value was exposed: %+v", record.Fields)
	}
	authorizer.context.Principal.Roles = []iam.Role{iam.RoleAuditor}
	authorizer.context.Principal.DataScope = iam.ScopeGlobal
	record, err = service.Get(t.Context(), "token", "responsible-department", "asset-1", false)
	if err != nil || record.Fields["head_phone"] != "13800000000" {
		t.Fatalf("auditor could not view complete value: record=%+v err=%v", record, err)
	}
}

func TestOptimisticLockIsPreserved(t *testing.T) {
	repository := newFakeRepository()
	repository.records["asset-1"] = Record{
		ID: "asset-1", Type: "responsible-department", OwnerID: "admin",
		Version: 3, Fields: map[string]any{"department_name": "信息中心"},
	}
	authorizer := &fakeAuthorizer{context: iam.AuthorizationContext{Principal: iam.Principal{
		UserID: "admin", Roles: []iam.Role{iam.RoleAdmin}, DataScope: iam.ScopeGlobal,
	}}}
	service := NewService(repository, authorizer)
	if _, err := service.Update(t.Context(), "token", "responsible-department", "asset-1", 2, map[string]any{
		"department_name": "新名称",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update did not conflict: %v", err)
	}
}
