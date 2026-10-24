package taskcenter

import (
	"strings"
	"testing"
)

func TestStateMachine(t *testing.T) {
	if !CanTransition(Pending, Running) || !CanTransition(Failed, DeadLetter) || CanTransition(Succeeded, Running) {
		t.Fatal("invalid transition matrix")
	}
}

func TestPreflightCSVUsesGeneratedSchema(t *testing.T) {
	csv := "department_name,department_code\n研发部,DEV\n"
	result, err := PreflightCSV("responsible-department", strings.NewReader(csv), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Valid != 1 || len(result.Rows) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestPreflightRejectsUnknownColumn(t *testing.T) {
	_, err := PreflightCSV("responsible-department", strings.NewReader("sql_injection\nx\n"), false)
	if err == nil {
		t.Fatal("expected unknown column error")
	}
}
