package taskcenter

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestPublicTaskContractDoesNotExposeExecutionMetadata(t *testing.T) {
	now := time.Now().UTC()
	task := Task{ID: uuid(), Kind: "import", AssetType: "responsible-department", State: Running,
		IdempotencyKey: "request", RequestedBy: uuid(), DepartmentID: uuid(), Progress: 42, Attempt: 2, MaxAttempts: 3,
		Input: map[string]any{"source_object_key": "imports/source.csv"}, Result: map[string]any{"committed": 12}, LastError: "previous failure", CreatedAt: now, UpdatedAt: now,
		DispatchVersion: 2, WorkerID: "private-worker", ExecutionToken: uuid(), LeaseUntil: &now, NextRunAt: &now, ObservedAt: now}
	expected, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(publicTask(task))
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("public contract changed: %s versus %s", actual, expected)
	}
	publicType := reflect.TypeOf(TaskDTO{})
	for _, field := range []string{"DispatchVersion", "WorkerID", "ExecutionToken", "LeaseUntil", "NextRunAt", "ObservedAt"} {
		if _, exists := publicType.FieldByName(field); exists {
			t.Errorf("internal field in public DTO: %s", field)
		}
	}
	if publicTasks(nil) != nil {
		t.Fatal("nil list contract changed")
	}
	if got := publicTasks([]Task{task}); len(got) != 1 || got[0].ID != task.ID {
		t.Fatalf("public list=%+v", got)
	}
}
