package taskcenter

import (
	"errors"
	"time"
)

type State string

const (
	Pending              State = "pending"
	Running              State = "running"
	AwaitingConfirmation State = "awaiting_confirmation"
	Succeeded            State = "succeeded"
	Failed               State = "failed"
	Cancelled            State = "cancelled"
	DeadLetter           State = "dead_letter"
)

var (
	ErrNotFound  = errors.New("task not found")
	ErrConflict  = errors.New("task state conflict")
	ErrInvalid   = errors.New("invalid task")
	ErrLeaseLost = errors.New("task execution lease lost")
)

type Task struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	AssetType      string         `json:"asset_type"`
	State          State          `json:"state"`
	IdempotencyKey string         `json:"idempotency_key"`
	RequestedBy    string         `json:"requested_by"`
	DepartmentID   string         `json:"department_id,omitempty"`
	Progress       int            `json:"progress"`
	Attempt        int            `json:"attempt"`
	MaxAttempts    int            `json:"max_attempts"`
	Input          map[string]any `json:"input,omitempty"`
	Result         map[string]any `json:"result,omitempty"`
	LastError      string         `json:"last_error,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	// Execution metadata is internal and must never enter public task responses.
	DispatchVersion int64      `json:"-"`
	NextRunAt       *time.Time `json:"-"`
	WorkerID        string     `json:"-"`
	ExecutionToken  string     `json:"-"`
	LeaseUntil      *time.Time `json:"-"`
	ObservedAt      time.Time  `json:"-"`
}

type Execution struct {
	Task
	WorkerID        string
	Token           string
	DispatchVersion int64
	LeaseUntil      time.Time
}

func CanTransition(from, to State) bool {
	switch from {
	case Pending:
		return to == Running || to == Cancelled || to == DeadLetter
	case Running:
		return to == AwaitingConfirmation || to == Succeeded || to == Failed || to == DeadLetter
	case AwaitingConfirmation:
		return to == Pending || to == Cancelled
	case Failed:
		return to == Pending || to == Running || to == DeadLetter || to == Cancelled
	default:
		return false
	}
}
