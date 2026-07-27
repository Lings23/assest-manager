package iam

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	accessTTL  = 15 * time.Minute
	refreshTTL = 7 * 24 * time.Hour
)

type Service struct {
	store     Store
	signer    *TokenSigner
	now       func() time.Time
	dummyHash []byte
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
	AccessExpiry time.Time
	Principal    Principal
}

type NewUser struct {
	Username           string
	DisplayName        string
	Password           string
	DepartmentID       string
	Roles              []Role
	DataScope          DataScope
	MustChangePassword bool
}

type NewDepartment struct {
	Name     string
	ParentID string
}

type AuthorizationContext struct {
	Principal     Principal `json:"principal"`
	DepartmentIDs []string  `json:"department_ids"`
}

func NewService(store Store, signer *TokenSigner) (*Service, error) {
	if store == nil || signer == nil {
		return nil, fmt.Errorf("store and signer are required")
	}
	dummyHash, err := bcrypt.GenerateFromPassword([]byte("not-a-real-password-value"), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return &Service{store: store, signer: signer, now: time.Now, dummyHash: dummyHash}, nil
}

func (s *Service) Login(ctx context.Context, username, password string) (TokenPair, error) {
	user, err := s.store.GetUserByUsername(ctx, normalizeUsername(username))
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return TokenPair{}, ErrInvalidCredentials
	}
	if !user.Enabled || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return TokenPair{}, ErrInvalidCredentials
	}
	return s.newSession(ctx, user, "")
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return TokenPair{}, ErrUnauthorized
	}
	now := s.now().UTC()
	newToken, err := randomOpaqueToken(32)
	if err != nil {
		return TokenPair{}, err
	}
	newSession := Session{
		TokenHash: hashToken(newToken), ExpiresAt: now.Add(refreshTTL), CreatedAt: now,
	}
	oldSession, err := s.store.RotateSession(ctx, hashToken(refreshToken), newSession, now)
	if err != nil {
		return TokenPair{}, err
	}
	user, err := s.store.GetUser(ctx, oldSession.UserID)
	if err != nil || !user.Enabled {
		_ = s.store.RevokeUserSessions(ctx, oldSession.UserID, now)
		return TokenPair{}, ErrUnauthorized
	}
	access, expiry, err := s.signer.Sign(PrincipalFromUser(user), accessTTL)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken: access, RefreshToken: newToken, AccessExpiry: expiry,
		Principal: PrincipalFromUser(user),
	}, nil
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if strings.TrimSpace(refreshToken) == "" {
		return nil
	}
	return s.store.RevokeSession(ctx, hashToken(refreshToken), s.now().UTC())
}

func (s *Service) Authenticate(ctx context.Context, accessToken string) (Principal, error) {
	principal, err := s.signer.Verify(accessToken)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	user, err := s.store.GetUser(ctx, principal.UserID)
	if err != nil || !user.Enabled || user.PermissionsVersion != principal.PermissionsVersion {
		return Principal{}, ErrUnauthorized
	}
	return PrincipalFromUser(user), nil
}

func (s *Service) CreateUser(ctx context.Context, input NewUser) (User, error) {
	input.Username = normalizeUsername(input.Username)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if len(input.Username) < 3 || len(input.Username) > 50 ||
		len(input.DisplayName) < 1 || len(input.DisplayName) > 100 ||
		len(input.Password) < 12 || len(input.Password) > 256 ||
		validateAccess(input.Roles, input.DataScope) != nil {
		return User{}, ErrInvalidInput
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	id, err := randomUUID()
	if err != nil {
		return User{}, err
	}
	return s.store.CreateUser(ctx, CreateUserParams{
		ID: id, Username: input.Username, DisplayName: input.DisplayName,
		PasswordHash: string(passwordHash), DepartmentID: input.DepartmentID,
		Roles: input.Roles, DataScope: input.DataScope, Enabled: true,
		MustChangePassword: input.MustChangePassword,
	})
}

func (s *Service) UpdateAccess(ctx context.Context, input UpdateAccessParams) (User, error) {
	if strings.TrimSpace(input.UserID) == "" || validateAccess(input.Roles, input.DataScope) != nil {
		return User{}, ErrInvalidInput
	}
	input.UpdatedAt = s.now().UTC()
	return s.store.UpdateUserAccess(ctx, input)
}

func (s *Service) ChangePassword(
	ctx context.Context, principal Principal, currentPassword, newPassword string,
) error {
	if len(newPassword) < 12 || len(newPassword) > 256 || currentPassword == newPassword {
		return ErrInvalidInput
	}
	user, err := s.store.GetUser(ctx, principal.UserID)
	if err != nil || !user.Enabled {
		return ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)) != nil {
		return ErrInvalidCredentials
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.store.UpdatePassword(ctx, user.ID, string(passwordHash), false, s.now().UTC())
	return err
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	return s.store.ListUsers(ctx)
}

func (s *Service) CreateDepartment(ctx context.Context, input NewDepartment) (Department, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.ParentID = strings.TrimSpace(input.ParentID)
	if input.Name == "" || len(input.Name) > 100 {
		return Department{}, ErrInvalidInput
	}
	id, err := randomUUID()
	if err != nil {
		return Department{}, err
	}
	return s.store.CreateDepartment(ctx, CreateDepartmentParams{
		ID: id, Name: input.Name, ParentID: input.ParentID,
	})
}

func (s *Service) ListDepartments(ctx context.Context) ([]Department, error) {
	return s.store.ListDepartments(ctx)
}

func (s *Service) CanRead(
	ctx context.Context, principal Principal, ownerID, departmentID string,
) (bool, error) {
	switch principal.DataScope {
	case ScopeGlobal:
		return true, nil
	case ScopeOwn:
		return principal.UserID == ownerID, nil
	case ScopeDepartment:
		return principal.DepartmentID != "" && principal.DepartmentID == departmentID, nil
	case ScopeDescendants:
		return s.store.IsDepartmentDescendant(ctx, principal.DepartmentID, departmentID)
	default:
		return false, ErrForbidden
	}
}

func (s *Service) Authorize(
	ctx context.Context, principal Principal, action string,
) (AuthorizationContext, error) {
	if principal.MustChangePassword {
		return AuthorizationContext{}, ErrForbidden
	}
	if !roleAllows(principal.Roles, action) {
		return AuthorizationContext{}, ErrForbidden
	}
	result := AuthorizationContext{Principal: principal}
	switch principal.DataScope {
	case ScopeDepartment:
		if principal.DepartmentID != "" {
			result.DepartmentIDs = []string{principal.DepartmentID}
		}
	case ScopeDescendants:
		ids, err := s.store.ListDepartmentDescendantIDs(ctx, principal.DepartmentID)
		if err != nil {
			return AuthorizationContext{}, err
		}
		result.DepartmentIDs = ids
	}
	return result, nil
}

func roleAllows(roles []Role, action string) bool {
	for _, role := range roles {
		if role == RoleAdmin {
			return true
		}
		switch action {
		case "asset:read":
			if role == RoleReporter || role == RoleReviewer || role == RoleAuditor {
				return true
			}
		case "asset:create", "asset:update":
			if role == RoleReporter {
				return true
			}
		case "asset:approve":
			if role == RoleReviewer {
				return true
			}
		case "audit:read":
			if role == RoleAuditor {
				return true
			}
		}
	}
	return false
}

func (s *Service) newSession(ctx context.Context, user User, familyID string) (TokenPair, error) {
	now := s.now().UTC()
	refreshToken, err := randomOpaqueToken(32)
	if err != nil {
		return TokenPair{}, err
	}
	if familyID == "" {
		familyID, err = randomOpaqueToken(16)
		if err != nil {
			return TokenPair{}, err
		}
	}
	if err := s.store.CreateSession(ctx, Session{
		TokenHash: hashToken(refreshToken), FamilyID: familyID, UserID: user.ID,
		ExpiresAt: now.Add(refreshTTL), CreatedAt: now,
	}); err != nil {
		return TokenPair{}, err
	}
	principal := PrincipalFromUser(user)
	accessToken, expiry, err := s.signer.Sign(principal, accessTTL)
	if err != nil {
		_ = s.store.RevokeSession(ctx, hashToken(refreshToken), now)
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken: accessToken, RefreshToken: refreshToken,
		AccessExpiry: expiry, Principal: principal,
	}, nil
}
