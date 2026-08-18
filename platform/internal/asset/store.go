package asset

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"asset-platform/internal/assetschema"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/001_generated.sql
var assetMigration string

type Store struct {
	pool *pgxpool.Pool
}

type ListFilter struct {
	OwnerID        string
	DepartmentIDs  []string
	Global         bool
	IncludeDeleted bool
	Search         string
	SortField      string
	SortDescending bool
	Limit          int
	Offset         int
}

type Version struct {
	Version   int64           `json:"version"`
	Action    string          `json:"action"`
	ChangedBy string          `json:"changed_by"`
	Snapshot  json.RawMessage `json:"snapshot"`
	CreatedAt time.Time       `json:"created_at"`
}

func OpenStore(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	config.MaxConns = 10
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	if _, err := pool.Exec(ctx, assetMigration); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply asset migration: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) List(ctx context.Context, assetType string, filter ListFilter) ([]Record, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return nil, ErrUnknownType
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	var conditions []string
	var arguments []any
	addArgument := func(value any) string {
		arguments = append(arguments, value)
		return fmt.Sprintf("$%d", len(arguments))
	}
	if !filter.IncludeDeleted {
		conditions = append(conditions, "t.deleted_at IS NULL")
	}
	if !filter.Global {
		if filter.OwnerID != "" {
			conditions = append(conditions, "t.owner_id = "+addArgument(filter.OwnerID))
		} else if len(filter.DepartmentIDs) > 0 {
			conditions = append(conditions, "t.department_id = ANY("+addArgument(filter.DepartmentIDs)+")")
		} else {
			return []Record{}, nil
		}
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		var searchable []string
		placeholder := addArgument("%" + search + "%")
		for _, field := range schema.Fields {
			if field.Searchable {
				searchable = append(searchable, fmt.Sprintf("t.%s::text ILIKE %s", quoteIdentifier(field.Name), placeholder))
			}
		}
		if len(searchable) > 0 {
			conditions = append(conditions, "("+strings.Join(searchable, " OR ")+")")
		}
	}
	where := "TRUE"
	if len(conditions) > 0 {
		where = strings.Join(conditions, " AND ")
	}
	sortField := "updated_at"
	if filter.SortField != "" {
		for _, field := range schema.Fields {
			if field.Name == filter.SortField && field.Sortable {
				sortField = field.Name
				break
			}
		}
	}
	direction := "ASC"
	if filter.SortDescending || sortField == "updated_at" {
		direction = "DESC"
	}
	limitPlaceholder := addArgument(filter.Limit)
	offsetPlaceholder := addArgument(filter.Offset)
	query := fmt.Sprintf(`
		SELECT to_jsonb(t)
		FROM asset.%s AS t
		WHERE %s
		ORDER BY t.%s %s, t.id ASC
		LIMIT %s OFFSET %s
	`, quoteIdentifier(schema.Table), where, quoteIdentifier(sortField), direction, limitPlaceholder, offsetPlaceholder)
	rows, err := s.pool.Query(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []Record
	for rows.Next() {
		var value []byte
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		record, err := decodeRecord(schema, value)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) Get(ctx context.Context, assetType, id string, includeDeleted bool) (Record, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return Record{}, ErrUnknownType
	}
	return getRecord(ctx, s.pool, schema, id, includeDeleted, false)
}

func (s *Store) Create(
	ctx context.Context, assetType, ownerID, departmentID, changedBy string, fields map[string]any,
) (Record, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return Record{}, ErrUnknownType
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, err := newUUID()
	if err != nil {
		return Record{}, err
	}
	names := []string{"id", "owner_id", "department_id"}
	values := []any{id, ownerID, emptyToNil(departmentID)}
	for _, field := range schema.Fields {
		if value, exists := fields[field.Name]; exists {
			names = append(names, quoteIdentifier(field.Name))
			values = append(values, value)
		}
	}
	placeholders := make([]string, len(values))
	for index := range placeholders {
		placeholders[index] = fmt.Sprintf("$%d", index+1)
	}
	query := fmt.Sprintf("INSERT INTO asset.%s (%s) VALUES (%s)",
		quoteIdentifier(schema.Table), strings.Join(names, ", "), strings.Join(placeholders, ", "))
	if _, err := tx.Exec(ctx, query, values...); err != nil {
		return Record{}, err
	}
	record, err := getRecord(ctx, tx, schema, id, true, false)
	if err != nil {
		return Record{}, err
	}
	if err := writeVersion(ctx, tx, record, "create", changedBy); err != nil {
		return Record{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *Store) Update(
	ctx context.Context, assetType, id string, expectedVersion int64, changedBy string, fields map[string]any,
) (Record, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return Record{}, ErrUnknownType
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := getRecord(ctx, tx, schema, id, false, true)
	if err != nil {
		return Record{}, err
	}
	if current.Version != expectedVersion {
		return Record{}, ErrConflict
	}
	if len(fields) == 0 {
		return current, nil
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	sets := make([]string, 0, len(names)+2)
	arguments := make([]any, 0, len(names)+2)
	for _, name := range names {
		arguments = append(arguments, fields[name])
		sets = append(sets, fmt.Sprintf("%s = $%d", quoteIdentifier(name), len(arguments)))
	}
	sets = append(sets, "version = version + 1", "updated_at = now()")
	arguments = append(arguments, id, expectedVersion)
	query := fmt.Sprintf(`
		UPDATE asset.%s SET %s
		WHERE id = $%d AND version = $%d AND deleted_at IS NULL
	`, quoteIdentifier(schema.Table), strings.Join(sets, ", "), len(arguments)-1, len(arguments))
	tag, err := tx.Exec(ctx, query, arguments...)
	if err != nil {
		return Record{}, err
	}
	if tag.RowsAffected() != 1 {
		return Record{}, ErrConflict
	}
	record, err := getRecord(ctx, tx, schema, id, false, false)
	if err != nil {
		return Record{}, err
	}
	if err := writeVersion(ctx, tx, record, "update", changedBy); err != nil {
		return Record{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *Store) SoftDelete(
	ctx context.Context, assetType, id string, expectedVersion int64, changedBy string,
) (Record, error) {
	return s.changeDeletedState(ctx, assetType, id, expectedVersion, changedBy, true)
}

func (s *Store) Restore(
	ctx context.Context, assetType, id string, expectedVersion int64, changedBy string,
) (Record, error) {
	return s.changeDeletedState(ctx, assetType, id, expectedVersion, changedBy, false)
}

func (s *Store) ListVersions(ctx context.Context, assetType, id string) ([]Version, error) {
	if _, ok := assetschema.Types[assetType]; !ok {
		return nil, ErrUnknownType
	}
	rows, err := s.pool.Query(ctx, `
		SELECT version, action, changed_by, snapshot, created_at
		FROM asset.asset_versions
		WHERE asset_type = $1 AND asset_id = $2
		ORDER BY version DESC
	`, assetType, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []Version
	for rows.Next() {
		var version Version
		if err := rows.Scan(&version.Version, &version.Action, &version.ChangedBy, &version.Snapshot, &version.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s *Store) changeDeletedState(
	ctx context.Context, assetType, id string, expectedVersion int64, changedBy string, deleted bool,
) (Record, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return Record{}, ErrUnknownType
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := getRecord(ctx, tx, schema, id, true, true)
	if err != nil {
		return Record{}, err
	}
	if current.Version != expectedVersion || (deleted && current.DeletedAt != nil) || (!deleted && current.DeletedAt == nil) {
		return Record{}, ErrConflict
	}
	deletedExpression := "NULL"
	action := "restore"
	if deleted {
		deletedExpression = "now()"
		action = "delete"
	}
	query := fmt.Sprintf(`
		UPDATE asset.%s
		SET deleted_at = %s, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2
	`, quoteIdentifier(schema.Table), deletedExpression)
	tag, err := tx.Exec(ctx, query, id, expectedVersion)
	if err != nil {
		return Record{}, err
	}
	if tag.RowsAffected() != 1 {
		return Record{}, ErrConflict
	}
	record, err := getRecord(ctx, tx, schema, id, true, false)
	if err != nil {
		return Record{}, err
	}
	if err := writeVersion(ctx, tx, record, action, changedBy); err != nil {
		return Record{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, err
	}
	return record, nil
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getRecord(
	ctx context.Context, queryer queryer, schema assetschema.Schema, id string, includeDeleted, lock bool,
) (Record, error) {
	deletedCondition := "AND t.deleted_at IS NULL"
	if includeDeleted {
		deletedCondition = ""
	}
	lockClause := ""
	if lock {
		lockClause = " FOR UPDATE"
	}
	query := fmt.Sprintf(`
		SELECT to_jsonb(t)
		FROM asset.%s AS t
		WHERE t.id = $1 %s%s
	`, quoteIdentifier(schema.Table), deletedCondition, lockClause)
	var value []byte
	if err := queryer.QueryRow(ctx, query, id).Scan(&value); errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	} else if err != nil {
		return Record{}, err
	}
	return decodeRecord(schema, value)
}

func decodeRecord(schema assetschema.Schema, value []byte) (Record, error) {
	var raw map[string]any
	if err := json.Unmarshal(value, &raw); err != nil {
		return Record{}, err
	}
	record := Record{
		ID: stringValue(raw["id"]), Type: schema.Type,
		OwnerID: stringValue(raw["owner_id"]), DepartmentID: stringValue(raw["department_id"]),
		Version: int64Value(raw["version"]), Fields: make(map[string]any, len(schema.Fields)),
	}
	var err error
	record.CreatedAt, err = timeValue(raw["created_at"])
	if err != nil {
		return Record{}, err
	}
	record.UpdatedAt, err = timeValue(raw["updated_at"])
	if err != nil {
		return Record{}, err
	}
	if raw["deleted_at"] != nil {
		value, err := timeValue(raw["deleted_at"])
		if err != nil {
			return Record{}, err
		}
		record.DeletedAt = &value
	}
	for _, field := range schema.Fields {
		if fieldValue, exists := raw[field.Name]; exists && fieldValue != nil {
			record.Fields[field.Name] = fieldValue
		}
	}
	return record, nil
}

func writeVersion(ctx context.Context, tx pgx.Tx, record Record, action, changedBy string) error {
	snapshot, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO asset.asset_versions (
			asset_type, asset_id, version, action, changed_by, snapshot
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, record.Type, record.ID, record.Version, action, changedBy, snapshot)
	return err
}

func newUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func emptyToNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func stringValue(value any) string {
	typed, _ := value.(string)
	return typed
}

func int64Value(value any) int64 {
	number, _ := value.(float64)
	return int64(number)
}

func timeValue(value any) (time.Time, error) {
	typed, ok := value.(string)
	if !ok {
		return time.Time{}, fmt.Errorf("invalid database timestamp")
	}
	return time.Parse(time.RFC3339Nano, typed)
}
