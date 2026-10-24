package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Import   bool   `json:"import"`
	Enum     []struct {
		Label string `json:"label"`
		Value any    `json:"value"`
	} `json:"enum"`
}
type schema struct {
	Type   string  `json:"type"`
	Fields []field `json:"fields"`
}
type problem struct {
	AssetType string `json:"asset_type"`
	LegacyID  string `json:"legacy_id"`
	Field     string `json:"field,omitempty"`
	Message   string `json:"message"`
}
type report struct {
	DryRun      bool                  `json:"dry_run"`
	Scanned     int                   `json:"scanned"`
	Valid       int                   `json:"valid"`
	Migrated    int                   `json:"migrated"`
	Skipped     int                   `json:"skipped"`
	Types       map[string]*typeStats `json:"types"`
	Corrections []correction          `json:"corrections,omitempty"`
	Errors      []problem             `json:"errors,omitempty"`
}

type typeStats struct {
	Scanned  int `json:"scanned"`
	Valid    int `json:"valid"`
	Migrated int `json:"migrated"`
	Skipped  int `json:"skipped"`
}

type correction struct {
	AssetType   string `json:"asset_type"`
	LegacyID    string `json:"legacy_id"`
	SourceField string `json:"source_field"`
	TargetField string `json:"target_field"`
	From        any    `json:"from,omitempty"`
	To          any    `json:"to,omitempty"`
	Reason      string `json:"reason"`
}

var tables = map[string]string{"system-info": "system_info_assets", "hardware-software": "hardware_software_assets", "data": "data_assets", "supply-chain": "supply_chain_assets", "vulnerability": "vulnerability_assets", "software-statistics": "software_statistics", "responsible-department": "responsible_departments"}

// Explicit aliases make legacy drift reviewable instead of silently guessing.
var columnAliases = map[string]map[string]string{
	"data":        {"cross_border": "cross_border", "is_cross_border": "is_cross_border"},
	"system-info": {"has_media_platform": "has_media_platform"},
}

func main() {
	var source, api, token, only, resume, reportPath, checkpoint, department string
	var dry bool
	flag.StringVar(&source, "source", "", "SQLite backup path")
	flag.StringVar(&api, "api", "http://127.0.0.1:8080/api/v1", "new platform API")
	flag.StringVar(&token, "token", "", "access token (or ASSET_MIGRATION_TOKEN)")
	flag.StringVar(&only, "asset-type", "", "single asset type")
	flag.StringVar(&resume, "resume-from", "", "resume at legacy id")
	flag.StringVar(&reportPath, "report", "migration-report.json", "JSON report")
	flag.StringVar(&checkpoint, "checkpoint", "migration-checkpoint.json", "idempotency checkpoint")
	flag.StringVar(&department, "department-id", "", "target department UUID")
	flag.BoolVar(&dry, "dry-run", false, "validate only")
	flag.Parse()
	if token == "" {
		token = os.Getenv("ASSET_MIGRATION_TOKEN")
	}
	if source == "" || (!dry && token == "") {
		fatal("--source and, unless --dry-run, --token are required")
	}
	db, e := sql.Open("sqlite", "file:"+source+"?mode=ro")
	if e != nil {
		fatal(e.Error())
	}
	defer db.Close()
	done := loadCheckpoint(checkpoint)
	client := &http.Client{Timeout: 30 * time.Second}
	result := execute(context.Background(), db, client, strings.TrimRight(api, "/"), token, only, resume, department, dry, done)
	writeJSON(reportPath, result)
	if !dry {
		writeJSON(checkpoint, done)
	}
	fmt.Printf("scanned=%d valid=%d migrated=%d skipped=%d errors=%d\n", result.Scanned, result.Valid, result.Migrated, result.Skipped, len(result.Errors))
	if len(result.Errors) > 0 {
		os.Exit(2)
	}
}

func execute(ctx context.Context, db *sql.DB, client *http.Client, api, token, only, resume, department string, dry bool, done map[string]string) report {
	out := report{DryRun: dry, Types: map[string]*typeStats{}}
	types := make([]string, 0, len(tables))
	for typ := range tables {
		if only == "" || typ == only {
			types = append(types, typ)
		}
	}
	sort.Strings(types)
	for _, typ := range types {
		stats := &typeStats{}
		out.Types[typ] = stats
		contract, e := getSchema(ctx, client, api, token, typ)
		if e != nil {
			out.Errors = append(out.Errors, problem{AssetType: typ, Message: e.Error()})
			continue
		}
		rows, e := db.QueryContext(ctx, `SELECT * FROM "`+tables[typ]+`" ORDER BY id`)
		if e != nil {
			out.Errors = append(out.Errors, problem{AssetType: typ, Message: e.Error()})
			continue
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			raw := make([]any, len(columns))
			ptr := make([]any, len(columns))
			for i := range raw {
				ptr[i] = &raw[i]
			}
			if e = rows.Scan(ptr...); e != nil {
				out.Errors = append(out.Errors, problem{AssetType: typ, Message: e.Error()})
				continue
			}
			legacyID := ""
			isDeleted := false
			values := map[string]any{}
			for i, name := range columns {
				if name == "id" {
					legacyID = fmt.Sprint(raw[i])
					continue
				}
				if name == "is_deleted" {
					isDeleted, _ = convertBoolean(raw[i])
					continue
				}
				target := name
				if alias, ok := columnAliases[typ][name]; ok {
					target = alias
					if alias != name {
						out.Corrections = append(out.Corrections, correction{typ, legacyID, name, alias, nil, nil, "column_alias"})
					}
				}
				if f, ok := contract[target]; ok && raw[i] != nil {
					converted, changed, reason := convert(f, raw[i])
					values[target] = converted
					if changed {
						out.Corrections = append(out.Corrections, correction{typ, legacyID, name, target, printable(raw[i]), converted, reason})
					}
				}
			}
			key := typ + ":" + legacyID
			if beforeResume(legacyID, resume) || done[key] != "" {
				out.Skipped++
				stats.Skipped++
				continue
			}
			out.Scanned++
			stats.Scanned++
			issues := validate(contract, values)
			if len(issues) > 0 {
				for _, p := range issues {
					p.AssetType = typ
					p.LegacyID = legacyID
					out.Errors = append(out.Errors, p)
				}
				continue
			}
			out.Valid++
			stats.Valid++
			if dry {
				continue
			}
			id, created, e := create(ctx, client, api, token, typ, legacyID, department, isDeleted, values)
			if e != nil {
				out.Errors = append(out.Errors, problem{typ, legacyID, "", e.Error()})
				continue
			}
			done[key] = id
			if created {
				out.Migrated++
				stats.Migrated++
			} else {
				out.Skipped++
				stats.Skipped++
			}
		}
		rows.Close()
	}
	return out
}
func getSchema(ctx context.Context, c *http.Client, api, token, typ string) (map[string]field, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", api+"/asset-types/"+typ+"/schema", nil)
	req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(token, "Bearer "))
	res, e := c.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("schema request returned %d", res.StatusCode)
	}
	var body struct {
		Schema schema `json:"schema"`
	}
	if e = json.NewDecoder(res.Body).Decode(&body); e != nil {
		return nil, e
	}
	out := map[string]field{}
	for _, f := range body.Schema.Fields {
		if f.Import {
			out[f.Name] = f
		}
	}
	return out, nil
}
func create(ctx context.Context, c *http.Client, api, token, typ, legacyID, department string, isDeleted bool, values map[string]any) (string, bool, error) {
	data, _ := json.Marshal(map[string]any{"department_id": department, "is_deleted": isDeleted, "fields": values})
	req, _ := http.NewRequestWithContext(ctx, "POST", api+"/migrations/"+typ+"/"+legacyID, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(token, "Bearer "))
	req.Header.Set("Content-Type", "application/json")
	res, e := c.Do(req)
	if e != nil {
		return "", false, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 201 {
		return "", false, fmt.Errorf("migration create returned %d", res.StatusCode)
	}
	var body struct {
		Asset struct {
			ID string `json:"id"`
		} `json:"asset"`
		Created bool `json:"created"`
	}
	e = json.NewDecoder(res.Body).Decode(&body)
	return body.Asset.ID, body.Created, e
}
func validate(contract map[string]field, values map[string]any) []problem {
	var out []problem
	for name, f := range contract {
		v, ok := values[name]
		if f.Required && !ok {
			out = append(out, problem{Field: name, Message: "field is required"})
			continue
		}
		if !ok {
			continue
		}
		switch f.Type {
		case "boolean":
			if _, ok = v.(bool); !ok {
				out = append(out, problem{Field: name, Message: "must be boolean"})
			}
		case "integer":
			if _, ok = v.(int64); !ok {
				out = append(out, problem{Field: name, Message: "must be integer"})
			}
		case "number":
			if _, ok = v.(float64); !ok {
				out = append(out, problem{Field: name, Message: "must be number"})
			}
		case "date":
			if text, yes := v.(string); !yes || !validDate(text) {
				out = append(out, problem{Field: name, Message: "must be YYYY-MM-DD date"})
			}
		}
		if len(f.Enum) > 0 && !enumContains(f, v) {
			out = append(out, problem{Field: name, Message: "value is not in enum"})
		}
	}
	return out
}
func convert(f field, v any) (any, bool, string) {
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	if f.Type == "boolean" {
		if b, ok := convertBoolean(v); ok {
			return b, fmt.Sprint(v) != fmt.Sprint(b), "boolean_normalized"
		}
	}
	if f.Type == "integer" {
		if text, ok := v.(string); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64); err == nil {
				return n, true, "integer_normalized"
			}
		}
	}
	if f.Type == "number" {
		switch x := v.(type) {
		case int64:
			return float64(x), true, "number_normalized"
		case string:
			if n, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
				return n, true, "number_normalized"
			}
		}
	}
	if f.Type == "date" {
		if text, ok := v.(string); ok {
			normalized := strings.ReplaceAll(strings.TrimSpace(text), "/", "-")
			return normalized, normalized != text, "date_normalized"
		}
	}
	for _, option := range f.Enum {
		if text, ok := v.(string); ok && strings.EqualFold(strings.TrimSpace(text), option.Label) {
			value := option.Value
			if f.Type == "integer" {
				if number, ok := value.(float64); ok {
					value = int64(number)
				}
			}
			return value, fmt.Sprint(value) != text, "enum_label_to_value"
		}
	}
	return v, false, ""
}

func convertBoolean(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case int64:
		return x != 0, true
	case float64:
		return x != 0, true
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "1", "true", "yes", "y", "是":
			return true, true
		case "0", "false", "no", "n", "否":
			return false, true
		}
	}
	return false, false
}
func enumContains(f field, value any) bool {
	for _, option := range f.Enum {
		if fmt.Sprint(option.Value) == fmt.Sprint(value) {
			return true
		}
	}
	return false
}
func validDate(value string) bool { _, err := time.Parse("2006-01-02", value); return err == nil }
func printable(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}
func beforeResume(id, resume string) bool {
	if resume == "" {
		return false
	}
	a, ea := strconv.ParseInt(id, 10, 64)
	b, eb := strconv.ParseInt(resume, 10, 64)
	if ea == nil && eb == nil {
		return a < b
	}
	return id < resume
}
func loadCheckpoint(path string) map[string]string {
	out := map[string]string{}
	data, e := os.ReadFile(path)
	if e == nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}
func writeJSON(path string, v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	if e := os.WriteFile(path, data, 0600); e != nil {
		fatal(e.Error())
	}
}
func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
