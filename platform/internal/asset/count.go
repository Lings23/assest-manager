package asset

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"asset-platform/internal/assetschema"
)

type Count struct {
	AssetType string `json:"asset_type"`
	Total     int64  `json:"total"`
}

func hasAssetScope(filter ListFilter) bool {
	return filter.Global || filter.OwnerID != "" || len(filter.DepartmentIDs) > 0
}

// assetFilterSQL is shared by List, ExportPage and Count. An empty authorization
// scope always denies access, including when the caller requests deleted rows.
func assetFilterSQL(schema assetschema.Schema, filter ListFilter, offset int) (string, []any) {
	var conditions []string
	var args []any
	add := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", offset+len(args))
	}
	if !filter.IncludeDeleted {
		conditions = append(conditions, "t.deleted_at IS NULL")
	}
	if !filter.Global {
		switch {
		case filter.OwnerID != "":
			conditions = append(conditions, "t.owner_id = "+add(filter.OwnerID))
		case len(filter.DepartmentIDs) > 0:
			conditions = append(conditions, "t.department_id = ANY("+add(filter.DepartmentIDs)+")")
		default:
			conditions = append(conditions, "FALSE")
		}
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		var searchable []string
		for _, field := range schema.Fields {
			if field.Searchable {
				searchable = append(searchable, fmt.Sprintf("t.%s::text ILIKE %%s", quoteIdentifier(field.Name)))
			}
		}
		if len(searchable) > 0 {
			placeholder := add("%" + search + "%")
			for i := range searchable {
				searchable[i] = fmt.Sprintf(searchable[i], placeholder)
			}
			conditions = append(conditions, "("+strings.Join(searchable, " OR ")+")")
		}
	}
	if len(conditions) == 0 {
		return "TRUE", args
	}
	return strings.Join(conditions, " AND "), args
}

func (s *Store) Count(ctx context.Context, assetType string, filter ListFilter) (int64, error) {
	schema, ok := assetschema.Types[assetType]
	if !ok {
		return 0, ErrUnknownType
	}
	where, args := assetFilterSQL(schema, filter, 0)
	var total int64
	err := s.pool.QueryRow(ctx, "SELECT count(*) FROM asset."+quoteIdentifier(schema.Table)+" AS t WHERE "+where, args...).Scan(&total)
	return total, err
}

// Counts uses one HTTP call and one PostgreSQL statement/snapshot for all types.
// No business fields or sensitive values leave the asset service.
func (s *Store) Counts(ctx context.Context, filter ListFilter) ([]Count, error) {
	types := make([]string, 0, len(assetschema.Types))
	for name := range assetschema.Types {
		types = append(types, name)
	}
	sort.Strings(types)
	queries := make([]string, 0, len(types))
	var args []any
	for _, name := range types {
		schema := assetschema.Types[name]
		where, parameters := assetFilterSQL(schema, filter, len(args))
		args = append(args, parameters...)
		// Both identifiers and type literals originate exclusively in the schema.
		queries = append(queries, fmt.Sprintf("SELECT '%s'::text AS asset_type, count(*) AS total FROM asset.%s AS t WHERE %s", name, quoteIdentifier(schema.Table), where))
	}
	rows, err := s.pool.Query(ctx, strings.Join(queries, " UNION ALL ")+" ORDER BY asset_type", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make([]Count, 0, len(types))
	for rows.Next() {
		var count Count
		if err := rows.Scan(&count.AssetType, &count.Total); err != nil {
			return nil, err
		}
		counts = append(counts, count)
	}
	return counts, rows.Err()
}
