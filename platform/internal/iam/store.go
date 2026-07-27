package iam

import (
	"context"
	"time"
)

type CreateUserParams struct {
	ID                 string
	Username           string
	DisplayName        string
	PasswordHash       string
	DepartmentID       string
	Roles              []Role
	DataScope          DataScope
	Enabled            bool
	MustChangePassword bool
}

type UpdateAccessParams struct {
	UserID       string
	Enabled      bool
	Roles        []Role
	DataScope    DataScope
	DepartmentID string
	UpdatedAt    time.Time
}

type CreateDepartmentParams struct {
	ID       string
	Name     string
	ParentID string
}

type Store interface {
	GetUserByUsername(context.Context, string) (User, error)
	GetUser(context.Context, string) (User, error)
	ListUsers(context.Context) ([]User, error)
	CreateUser(context.Context, CreateUserParams) (User, error)
	UpdateUserAccess(context.Context, UpdateAccessParams) (User, error)
	UpdatePassword(context.Context, string, string, bool, time.Time) (User, error)
	ListDepartments(context.Context) ([]Department, error)
	CreateDepartment(context.Context, CreateDepartmentParams) (Department, error)
	IsDepartmentDescendant(context.Context, string, string) (bool, error)
	ListDepartmentDescendantIDs(context.Context, string) ([]string, error)
	CreateSession(context.Context, Session) error
	RotateSession(context.Context, string, Session, time.Time) (Session, error)
	RevokeSession(context.Context, string, time.Time) error
	RevokeUserSessions(context.Context, string, time.Time) error
	Close()
}
