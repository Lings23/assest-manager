package taskcenter

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"asset-platform/internal/asset"
	"asset-platform/internal/assetschema"
)

type RowError struct {
	Row     int    `json:"row"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}
type Preflight struct {
	AssetType string           `json:"asset_type"`
	Total     int              `json:"total"`
	Valid     int              `json:"valid"`
	Errors    []RowError       `json:"errors,omitempty"`
	Rows      []map[string]any `json:"-"`
}

// PreflightCSV streams records from r and uses the generated Schema as the only
// column contract. It keeps normalized rows only when requested by callers.
func PreflightCSV(assetType string, r io.Reader, keepRows bool) (Preflight, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return Preflight{}, PermanentFailure(asset.ErrUnknownType)
	}
	allowed := map[string]assetschema.Field{}
	for _, f := range schema.Fields {
		if f.Import {
			allowed[f.Name] = f
		}
	}
	reader := csv.NewReader(r)
	reader.ReuseRecord = false
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		var parseError *csv.ParseError
		if errors.Is(err, io.EOF) || errors.As(err, &parseError) {
			err = PermanentFailure(err)
		}
		return Preflight{}, fmt.Errorf("read header: %w", err)
	}
	seen := map[string]bool{}
	for _, name := range header {
		name = strings.TrimSpace(name)
		if _, ok := allowed[name]; !ok || seen[name] {
			return Preflight{}, PermanentFailure(fmt.Errorf("unsupported or duplicate column %q", name))
		}
		seen[name] = true
	}
	result := Preflight{AssetType: assetType}
	for rowNumber := 2; ; rowNumber++ {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			var parseError *csv.ParseError
			if !errors.As(readErr, &parseError) {
				// Transport errors are not immutable CSV errors. Stop reading
				// instead of repeatedly consuming the same failing source.
				return Preflight{}, fmt.Errorf("read row %d: %w", rowNumber, readErr)
			}
			result.Errors = append(result.Errors, RowError{Row: rowNumber, Message: readErr.Error()})
			continue
		}
		result.Total++
		if len(record) != len(header) {
			result.Errors = append(result.Errors, RowError{Row: rowNumber, Message: "column count does not match header"})
			continue
		}
		values := make(map[string]any, len(header))
		for i, raw := range record {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			field := allowed[strings.TrimSpace(header[i])]
			value, convErr := csvValue(field, raw)
			if convErr != nil {
				result.Errors = append(result.Errors, RowError{Row: rowNumber, Field: field.Name, Message: convErr.Error()})
				continue
			}
			values[field.Name] = value
		}
		normalized, validateErr := asset.ValidateCreate(assetType, values)
		if validateErr != nil {
			if fields, ok := validateErr.(*asset.ValidationError); ok {
				for name, message := range fields.Fields {
					result.Errors = append(result.Errors, RowError{Row: rowNumber, Field: name, Message: message})
				}
			} else {
				result.Errors = append(result.Errors, RowError{Row: rowNumber, Message: validateErr.Error()})
			}
			continue
		}
		result.Valid++
		if keepRows {
			result.Rows = append(result.Rows, normalized)
		}
	}
	return result, nil
}

func csvValue(field assetschema.Field, raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	switch field.Type {
	case "boolean":
		v, err := strconv.ParseBool(strings.ToLower(raw))
		if err != nil {
			return nil, fmt.Errorf("must be true or false")
		}
		return v, nil
	case "integer":
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("must be an integer")
		}
		return v, nil
	case "number":
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("must be a number")
		}
		return v, nil
	default:
		return raw, nil
	}
}

func EncodeErrorReport(w io.Writer, errors []RowError) error {
	enc := json.NewEncoder(w)
	for _, item := range errors {
		if err := enc.Encode(item); err != nil {
			return err
		}
	}
	return nil
}
