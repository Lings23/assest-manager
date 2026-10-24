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

type normalizedImportRow struct {
	key, id string
	fields  map[string]any
}

// CreateBatchFast uses one transaction and set-based inserts for a chunk. An
// atomic reservation of the persisted row keys makes concurrent replay safe;
// reservations, assets and version snapshots are committed together.
func (s *Store) CreateBatchFast(ctx context.Context, taskID, assetType, ownerID, departmentID string, rows []ImportRow) (BatchResult, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return BatchResult{}, ErrUnknownType
	}
	if taskID == "" || ownerID == "" || len(rows) == 0 || len(rows) > MaxImportBatchRows {
		return BatchResult{}, ErrValidation
	}
	normalized := make([]normalizedImportRow, 0, len(rows))
	keys := make(map[string]bool, len(rows))
	for _, row := range rows {
		fields, err := ValidateCreate(assetType, row.Fields)
		if err != nil {
			return BatchResult{}, err
		}
		if strings.TrimSpace(row.Key) == "" || keys[row.Key] {
			return BatchResult{}, ErrValidation
		}
		keys[row.Key] = true
		id, err := newUUID()
		if err != nil {
			return BatchResult{}, err
		}
		normalized = append(normalized, normalizedImportRow{row.Key, id, fields})
	}
	// All competing chunks reserve overlapping keys in the same order.
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].key < normalized[j].key })
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BatchResult{}, err
	}
	defer tx.Rollback(ctx)
	idemArgs := make([]any, 0, len(normalized)*4)
	idemGroups := make([]string, 0, len(normalized))
	for _, row := range normalized {
		marks := make([]string, 4)
		for i, value := range []any{taskID, row.key, assetType, row.id} {
			idemArgs = append(idemArgs, value)
			marks[i] = fmt.Sprintf("$%d", len(idemArgs))
		}
		idemGroups = append(idemGroups, "("+strings.Join(marks, ",")+")")
	}
	found, err := tx.Query(ctx, `INSERT INTO asset.import_idempotency(task_id,row_key,asset_type,asset_id) VALUES `+strings.Join(idemGroups, ",")+` ON CONFLICT(task_id,row_key) DO NOTHING RETURNING row_key,asset_id`, idemArgs...)
	if err != nil {
		return BatchResult{}, err
	}
	reserved := make(map[string]string, len(normalized))
	for found.Next() {
		var key, id string
		if err = found.Scan(&key, &id); err != nil {
			found.Close()
			return BatchResult{}, err
		}
		reserved[key] = id
	}
	found.Close()
	if err = found.Err(); err != nil {
		return BatchResult{}, err
	}
	fresh := make([]normalizedImportRow, 0, len(reserved))
	for _, row := range normalized {
		if id, ok := reserved[row.key]; ok {
			row.id = id
			fresh = append(fresh, row)
		}
	}
	result := BatchResult{Skipped: len(rows) - len(fresh)}
	if len(fresh) == 0 {
		if err = tx.Commit(ctx); err != nil {
			return BatchResult{}, err
		}
		return result, nil
	}
	fieldSet := map[string]bool{}
	for _, row := range fresh {
		for name := range row.fields {
			fieldSet[name] = true
		}
	}
	fieldNames := make([]string, 0, len(fieldSet))
	for name := range fieldSet {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)
	columns := []string{"id", "owner_id", "department_id"}
	for _, name := range fieldNames {
		columns = append(columns, quoteIdentifier(name))
	}
	args := make([]any, 0, len(fresh)*len(columns))
	groups := make([]string, 0, len(fresh))
	for _, row := range fresh {
		marks := make([]string, 0, len(columns))
		values := []any{row.id, ownerID, emptyToNil(departmentID)}
		for _, name := range fieldNames {
			values = append(values, row.fields[name])
		}
		for _, value := range values {
			args = append(args, value)
			marks = append(marks, fmt.Sprintf("$%d", len(args)))
		}
		groups = append(groups, "("+strings.Join(marks, ",")+")")
	}
	query := fmt.Sprintf("INSERT INTO asset.%s AS t (%s) VALUES %s RETURNING to_jsonb(t)", quoteIdentifier(schema.Table), strings.Join(columns, ","), strings.Join(groups, ","))
	inserted, err := tx.Query(ctx, query, args...)
	if err != nil {
		return BatchResult{}, err
	}
	records := make([]Record, 0, len(fresh))
	for inserted.Next() {
		var raw []byte
		if err = inserted.Scan(&raw); err != nil {
			inserted.Close()
			return BatchResult{}, err
		}
		record, decodeErr := decodeRecord(schema, raw)
		if decodeErr != nil {
			inserted.Close()
			return BatchResult{}, decodeErr
		}
		records = append(records, record)
	}
	inserted.Close()
	if err = inserted.Err(); err != nil {
		return BatchResult{}, err
	}
	if len(records) != len(fresh) {
		return BatchResult{}, fmt.Errorf("batch insert count mismatch")
	}
	versionArgs := make([]any, 0, len(records)*6)
	versionGroups := make([]string, 0, len(records))
	for _, record := range records {
		snapshot, marshalErr := json.Marshal(record)
		if marshalErr != nil {
			return BatchResult{}, marshalErr
		}
		marks := make([]string, 6)
		for i, value := range []any{record.Type, record.ID, record.Version, "import", ownerID, snapshot} {
			versionArgs = append(versionArgs, value)
			marks[i] = fmt.Sprintf("$%d", len(versionArgs))
		}
		versionGroups = append(versionGroups, "("+strings.Join(marks, ",")+")")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO asset.asset_versions(asset_type,asset_id,version,action,changed_by,snapshot) VALUES `+strings.Join(versionGroups, ","), versionArgs...); err != nil {
		return BatchResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return BatchResult{}, err
	}
	result.Created = len(fresh)
	return result, nil
}
