package asset

import (
	"errors"
	"time"
)

var (
	ErrUnknownType = errors.New("unknown asset type")
	ErrValidation  = errors.New("asset validation failed")
	ErrNotFound    = errors.New("asset not found")
	ErrConflict    = errors.New("asset version conflict")
	ErrForbidden   = errors.New("asset access forbidden")
)

type Record struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	OwnerID      string         `json:"owner_id"`
	DepartmentID string         `json:"department_id,omitempty"`
	Version      int64          `json:"version"`
	Fields       map[string]any `json:"fields"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    *time.Time     `json:"deleted_at,omitempty"`
}

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string { return ErrValidation.Error() }

func (e *ValidationError) Unwrap() error { return ErrValidation }
