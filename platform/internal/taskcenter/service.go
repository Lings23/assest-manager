package taskcenter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"asset-platform/internal/asset"
	"asset-platform/internal/iam"
)

type Service struct {
	store      *Store
	authorizer asset.Authorizer
	assets     AssetClient
	objects    ObjectStore
}

func NewService(store *Store, authorizer asset.Authorizer, assets AssetClient, objects ObjectStore) *Service {
	return &Service{store: store, authorizer: authorizer, assets: assets, objects: objects}
}
func (s *Service) Create(ctx context.Context, token, kind, assetType, key, department, csvData string) (Task, bool, error) {
	action := "asset:read"
	if kind == "import" {
		action = "asset:create"
	} else if kind != "export" {
		return Task{}, false, ErrInvalid
	}
	auth, err := s.authorizer.Authorize(ctx, token, action)
	if err != nil {
		return Task{}, false, err
	}
	if strings.TrimSpace(department) == "" {
		department = auth.Principal.DepartmentID
	}
	if kind == "import" && !permittedDepartment(department, auth) {
		return Task{}, false, asset.ErrForbidden
	}
	if kind == "import" && csvData == "" {
		return Task{}, false, ErrInvalid
	}
	if existing, lookupErr := s.store.GetByKey(ctx, auth.Principal.UserID, kind, key); lookupErr == nil {
		return existing, false, nil
	} else if !errors.Is(lookupErr, ErrNotFound) {
		return Task{}, false, lookupErr
	}
	query := queryFromAuthorization(auth)
	input := map[string]any{"owner_id": query.OwnerID, "department_ids": query.DepartmentIDs, "global": query.Global, "view_sensitive": query.ViewSensitive}
	sourceKey := ""
	if kind == "import" {
		sourceKey = importSourceKey(auth.Principal.UserID, key, []byte(csvData))
		if err = s.objects.Put(ctx, sourceKey, []byte(csvData), "text/csv; charset=utf-8"); err != nil {
			return Task{}, false, fmt.Errorf("store import source: %w", err)
		}
		input["source_object_key"] = sourceKey
		input["source_size"] = len(csvData)
		input["source_content_type"] = "text/csv; charset=utf-8"
	}
	task, created, err := s.store.Create(ctx, Task{Kind: kind, AssetType: assetType, IdempotencyKey: key, RequestedBy: auth.Principal.UserID, DepartmentID: department, Input: input})
	if err != nil {
		existing, lookupErr := s.store.GetByKey(ctx, auth.Principal.UserID, kind, key)
		if lookupErr == nil {
			if sourceKey != "" && stringInput(existing.Input, "source_object_key") != sourceKey {
				_ = s.objects.Delete(ctx, sourceKey)
			}
			return existing, false, nil
		}
		// A commit error has an unknown outcome. Delete only after PostgreSQL
		// confirms that no task references the staged object.
		if sourceKey != "" && errors.Is(lookupErr, ErrNotFound) {
			_ = s.objects.Delete(ctx, sourceKey)
		}
		return task, created, err
	}
	if !created {
		if sourceKey != "" && stringInput(task.Input, "source_object_key") != sourceKey {
			_ = s.objects.Delete(ctx, sourceKey)
		}
		return task, created, err
	}
	if kind == "import" {
		running, err := s.store.Transition(ctx, task.ID, Pending, Running, 1, nil, "")
		if err != nil {
			return Task{}, true, err
		}
		body, _, objectErr := s.objects.Get(ctx, sourceKey)
		if objectErr != nil {
			_, _ = s.store.FailPermanent(ctx, task.ID, objectErr.Error())
			return running, true, fmt.Errorf("preflight source: %w", objectErr)
		}
		check, err := PreflightCSV(assetType, body, false)
		closeErr := body.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_, _ = s.store.FailPermanent(ctx, task.ID, err.Error())
			return running, true, fmt.Errorf("preflight: %w", err)
		}
		result := map[string]any{"total": check.Total, "valid": check.Valid, "error_count": len(check.Errors), "source_object_key": sourceKey}
		if len(check.Errors) > 0 {
			var report bytes.Buffer
			if err = EncodeErrorReport(&report, check.Errors); err != nil {
				_, _ = s.store.FailPermanent(ctx, task.ID, err.Error())
				return running, true, fmt.Errorf("encode preflight errors: %w", err)
			}
			errorKey := "import-errors/" + task.ID + ".jsonl"
			if err = s.objects.Put(ctx, errorKey, report.Bytes(), "application/x-ndjson; charset=utf-8"); err != nil {
				_, _ = s.store.FailPermanent(ctx, task.ID, err.Error())
				return running, true, fmt.Errorf("store preflight errors: %w", err)
			}
			result["error_object_key"] = errorKey
			result["error_download_url"] = "/api/v1/import-jobs/" + task.ID + "/errors"
		}
		updated, transitionErr := s.store.Transition(ctx, task.ID, Running, AwaitingConfirmation, 100, result, "")
		return updated, true, transitionErr
	}
	return task, true, nil
}

func importSourceKey(userID, idempotencyKey string, data []byte) string {
	keyHash := sha256Hex([]byte(idempotencyKey))
	return "imports/" + userID + "/" + keyHash[:16] + "/" + sha256Hex(data) + ".csv"
}
func (s *Service) List(ctx context.Context, token, kind string) ([]Task, error) {
	auth, err := s.authorizer.Authorize(ctx, token, "asset:read")
	if err != nil {
		return nil, err
	}
	return s.store.List(ctx, auth.Principal.UserID, kind)
}
func (s *Service) Get(ctx context.Context, token, id string) (Task, error) {
	auth, err := s.authorizer.Authorize(ctx, token, "asset:read")
	if err != nil {
		return Task{}, err
	}
	return s.store.Get(ctx, id, auth.Principal.UserID)
}
func (s *Service) Cancel(ctx context.Context, token, id string) (Task, error) {
	auth, err := s.authorizer.Authorize(ctx, token, "asset:read")
	if err != nil {
		return Task{}, err
	}
	task, err := s.store.Get(ctx, id, auth.Principal.UserID)
	if err != nil {
		return Task{}, err
	}
	if task.State == Running || task.State == Succeeded || task.State == Cancelled || task.State == DeadLetter {
		return Task{}, ErrConflict
	}
	if task.Kind == "import" {
		if _, err = s.authorizer.Authorize(ctx, token, "asset:create"); err != nil {
			return Task{}, err
		}
	}
	return s.store.Cancel(ctx, id, auth.Principal.UserID)
}
func (s *Service) Confirm(ctx context.Context, token, id string) (Task, error) {
	auth, err := s.authorizer.Authorize(ctx, token, "asset:create")
	if err != nil {
		return Task{}, err
	}
	t, err := s.store.Get(ctx, id, auth.Principal.UserID)
	if err != nil {
		return Task{}, err
	}
	return s.store.ConfirmAndEnqueue(ctx, t.ID, auth.Principal.UserID)
}

func permittedDepartment(department string, auth iam.AuthorizationContext) bool {
	switch auth.Principal.DataScope {
	case iam.ScopeGlobal:
		return true
	case iam.ScopeOwn:
		return department == auth.Principal.DepartmentID
	default:
		for _, allowed := range auth.DepartmentIDs {
			if department == allowed {
				return true
			}
		}
		return false
	}
}
