package asset

import (
	"context"
	"errors"
	"strings"

	"asset-platform/internal/assetschema"
	"asset-platform/internal/iam"
)

type Repository interface {
	List(context.Context, string, ListFilter) ([]Record, error)
	Get(context.Context, string, string, bool) (Record, error)
	Create(context.Context, string, string, string, string, map[string]any) (Record, error)
	Update(context.Context, string, string, int64, string, map[string]any) (Record, error)
	SoftDelete(context.Context, string, string, int64, string) (Record, error)
	Restore(context.Context, string, string, int64, string) (Record, error)
	ListVersions(context.Context, string, string) ([]Version, error)
	Close()
}

type Service struct {
	store      Repository
	authorizer Authorizer
}

func NewService(store Repository, authorizer Authorizer) *Service {
	return &Service{store: store, authorizer: authorizer}
}

func (s *Service) Schemas() []assetschema.Schema {
	result := make([]assetschema.Schema, 0, len(assetschema.Types))
	for _, schema := range assetschema.Types {
		result = append(result, schema)
	}
	sortSchemas(result)
	return result
}

func (s *Service) Schema(assetType string) (assetschema.Schema, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return assetschema.Schema{}, ErrUnknownType
	}
	return schema, nil
}

func (s *Service) List(
	ctx context.Context, authorization, assetType string, filter ListFilter,
) ([]Record, error) {
	auth, err := s.authorizer.Authorize(ctx, authorization, "asset:read")
	if err != nil {
		return nil, err
	}
	applyDataScope(&filter, auth)
	records, err := s.store.List(ctx, assetType, filter)
	if err != nil {
		return nil, err
	}
	for index := range records {
		maskSensitive(&records[index], auth.Principal)
	}
	return records, nil
}

func (s *Service) Get(
	ctx context.Context, authorization, assetType, id string, includeDeleted bool,
) (Record, error) {
	auth, err := s.authorizer.Authorize(ctx, authorization, "asset:read")
	if err != nil {
		return Record{}, err
	}
	record, err := s.store.Get(ctx, assetType, id, includeDeleted)
	if err != nil {
		return Record{}, err
	}
	if !canAccess(record, auth) {
		return Record{}, ErrNotFound
	}
	maskSensitive(&record, auth.Principal)
	return record, nil
}

func (s *Service) Create(
	ctx context.Context, authorization, assetType, departmentID string, input map[string]any,
) (Record, error) {
	auth, err := s.authorizer.Authorize(ctx, authorization, "asset:create")
	if err != nil {
		return Record{}, err
	}
	departmentID = strings.TrimSpace(departmentID)
	if departmentID == "" {
		departmentID = auth.Principal.DepartmentID
	}
	if !canCreateInDepartment(departmentID, auth) {
		return Record{}, ErrForbidden
	}
	fields, err := ValidateCreate(assetType, input)
	if err != nil {
		return Record{}, err
	}
	record, err := s.store.Create(ctx, assetType, auth.Principal.UserID, departmentID, auth.Principal.UserID, fields)
	if err != nil {
		return Record{}, err
	}
	maskSensitive(&record, auth.Principal)
	return record, nil
}

func (s *Service) Update(
	ctx context.Context, authorization, assetType, id string, version int64, patch map[string]any,
) (Record, error) {
	auth, err := s.authorizer.Authorize(ctx, authorization, "asset:update")
	if err != nil {
		return Record{}, err
	}
	current, err := s.store.Get(ctx, assetType, id, false)
	if err != nil {
		return Record{}, err
	}
	if !canAccess(current, auth) {
		return Record{}, ErrNotFound
	}
	fields, err := ValidateUpdate(assetType, current.Fields, patch)
	if err != nil {
		return Record{}, err
	}
	record, err := s.store.Update(ctx, assetType, id, version, auth.Principal.UserID, fields)
	if err != nil {
		return Record{}, err
	}
	maskSensitive(&record, auth.Principal)
	return record, nil
}

func (s *Service) Delete(
	ctx context.Context, authorization, assetType, id string, version int64,
) error {
	auth, err := s.authorizer.Authorize(ctx, authorization, "asset:delete")
	if err != nil {
		return err
	}
	_, err = s.store.SoftDelete(ctx, assetType, id, version, auth.Principal.UserID)
	return err
}

func (s *Service) Restore(
	ctx context.Context, authorization, assetType, id string, version int64,
) (Record, error) {
	auth, err := s.authorizer.Authorize(ctx, authorization, "asset:restore")
	if err != nil {
		return Record{}, err
	}
	record, err := s.store.Restore(ctx, assetType, id, version, auth.Principal.UserID)
	if err != nil {
		return Record{}, err
	}
	maskSensitive(&record, auth.Principal)
	return record, nil
}

func (s *Service) Versions(
	ctx context.Context, authorization, assetType, id string,
) ([]Version, error) {
	auth, err := s.authorizer.Authorize(ctx, authorization, "asset:read")
	if err != nil {
		return nil, err
	}
	record, err := s.store.Get(ctx, assetType, id, true)
	if err != nil {
		return nil, err
	}
	if !canAccess(record, auth) {
		return nil, ErrNotFound
	}
	return s.store.ListVersions(ctx, assetType, id)
}

func applyDataScope(filter *ListFilter, authorization iam.AuthorizationContext) {
	switch authorization.Principal.DataScope {
	case iam.ScopeGlobal:
		filter.Global = true
	case iam.ScopeOwn:
		filter.OwnerID = authorization.Principal.UserID
	default:
		filter.DepartmentIDs = append([]string(nil), authorization.DepartmentIDs...)
	}
	if filter.IncludeDeleted && !authorization.Principal.HasRole(iam.RoleAdmin) {
		filter.IncludeDeleted = false
	}
}

func canAccess(record Record, authorization iam.AuthorizationContext) bool {
	switch authorization.Principal.DataScope {
	case iam.ScopeGlobal:
		return true
	case iam.ScopeOwn:
		return record.OwnerID == authorization.Principal.UserID
	case iam.ScopeDepartment, iam.ScopeDescendants:
		return contains(authorization.DepartmentIDs, record.DepartmentID)
	default:
		return false
	}
}

func canCreateInDepartment(departmentID string, authorization iam.AuthorizationContext) bool {
	switch authorization.Principal.DataScope {
	case iam.ScopeGlobal:
		return true
	case iam.ScopeOwn:
		return departmentID == authorization.Principal.DepartmentID
	case iam.ScopeDepartment, iam.ScopeDescendants:
		return contains(authorization.DepartmentIDs, departmentID)
	default:
		return false
	}
}

func maskSensitive(record *Record, principal iam.Principal) {
	if principal.HasRole(iam.RoleAdmin) || principal.HasRole(iam.RoleAuditor) {
		return
	}
	schema, ok := assetschema.Types[record.Type]
	if !ok {
		return
	}
	fields := make(map[string]any, len(record.Fields))
	for name, value := range record.Fields {
		fields[name] = value
	}
	record.Fields = fields
	for _, field := range schema.Fields {
		if field.Sensitive {
			if _, exists := record.Fields[field.Name]; exists {
				record.Fields[field.Name] = "***"
			}
		}
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func sortSchemas(schemas []assetschema.Schema) {
	for i := 0; i < len(schemas); i++ {
		for j := i + 1; j < len(schemas); j++ {
			if schemas[j].Type < schemas[i].Type {
				schemas[i], schemas[j] = schemas[j], schemas[i]
			}
		}
	}
}

func IsUnauthorized(err error) bool {
	return errors.Is(err, iam.ErrUnauthorized)
}
