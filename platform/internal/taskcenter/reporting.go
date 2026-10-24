package taskcenter

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"asset-platform/internal/asset"
	"asset-platform/internal/iam"
)

type Count = asset.Count

func (s *Service) AssetCounts(ctx context.Context, token string) ([]Count, error) {
	auth, err := s.authorizer.Authorize(ctx, token, "asset:read")
	if err != nil {
		return nil, err
	}
	query := queryFromAuthorization(auth)
	out, err := s.assets.Counts(ctx, query)
	if err != nil {
		return nil, err
	}
	departments := append([]string(nil), auth.DepartmentIDs...)
	sort.Strings(departments)
	scopeKey := strings.Join([]string{string(auth.Principal.DataScope), auth.Principal.DepartmentID, strings.Join(departments, ","), strconv.FormatBool(query.ViewSensitive)}, ":")
	for _, count := range out {
		if err = s.store.UpsertCount(ctx, count.AssetType, auth.Principal.UserID, scopeKey, count.Total); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Service) Download(ctx context.Context, token, id string) (io.ReadCloser, string, error) {
	auth, err := s.authorizer.Authorize(ctx, token, "asset:read")
	if err != nil {
		return nil, "", err
	}
	task, err := s.store.Get(ctx, id, auth.Principal.UserID)
	if err != nil {
		return nil, "", err
	}
	if task.Kind != "export" || task.State != Succeeded {
		return nil, "", ErrConflict
	}
	key, ok := task.Result["object_key"].(string)
	if !ok || key == "" {
		return nil, "", fmt.Errorf("export object missing")
	}
	return s.objects.Get(ctx, key)
}
func (s *Service) DownloadImportErrors(ctx context.Context, token, id string) (io.ReadCloser, string, error) {
	auth, err := s.authorizer.Authorize(ctx, token, "asset:read")
	if err != nil {
		return nil, "", err
	}
	task, err := s.store.Get(ctx, id, auth.Principal.UserID)
	if err != nil {
		return nil, "", err
	}
	if task.Kind != "import" {
		return nil, "", ErrConflict
	}
	key, ok := task.Result["error_object_key"].(string)
	if !ok || key == "" {
		return nil, "", ErrNotFound
	}
	return s.objects.Get(ctx, key)
}
func queryFromAuthorization(auth iam.AuthorizationContext) AssetQuery {
	query := AssetQuery{ViewSensitive: auth.Principal.HasRole(iam.RoleAdmin) || auth.Principal.HasRole(iam.RoleAuditor)}
	switch auth.Principal.DataScope {
	case iam.ScopeGlobal:
		query.Global = true
	case iam.ScopeOwn:
		query.OwnerID = auth.Principal.UserID
	default:
		query.DepartmentIDs = append([]string(nil), auth.DepartmentIDs...)
	}
	return query
}
