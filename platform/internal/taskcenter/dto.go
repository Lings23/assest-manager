package taskcenter

import "time"

// TaskDTO is the public task contract. It deliberately does not embed Task,
// so adding internal execution metadata cannot accidentally expand an API.
type TaskDTO struct {
	Phase          string         `json:"phase"`
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
}

func publicTask(task Task) TaskDTO {
	return TaskDTO{Phase: taskPhase(task), ID: task.ID, Kind: task.Kind, AssetType: task.AssetType, State: task.State, IdempotencyKey: task.IdempotencyKey,
		RequestedBy: task.RequestedBy, DepartmentID: task.DepartmentID, Progress: task.Progress, Attempt: task.Attempt, MaxAttempts: task.MaxAttempts,
		Input: task.Input, Result: task.Result, LastError: task.LastError, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt}
}
func publicTasks(tasks []Task) []TaskDTO {
	if tasks == nil {
		return nil
	}
	result := make([]TaskDTO, len(tasks))
	for i, task := range tasks {
		result[i] = publicTask(task)
	}
	return result
}
