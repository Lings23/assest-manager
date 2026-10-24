package asset

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"asset-platform/internal/assetschema"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateLegacy(ctx context.Context, assetType, legacyID, ownerID, departmentID string, isDeleted bool, fields map[string]any) (Record, bool, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return Record{}, false, ErrUnknownType
	}
	if strings.TrimSpace(legacyID) == "" {
		return Record{}, false, ErrValidation
	}
	id, err := newUUID()
	if err != nil {
		return Record{}, false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Record{}, false, err
	}
	defer tx.Rollback(ctx)
	var reservedID string
	err = tx.QueryRow(ctx, `INSERT INTO asset.legacy_migrations(legacy_type,legacy_id,asset_id) VALUES($1,$2,$3) ON CONFLICT(legacy_type,legacy_id) DO NOTHING RETURNING asset_id`, assetType, legacyID, id).Scan(&reservedID)
	if err == pgx.ErrNoRows {
		// The conflicting insert waits for the winner to finish. This next
		// statement sees its committed mapping and asset under READ COMMITTED.
		var existingID string
		if err = tx.QueryRow(ctx, `SELECT asset_id FROM asset.legacy_migrations WHERE legacy_type=$1 AND legacy_id=$2`, assetType, legacyID).Scan(&existingID); err != nil {
			return Record{}, false, err
		}
		record, getErr := getRecord(ctx, tx, schema, existingID, true, false)
		if getErr != nil {
			return Record{}, false, getErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return Record{}, false, commitErr
		}
		return record, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	id = reservedID
	names := []string{"id", "owner_id", "department_id", "deleted_at"}
	values := []any{id, ownerID, emptyToNil(departmentID), nil}
	if isDeleted {
		values[3] = time.Now().UTC()
	}
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
		return Record{}, false, err
	}
	record, err := getRecord(ctx, tx, schema, id, true, false)
	if err != nil {
		return Record{}, false, err
	}
	if err = writeVersion(ctx, tx, record, map[bool]string{true: "migration_deleted", false: "migration"}[isDeleted], ownerID); err != nil {
		return Record{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}
