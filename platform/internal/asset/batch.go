package asset

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"asset-platform/internal/assetschema"
	"github.com/jackc/pgx/v5"
)

type ImportRow struct {
	Key    string         `json:"key"`
	Fields map[string]any `json:"fields"`
}

const MaxImportBatchRows = 500
const InternalDecodeLimitBytes int64 = 8 << 20

// ImportBatchRequest is shared with the worker's exact wire-size calculation.
// Keep its encoding identical to the internal HTTP client's request.
type ImportBatchRequest struct {
	TaskID       string      `json:"task_id"`
	OwnerID      string      `json:"owner_id"`
	DepartmentID string      `json:"department_id"`
	Rows         []ImportRow `json:"rows"`
}

type BatchLimits struct {
	MaxRows      int   `json:"max_rows"`
	MaxBodyBytes int64 `json:"max_body_bytes"`
}

type BatchResult struct {
	Created int `json:"created"`
	Skipped int `json:"skipped"`
}

// CreateBatch commits an entire import chunk atomically. The idempotency key is
// reserved in the same transaction as the asset and version row, so a retry
// cannot create a duplicate or leave a key without its asset.
func (s *Store) CreateBatch(ctx context.Context, taskID, assetType, ownerID, departmentID string, rows []ImportRow) (BatchResult, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return BatchResult{}, ErrUnknownType
	}
	if taskID == "" || ownerID == "" || len(rows) == 0 || len(rows) > MaxImportBatchRows {
		return BatchResult{}, ErrValidation
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BatchResult{}, err
	}
	defer tx.Rollback(ctx)
	result := BatchResult{}
	for _, row := range rows {
		fields, validateErr := ValidateCreate(assetType, row.Fields)
		if validateErr != nil || strings.TrimSpace(row.Key) == "" {
			if validateErr != nil {
				return BatchResult{}, validateErr
			}
			return BatchResult{}, ErrValidation
		}
		id, idErr := newUUID()
		if idErr != nil {
			return BatchResult{}, idErr
		}
		tag, reserveErr := tx.Exec(ctx, `INSERT INTO asset.import_idempotency(task_id,row_key,asset_type,asset_id) VALUES($1,$2,$3,$4) ON CONFLICT(task_id,row_key) DO NOTHING`, taskID, row.Key, assetType, id)
		if reserveErr != nil {
			return BatchResult{}, reserveErr
		}
		if tag.RowsAffected() == 0 {
			result.Skipped++
			continue
		}
		names := []string{"id", "owner_id", "department_id"}
		values := []any{id, ownerID, emptyToNil(departmentID)}
		keys := make([]string, 0, len(fields))
		for name := range fields {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			names = append(names, quoteIdentifier(name))
			values = append(values, fields[name])
		}
		marks := make([]string, len(values))
		for i := range marks {
			marks[i] = fmt.Sprintf("$%d", i+1)
		}
		query := fmt.Sprintf("INSERT INTO asset.%s (%s) VALUES (%s)", quoteIdentifier(schema.Table), strings.Join(names, ","), strings.Join(marks, ","))
		if _, err = tx.Exec(ctx, query, values...); err != nil {
			return BatchResult{}, err
		}
		record, getErr := getRecord(ctx, tx, schema, id, true, false)
		if getErr != nil {
			return BatchResult{}, getErr
		}
		if err = writeVersion(ctx, tx, record, "import", ownerID); err != nil {
			return BatchResult{}, err
		}
		result.Created++
	}
	if err = tx.Commit(ctx); err != nil {
		return BatchResult{}, err
	}
	return result, nil
}

type ExportPage struct {
	Items      []Record `json:"items"`
	NextOffset int      `json:"next_offset"`
	Done       bool     `json:"done"`
}

func (s *Store) ExportPage(ctx context.Context, assetType string, filter ListFilter, viewSensitive bool) (ExportPage, error) {
	items, err := s.List(ctx, assetType, filter)
	if err != nil {
		return ExportPage{}, err
	}
	if !viewSensitive {
		schema := assetschema.Types[assetType]
		for i := range items {
			for _, field := range schema.Fields {
				if field.Sensitive {
					delete(items[i].Fields, field.Name)
				}
			}
		}
	}
	return ExportPage{Items: items, NextOffset: filter.Offset + len(items), Done: len(items) < filter.Limit}, nil
}

func marshalFields(fields map[string]any) json.RawMessage {
	value, _ := json.Marshal(fields)
	return value
}
