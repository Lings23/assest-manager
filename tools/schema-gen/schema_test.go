package main

import (
	"strings"
	"testing"
)

func sampleSchema() AssetSchema {
	minimum := float64(0)
	return AssetSchema{
		Type: "example-type", Title: "示例", Version: 1, Compatibility: "backward",
		Migration: "initial", Storage: Storage{Table: "example_assets"},
		Fields: []FieldSpec{
			{Name: "name", Label: "名称", Type: "string", Required: true, MaxLength: 100},
			{Name: "enabled", Label: "启用", Type: "boolean"},
			{Name: "count", Label: "数量", Type: "integer", Minimum: &minimum},
			{Name: "detail", Label: "详情", Type: "string", RequiredWhen: "enabled == true"},
		},
	}
}

func TestValidateSchemaRejectsUnknownOrUnsafeFields(t *testing.T) {
	schema := sampleSchema()
	if err := validateSchema(schema); err != nil {
		t.Fatal(err)
	}
	schema.Fields[3].RequiredWhen = "enabled; DROP TABLE users"
	if err := validateSchema(schema); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unsafe expression accepted: %v", err)
	}
	schema = sampleSchema()
	schema.Fields = append(schema.Fields, FieldSpec{Name: "owner_id", Label: "所有者", Type: "string"})
	if err := validateSchema(schema); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved field accepted: %v", err)
	}
}

func TestCompatibilityRules(t *testing.T) {
	previous := sampleSchema()
	next := sampleSchema()
	next.Version = 2
	next.Fields = append(next.Fields, FieldSpec{
		Name: "optional_note", Label: "备注", Type: "string",
	})
	if err := checkCompatibility(previous, next); err != nil {
		t.Fatalf("compatible addition rejected: %v", err)
	}
	next.Fields[len(next.Fields)-1].Required = true
	if err := checkCompatibility(previous, next); err == nil || !strings.Contains(err.Error(), "needs a default") {
		t.Fatalf("required addition accepted: %v", err)
	}
	next = sampleSchema()
	next.Version = 2
	next.Fields = next.Fields[1:]
	if err := checkCompatibility(previous, next); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("field removal accepted: %v", err)
	}
}

func TestGeneratorsUseStrongColumnsAndStableTypes(t *testing.T) {
	schema := sampleSchema()
	sql, err := generateSQL([]AssetSchema{schema})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sql), "data jsonb") || !strings.Contains(string(sql), `"name" varchar(100) NOT NULL`) ||
		!strings.Contains(string(sql), "snapshot jsonb NOT NULL") {
		t.Fatalf("SQL is not strongly typed:\n%s", sql)
	}
	typescript, err := generateTypeScript([]AssetSchema{schema})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(typescript), "name: string") || !strings.Contains(string(typescript), "count?: number") {
		t.Fatalf("unexpected TypeScript:\n%s", typescript)
	}
}
