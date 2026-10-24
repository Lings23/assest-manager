package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDryRunAndCheckpointIdempotency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE responsible_departments(id INTEGER PRIMARY KEY,department_name TEXT,department_code TEXT); INSERT INTO responsible_departments VALUES(1,'研发部','DEV')`); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/schema") {
			_ = json.NewEncoder(w).Encode(map[string]any{"schema": map[string]any{"fields": []map[string]any{{"name": "department_name", "type": "string", "required": true, "import": true}, {"name": "department_code", "type": "string", "import": true}}}})
			return
		}
		if r.Method == "POST" {
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"asset":{"id":"new-id"},"created":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	dry := execute(context.Background(), db, server.Client(), server.URL, "token", "responsible-department", "", "", true, map[string]string{})
	if dry.Valid != 1 || dry.Migrated != 0 {
		t.Fatalf("unexpected dry run: %#v", dry)
	}
	done := map[string]string{}
	first := execute(context.Background(), db, server.Client(), server.URL, "token", "responsible-department", "", "", false, done)
	if first.Migrated != 1 || done["responsible-department:1"] != "new-id" {
		t.Fatalf("unexpected migration: %#v %#v", first, done)
	}
	second := execute(context.Background(), db, server.Client(), server.URL, "token", "responsible-department", "", "", false, done)
	if second.Migrated != 0 || second.Skipped != 1 {
		t.Fatalf("not idempotent: %#v", second)
	}
}

func TestConversionsAndTargetSideIdempotency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE data_assets(id INTEGER PRIMARY KEY,data_name TEXT,data_level TEXT,is_cross_border TEXT,is_deleted INTEGER); INSERT INTO data_assets VALUES(10,'客户数据','核心','是',1)`)
	if err != nil {
		t.Fatal(err)
	}
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/schema") {
			_ = json.NewEncoder(w).Encode(map[string]any{"schema": map[string]any{"fields": []map[string]any{
				{"name": "data_name", "type": "string", "required": true, "import": true},
				{"name": "data_level", "type": "string", "required": true, "import": true, "enum": []map[string]any{{"label": "核心", "value": "core"}}},
				{"name": "is_cross_border", "type": "boolean", "required": true, "import": true},
			}}})
			return
		}
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/migrations/data/10") {
			posts++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["is_deleted"] != true {
				t.Errorf("soft delete lost: %#v", body)
			}
			fields := body["fields"].(map[string]any)
			if fields["data_level"] != "core" || fields["is_cross_border"] != true {
				t.Errorf("not corrected: %#v", fields)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"asset": map[string]any{"id": "stable-id"}, "created": posts == 1})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	first := execute(context.Background(), db, server.Client(), server.URL, "token", "data", "", "", false, map[string]string{})
	second := execute(context.Background(), db, server.Client(), server.URL, "token", "data", "", "", false, map[string]string{})
	if first.Migrated != 1 || second.Migrated != 0 || second.Skipped != 1 {
		t.Fatalf("server idempotency failed: first=%#v second=%#v", first, second)
	}
	if len(first.Corrections) < 2 {
		t.Fatalf("corrections not reported: %#v", first.Corrections)
	}
}
