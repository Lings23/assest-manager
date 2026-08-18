package asset

import (
	"encoding/json"
	"errors"
	"testing"

	"asset-platform/internal/assetschema"
)

func TestEverySchemaRejectsUnknownFields(t *testing.T) {
	if len(assetschema.Types) != 7 {
		t.Fatalf("expected seven generated schemas, got %d", len(assetschema.Types))
	}
	for assetType := range assetschema.Types {
		t.Run(assetType, func(t *testing.T) {
			_, err := ValidateCreate(assetType, map[string]any{"id": "attacker-controlled"})
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("unknown system field accepted: %v", err)
			}
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Fields["id"] != "unknown field" {
				t.Fatalf("unexpected validation result: %#v", err)
			}
		})
	}
}

func TestResponsibilityDepartmentValidation(t *testing.T) {
	fields, err := ValidateCreate("responsible-department", map[string]any{
		"department_name": "信息中心",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fields["department_name"] != "信息中心" {
		t.Fatalf("unexpected fields: %+v", fields)
	}
	_, err = ValidateCreate("responsible-department", map[string]any{})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("required field omission accepted: %v", err)
	}
}

func TestConditionalAndNumericValidation(t *testing.T) {
	_, err := ValidateCreate("system-info", map[string]any{
		"has_external_interface": true,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("conditional required field omission accepted: %v", err)
	}
	_, err = normalizeValue(assetschema.Field{
		Name: "port", Type: "integer", Minimum: floatPointer(0), Maximum: floatPointer(65535),
	}, json.Number("65536"))
	if err == nil {
		t.Fatal("out-of-range port accepted")
	}
}

func TestUpdateValidatesMergedState(t *testing.T) {
	current := map[string]any{
		"department_name": "信息中心",
		"department_code": "IT",
	}
	patch, err := ValidateUpdate("responsible-department", current, map[string]any{
		"department_head": "张三",
	})
	if err != nil || patch["department_head"] != "张三" {
		t.Fatalf("valid patch rejected: patch=%+v err=%v", patch, err)
	}
	if _, err := ValidateUpdate("responsible-department", current, map[string]any{
		"created_at": "2026-01-01",
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("system field patch accepted: %v", err)
	}
}

func floatPointer(value float64) *float64 { return &value }
