package asset

import "context"

type legacyRepository interface {
	CreateLegacy(context.Context, string, string, string, string, bool, map[string]any) (Record, bool, error)
}

func (s *Service) MigrateLegacy(ctx context.Context, authorization, assetType, legacyID, departmentID string, isDeleted bool, input map[string]any) (Record, bool, error) {
	auth, err := s.authorizer.Authorize(ctx, authorization, "asset:create")
	if err != nil {
		return Record{}, false, err
	}
	if departmentID == "" {
		departmentID = auth.Principal.DepartmentID
	}
	if !canCreateInDepartment(departmentID, auth) {
		return Record{}, false, ErrForbidden
	}
	fields, err := ValidateCreate(assetType, input)
	if err != nil {
		return Record{}, false, err
	}
	repository, ok := s.store.(legacyRepository)
	if !ok {
		return Record{}, false, ErrForbidden
	}
	record, created, err := repository.CreateLegacy(ctx, assetType, legacyID, auth.Principal.UserID, departmentID, isDeleted, fields)
	if err != nil {
		return Record{}, false, err
	}
	maskSensitive(&record, auth.Principal)
	return record, created, nil
}
