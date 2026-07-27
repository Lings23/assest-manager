package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	typeNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)
	fieldNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
	tableNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)
	requiredWhen     = regexp.MustCompile(`^([a-z][a-z0-9_]*) == (true|false)$`)
)

var reservedFields = map[string]struct{}{
	"id": {}, "owner_id": {}, "department_id": {}, "version": {},
	"created_at": {}, "updated_at": {}, "deleted_at": {},
}

type AssetSchema struct {
	Type          string      `yaml:"type" json:"type"`
	Title         string      `yaml:"title" json:"title"`
	Version       int         `yaml:"version" json:"version"`
	Compatibility string      `yaml:"compatibility" json:"compatibility"`
	Migration     string      `yaml:"migration" json:"migration"`
	Storage       Storage     `yaml:"storage" json:"storage"`
	Fields        []FieldSpec `yaml:"fields" json:"fields"`
}

type Storage struct {
	Table string `yaml:"table" json:"table"`
}

type FieldSpec struct {
	Name         string       `yaml:"name" json:"name"`
	Label        string       `yaml:"label" json:"label"`
	Type         string       `yaml:"type" json:"type"`
	Required     bool         `yaml:"required,omitempty" json:"required"`
	RequiredWhen string       `yaml:"requiredWhen,omitempty" json:"requiredWhen,omitempty"`
	MaxLength    int          `yaml:"maxLength,omitempty" json:"maxLength,omitempty"`
	Minimum      *float64     `yaml:"minimum,omitempty" json:"minimum,omitempty"`
	Maximum      *float64     `yaml:"maximum,omitempty" json:"maximum,omitempty"`
	Default      any          `yaml:"default,omitempty" json:"default,omitempty"`
	Enum         []EnumOption `yaml:"enum,omitempty" json:"enum,omitempty"`
	Searchable   bool         `yaml:"searchable,omitempty" json:"searchable"`
	Sortable     bool         `yaml:"sortable,omitempty" json:"sortable"`
	Filterable   bool         `yaml:"filterable,omitempty" json:"filterable"`
	Import       bool         `yaml:"import,omitempty" json:"import"`
	Export       bool         `yaml:"export,omitempty" json:"export"`
	List         bool         `yaml:"list,omitempty" json:"list"`
	Sensitive    bool         `yaml:"sensitive,omitempty" json:"sensitive"`
	Group        string       `yaml:"group,omitempty" json:"group,omitempty"`
	Widget       string       `yaml:"widget,omitempty" json:"widget,omitempty"`
	Help         string       `yaml:"help,omitempty" json:"help,omitempty"`
}

type EnumOption struct {
	Value any    `yaml:"value" json:"value"`
	Label string `yaml:"label" json:"label"`
}

func loadSchemas(directory string) ([]AssetSchema, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var schemas []AssetSchema
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		value, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		decoder := yaml.NewDecoder(bytes.NewReader(value))
		decoder.KnownFields(true)
		var schema AssetSchema
		if err := decoder.Decode(&schema); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := validateSchema(schema); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		schemas = append(schemas, schema)
	}
	sort.Slice(schemas, func(i, j int) bool { return schemas[i].Type < schemas[j].Type })
	if len(schemas) != 7 {
		return nil, fmt.Errorf("expected exactly seven asset schemas, found %d", len(schemas))
	}
	seen := make(map[string]struct{}, len(schemas))
	for _, schema := range schemas {
		if _, duplicate := seen[schema.Type]; duplicate {
			return nil, fmt.Errorf("duplicate asset type %q", schema.Type)
		}
		seen[schema.Type] = struct{}{}
	}
	return schemas, nil
}

func validateSchema(schema AssetSchema) error {
	if !typeNamePattern.MatchString(schema.Type) || strings.TrimSpace(schema.Title) == "" ||
		schema.Version < 1 || !tableNamePattern.MatchString(schema.Storage.Table) ||
		len(schema.Fields) == 0 {
		return fmt.Errorf("type, title, version, storage.table and fields are required")
	}
	if schema.Compatibility != "backward" && schema.Compatibility != "breaking" {
		return fmt.Errorf("compatibility must be backward or breaking")
	}
	fields := make(map[string]FieldSpec, len(schema.Fields))
	for _, field := range schema.Fields {
		if !fieldNamePattern.MatchString(field.Name) || strings.TrimSpace(field.Label) == "" {
			return fmt.Errorf("invalid field name or label %q", field.Name)
		}
		if _, reserved := reservedFields[field.Name]; reserved {
			return fmt.Errorf("field %q is reserved", field.Name)
		}
		if _, duplicate := fields[field.Name]; duplicate {
			return fmt.Errorf("duplicate field %q", field.Name)
		}
		switch field.Type {
		case "string":
			if field.MaxLength < 0 {
				return fmt.Errorf("field %q has invalid maxLength", field.Name)
			}
		case "integer", "number", "boolean", "date":
			if field.MaxLength != 0 {
				return fmt.Errorf("field %q maxLength only applies to string", field.Name)
			}
		default:
			return fmt.Errorf("field %q has unsupported type %q", field.Name, field.Type)
		}
		if field.Minimum != nil && field.Maximum != nil && *field.Minimum > *field.Maximum {
			return fmt.Errorf("field %q minimum exceeds maximum", field.Name)
		}
		if len(field.Enum) > 0 && field.Type != "string" && field.Type != "integer" {
			return fmt.Errorf("field %q enum requires string or integer type", field.Name)
		}
		enumSeen := make(map[string]struct{}, len(field.Enum))
		for _, option := range field.Enum {
			key := fmt.Sprint(option.Value)
			if strings.TrimSpace(option.Label) == "" {
				return fmt.Errorf("field %q has enum without label", field.Name)
			}
			if _, duplicate := enumSeen[key]; duplicate {
				return fmt.Errorf("field %q has duplicate enum value %q", field.Name, key)
			}
			enumSeen[key] = struct{}{}
		}
		fields[field.Name] = field
	}
	for _, field := range schema.Fields {
		if field.RequiredWhen == "" {
			continue
		}
		match := requiredWhen.FindStringSubmatch(field.RequiredWhen)
		if match == nil {
			return fmt.Errorf("field %q has unsafe requiredWhen expression", field.Name)
		}
		dependency, exists := fields[match[1]]
		if !exists || dependency.Type != "boolean" || dependency.Name == field.Name {
			return fmt.Errorf("field %q requiredWhen must reference another boolean field", field.Name)
		}
	}
	return nil
}

func checkCompatibility(previous, next AssetSchema) error {
	if previous.Type != next.Type || previous.Storage.Table != next.Storage.Table {
		return fmt.Errorf("asset type and storage table cannot change")
	}
	if next.Version <= previous.Version {
		return fmt.Errorf("schema version must increase")
	}
	nextFields := make(map[string]FieldSpec, len(next.Fields))
	for _, field := range next.Fields {
		nextFields[field.Name] = field
	}
	for _, oldField := range previous.Fields {
		newField, exists := nextFields[oldField.Name]
		if !exists {
			return fmt.Errorf("field %q was removed", oldField.Name)
		}
		if oldField.Type != newField.Type {
			return fmt.Errorf("field %q type changed", oldField.Name)
		}
		if oldField.Required == false && newField.Required && newField.Default == nil {
			return fmt.Errorf("field %q became required without default", oldField.Name)
		}
		newEnums := make(map[string]struct{}, len(newField.Enum))
		for _, option := range newField.Enum {
			newEnums[fmt.Sprint(option.Value)] = struct{}{}
		}
		for _, option := range oldField.Enum {
			if _, exists := newEnums[fmt.Sprint(option.Value)]; !exists {
				return fmt.Errorf("field %q removed enum value %v", oldField.Name, option.Value)
			}
		}
	}
	for _, field := range next.Fields {
		if _, existed := fieldByName(previous.Fields, field.Name); !existed && field.Required && field.Default == nil {
			return fmt.Errorf("new required field %q needs a default", field.Name)
		}
	}
	return nil
}

func fieldByName(fields []FieldSpec, name string) (FieldSpec, bool) {
	for _, field := range fields {
		if field.Name == name {
			return field, true
		}
	}
	return FieldSpec{}, false
}
