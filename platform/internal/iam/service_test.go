package iam

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func newTestService(t *testing.T) (*Service, *MemoryStore, User) {
	t.Helper()
	key, err := GenerateRSAKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewTokenSigner(key, "iam-test", "asset-platform")
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	service, err := NewService(store, signer)
	if err != nil {
		t.Fatal(err)
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser(context.Background(), CreateUserParams{
		ID: "user-1", Username: "Admin", DisplayName: "管理员",
		PasswordHash: string(passwordHash), Roles: []Role{RoleAdmin},
		DataScope: ScopeGlobal, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, user
}

func TestLoginRefreshRotationAndReuseDetection(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	pair, err := service.Login(ctx, "ADMIN", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("login did not issue both tokens")
	}
	if _, err := service.Authenticate(ctx, pair.AccessToken); err != nil {
		t.Fatalf("issued access token was rejected: %v", err)
	}

	rotated, err := service.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if _, err := service.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrRefreshReuse) {
		t.Fatalf("expected reuse detection, got %v", err)
	}
	if _, err := service.Refresh(ctx, rotated.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("session family was not revoked after reuse: %v", err)
	}
}

func TestDisableOrPermissionChangeInvalidatesTokens(t *testing.T) {
	service, _, user := newTestService(t)
	ctx := context.Background()
	pair, err := service.Login(ctx, user.Username, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateAccess(ctx, UpdateAccessParams{
		UserID: user.ID, Enabled: false, Roles: []Role{RoleReporter}, DataScope: ScopeOwn,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.PermissionsVersion != user.PermissionsVersion+1 {
		t.Fatalf("permissions version not incremented: %d", updated.PermissionsVersion)
	}
	if _, err := service.Authenticate(ctx, pair.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old access token remained valid: %v", err)
	}
	if _, err := service.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("refresh session remained valid: %v", err)
	}
}

func TestInvalidLoginDoesNotRevealUsername(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()
	_, missingErr := service.Login(ctx, "missing", "bad-password")
	_, wrongErr := service.Login(ctx, "admin", "bad-password")
	if !errors.Is(missingErr, ErrInvalidCredentials) || !errors.Is(wrongErr, ErrInvalidCredentials) ||
		missingErr.Error() != wrongErr.Error() {
		t.Fatalf("login errors differ: missing=%v wrong=%v", missingErr, wrongErr)
	}
}

func TestSignerRejectsAlgorithmSubstitution(t *testing.T) {
	service, _, _ := newTestService(t)
	pair, err := service.Login(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(pair.AccessToken, ".")
	parts[0] = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"
	if _, err := service.signer.Verify(strings.Join(parts, ".")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("algorithm substitution was not rejected: %v", err)
	}
}

func TestCreateUserValidatesRoleScopeAndPassword(t *testing.T) {
	service, _, _ := newTestService(t)
	_, err := service.CreateUser(context.Background(), NewUser{
		Username: "reporter", DisplayName: "填报员", Password: "short",
		Roles: []Role{RoleReporter}, DataScope: ScopeOwn,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("short password accepted: %v", err)
	}
	created, err := service.CreateUser(context.Background(), NewUser{
		Username: "reporter", DisplayName: "填报员",
		Password: "another correct password", Roles: []Role{RoleReporter}, DataScope: ScopeOwn,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Username != "reporter" || !created.Enabled || created.PermissionsVersion != 1 {
		t.Fatalf("unexpected created user: %+v", created)
	}
}

func TestDepartmentDataScopes(t *testing.T) {
	service, _, user := newTestService(t)
	ctx := context.Background()
	root, err := service.CreateDepartment(ctx, NewDepartment{Name: "总部"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := service.CreateDepartment(ctx, NewDepartment{Name: "下属部门", ParentID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateDepartment(ctx, NewDepartment{Name: "其他部门"})
	if err != nil {
		t.Fatal(err)
	}
	descendant := Principal{UserID: user.ID, DepartmentID: root.ID, DataScope: ScopeDescendants}
	if allowed, err := service.CanRead(ctx, descendant, "other-user", child.ID); err != nil || !allowed {
		t.Fatalf("descendant scope rejected child: allowed=%v err=%v", allowed, err)
	}
	if allowed, err := service.CanRead(ctx, descendant, "other-user", other.ID); err != nil || allowed {
		t.Fatalf("descendant scope accepted unrelated department: allowed=%v err=%v", allowed, err)
	}
	own := Principal{UserID: user.ID, DataScope: ScopeOwn}
	if allowed, _ := service.CanRead(ctx, own, user.ID, other.ID); !allowed {
		t.Fatal("own scope rejected owner")
	}
	if allowed, _ := service.CanRead(ctx, own, "other-user", root.ID); allowed {
		t.Fatal("own scope accepted another owner")
	}
}

func TestRolePermissionsAndAuthorizationContext(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()
	root, _ := service.CreateDepartment(ctx, NewDepartment{Name: "总部"})
	child, _ := service.CreateDepartment(ctx, NewDepartment{Name: "子部门", ParentID: root.ID})
	principal := Principal{
		UserID: "reporter", Roles: []Role{RoleReporter},
		DataScope: ScopeDescendants, DepartmentID: root.ID,
	}
	authorization, err := service.Authorize(ctx, principal, "asset:create")
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(authorization.DepartmentIDs, root.ID) || !containsString(authorization.DepartmentIDs, child.ID) {
		t.Fatalf("authorization context lacks department subtree: %+v", authorization)
	}
	if _, err := service.Authorize(ctx, principal, "asset:delete"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reporter delete was allowed: %v", err)
	}
	auditor := Principal{Roles: []Role{RoleAuditor}, DataScope: ScopeGlobal}
	if _, err := service.Authorize(ctx, auditor, "asset:create"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("auditor create was allowed: %v", err)
	}
}

func TestPasswordChangeRevokesSessionsAndClearsBootstrapFlag(t *testing.T) {
	service, _, user := newTestService(t)
	ctx := context.Background()
	user.MustChangePassword = true
	// Update through the store to model a bootstrap administrator.
	service.store.(*MemoryStore).users[user.ID] = user
	pair, err := service.Login(ctx, user.Username, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authorize(ctx, pair.Principal, "asset:read"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bootstrap password flag did not block business access: %v", err)
	}
	if err := service.ChangePassword(
		ctx, pair.Principal, "correct horse battery staple", "new secure administrator password",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("password change did not revoke refresh session: %v", err)
	}
	relogin, err := service.Login(ctx, user.Username, "new secure administrator password")
	if err != nil {
		t.Fatal(err)
	}
	if relogin.Principal.MustChangePassword {
		t.Fatal("must-change-password flag was not cleared")
	}
}
