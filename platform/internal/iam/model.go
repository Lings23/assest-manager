package iam

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrRefreshReuse       = errors.New("refresh token reuse detected")
	ErrNotFound           = errors.New("not found")
	ErrConflict           = errors.New("conflict")
	ErrInvalidInput       = errors.New("invalid input")
)

type Role string

const (
	RoleReporter Role = "reporter"
	RoleReviewer Role = "reviewer"
	RoleAuditor  Role = "auditor"
	RoleAdmin    Role = "admin"
)

type DataScope string

const (
	ScopeOwn         DataScope = "own"
	ScopeDepartment  DataScope = "department"
	ScopeDescendants DataScope = "descendants"
	ScopeGlobal      DataScope = "global"
)

type User struct {
	ID                 string    `json:"id"`
	Username           string    `json:"username"`
	DisplayName        string    `json:"display_name"`
	PasswordHash       string    `json:"-"`
	DepartmentID       string    `json:"department_id,omitempty"`
	Roles              []Role    `json:"roles"`
	DataScope          DataScope `json:"data_scope"`
	Enabled            bool      `json:"enabled"`
	MustChangePassword bool      `json:"must_change_password"`
	PermissionsVersion int64     `json:"permissions_version"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type Department struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id,omitempty"`
}

type Session struct {
	TokenHash string
	FamilyID  string
	UserID    string
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

type Principal struct {
	UserID             string    `json:"user_id"`
	Username           string    `json:"username"`
	DisplayName        string    `json:"display_name"`
	DepartmentID       string    `json:"department_id,omitempty"`
	Roles              []Role    `json:"roles"`
	DataScope          DataScope `json:"data_scope"`
	PermissionsVersion int64     `json:"permissions_version"`
	MustChangePassword bool      `json:"must_change_password"`
}

func (p Principal) HasRole(role Role) bool {
	for _, candidate := range p.Roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func PrincipalFromUser(user User) Principal {
	return Principal{
		UserID:             user.ID,
		Username:           user.Username,
		DisplayName:        user.DisplayName,
		DepartmentID:       user.DepartmentID,
		Roles:              append([]Role(nil), user.Roles...),
		DataScope:          user.DataScope,
		PermissionsVersion: user.PermissionsVersion,
		MustChangePassword: user.MustChangePassword,
	}
}

func normalizeUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validRole(role Role) bool {
	switch role {
	case RoleReporter, RoleReviewer, RoleAuditor, RoleAdmin:
		return true
	default:
		return false
	}
}

func validScope(scope DataScope) bool {
	switch scope {
	case ScopeOwn, ScopeDepartment, ScopeDescendants, ScopeGlobal:
		return true
	default:
		return false
	}
}

func validateAccess(roles []Role, scope DataScope) error {
	if len(roles) == 0 || !validScope(scope) {
		return ErrInvalidInput
	}
	seen := make(map[Role]struct{}, len(roles))
	for _, role := range roles {
		if !validRole(role) {
			return ErrInvalidInput
		}
		if _, exists := seen[role]; exists {
			return ErrInvalidInput
		}
		seen[role] = struct{}{}
	}
	return nil
}
