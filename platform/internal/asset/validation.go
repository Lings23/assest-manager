package asset

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"asset-platform/internal/assetschema"
)

func ValidateCreate(assetType string, input map[string]any) (map[string]any, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return nil, ErrUnknownType
	}
	normalized := make(map[string]any, len(input))
	errorsByField := make(map[string]string)
	known := fieldsByName(schema)
	for name := range input {
		if _, exists := known[name]; !exists {
			errorsByField[name] = "unknown field"
		}
	}
	for _, field := range schema.Fields {
		value, exists := input[field.Name]
		if !exists && field.Default != nil {
			value, exists = field.Default, true
		}
		if exists {
			converted, err := normalizeValue(field, value)
			if err != nil {
				errorsByField[field.Name] = err.Error()
			} else {
				normalized[field.Name] = converted
			}
		}
	}
	for _, field := range schema.Fields {
		_, exists := normalized[field.Name]
		required := field.Required || conditionRequired(field.RequiredWhen, normalized)
		if required && !exists {
			errorsByField[field.Name] = "field is required"
		}
	}
	if len(errorsByField) > 0 {
		return nil, &ValidationError{Fields: errorsByField}
	}
	return normalized, nil
}

func ValidateUpdate(assetType string, current, patch map[string]any) (map[string]any, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return nil, ErrUnknownType
	}
	known := fieldsByName(schema)
	normalizedPatch := make(map[string]any, len(patch))
	errorsByField := make(map[string]string)
	for name, value := range patch {
		field, exists := known[name]
		if !exists {
			errorsByField[name] = "unknown field"
			continue
		}
		converted, err := normalizeValue(field, value)
		if err != nil {
			errorsByField[name] = err.Error()
			continue
		}
		normalizedPatch[name] = converted
	}
	if len(errorsByField) > 0 {
		return nil, &ValidationError{Fields: errorsByField}
	}
	merged := make(map[string]any, len(current)+len(normalizedPatch))
	for name, value := range current {
		merged[name] = value
	}
	for name, value := range normalizedPatch {
		merged[name] = value
	}
	if _, err := ValidateCreate(assetType, merged); err != nil {
		return nil, err
	}
	return normalizedPatch, nil
}

func fieldsByName(schema assetschema.Schema) map[string]assetschema.Field {
	result := make(map[string]assetschema.Field, len(schema.Fields))
	for _, field := range schema.Fields {
		result[field.Name] = field
	}
	return result
}

func normalizeValue(field assetschema.Field, value any) (any, error) {
	if value == nil {
		if field.Required {
			return nil, fmt.Errorf("field is required")
		}
		return nil, nil
	}
	var normalized any
	switch field.Type {
	case "string":
		typed, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("must be a string")
		}
		if field.MaxLength > 0 && len([]rune(typed)) > field.MaxLength {
			return nil, fmt.Errorf("must contain at most %d characters", field.MaxLength)
		}
		normalized = typed
	case "date":
		typed, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("must be a YYYY-MM-DD date")
		}
		if _, err := time.Parse("2006-01-02", typed); err != nil {
			return nil, fmt.Errorf("must be a YYYY-MM-DD date")
		}
		normalized = typed
	case "boolean":
		typed, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("must be a boolean")
		}
		normalized = typed
	case "integer":
		number, ok := numericValue(value)
		if !ok || math.Trunc(number) != number || number > math.MaxInt64 || number < math.MinInt64 {
			return nil, fmt.Errorf("must be an integer")
		}
		normalized = int64(number)
	case "number":
		number, ok := numericValue(value)
		if !ok || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, fmt.Errorf("must be a number")
		}
		normalized = number
	default:
		return nil, fmt.Errorf("unsupported field type")
	}
	if field.Minimum != nil {
		if number, ok := numericValue(normalized); ok && number < *field.Minimum {
			return nil, fmt.Errorf("must be at least %s", strconv.FormatFloat(*field.Minimum, 'f', -1, 64))
		}
	}
	if field.Maximum != nil {
		if number, ok := numericValue(normalized); ok && number > *field.Maximum {
			return nil, fmt.Errorf("must be at most %s", strconv.FormatFloat(*field.Maximum, 'f', -1, 64))
		}
	}
	if len(field.Enum) > 0 {
		valid := false
		for _, option := range field.Enum {
			if fmt.Sprint(normalized) == fmt.Sprint(option.Value) {
				valid = true
				break
			}
		}
		if !valid {
			return nil, fmt.Errorf("contains an unsupported enum value")
		}
	}
	return normalized, nil
}

func conditionRequired(expression string, values map[string]any) bool {
	if expression == "" {
		return false
	}
	parts := strings.Split(expression, " == ")
	if len(parts) != 2 {
		return false
	}
	value, ok := values[parts[0]].(bool)
	if !ok {
		return false
	}
	expected, err := strconv.ParseBool(parts[1])
	return err == nil && value == expected
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	default:
		return 0, false
	}
}
