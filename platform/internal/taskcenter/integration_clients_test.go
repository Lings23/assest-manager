package taskcenter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"asset-platform/internal/asset"
)

func TestS3StoreSignedPutAndGet(t *testing.T) {
	var object []byte
	lifecycleConfigured := false
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("missing SigV4 authorization")
		}
		if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/asset-exports/file.csv") {
			object, _ = io.ReadAll(r.Body)
			w.WriteHeader(200)
			return
		}
		if r.Method == "PUT" && r.URL.Path == "/asset-exports" && r.URL.RawQuery == "lifecycle=" {
			configuration, _ := io.ReadAll(r.Body)
			lifecycleConfigured = strings.Contains(string(configuration), "<Prefix>imports/</Prefix>") && strings.Count(string(configuration), "<Days>7</Days>") == 3 && r.Header.Get("Content-MD5") != "" && strings.Contains(r.Header.Get("Authorization"), "SignedHeaders=content-md5;")
			w.WriteHeader(200)
			return
		}
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "text/csv")
			_, _ = w.Write(object)
			return
		}
		if r.Method == "DELETE" && strings.HasSuffix(r.URL.Path, "/asset-exports/file.csv") {
			deleted = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	store, err := NewS3Store(server.URL, "access", "secret", "asset-exports", 7)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = store.Put(context.Background(), "file.csv", []byte("a,b\n1,2\n"), "text/csv"); err != nil {
		t.Fatal(err)
	}
	body, contentType, err := store.Get(context.Background(), "file.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, _ := io.ReadAll(body)
	if string(data) != "a,b\n1,2\n" || contentType != "text/csv" {
		t.Fatalf("unexpected object %q %q", data, contentType)
	}
	if err = store.Delete(context.Background(), "file.csv"); err != nil {
		t.Fatal(err)
	}
	if !lifecycleConfigured || !deleted {
		t.Fatalf("lifecycle configured=%t deleted=%t", lifecycleConfigured, deleted)
	}
}

func TestImportSourceKeyIsStableAndContentAddressed(t *testing.T) {
	first := importSourceKey("user-1", "request-1", []byte("a,b\n1,2\n"))
	if first != importSourceKey("user-1", "request-1", []byte("a,b\n1,2\n")) {
		t.Fatal("same import must produce the same key")
	}
	if first == importSourceKey("user-1", "request-1", []byte("a,b\n2,3\n")) || !strings.HasPrefix(first, "imports/user-1/") {
		t.Fatalf("unexpected content-addressed key %q", first)
	}
}

func TestExportCSVUsesGeneratedColumns(t *testing.T) {
	data, err := exportCSV("responsible-department", []asset.Record{{ID: "id-1", OwnerID: "owner", DepartmentID: "dept", Fields: map[string]any{"department_name": "研发部", "department_code": "DEV"}}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "department_name") || !strings.Contains(text, "研发部") {
		t.Fatalf("unexpected csv: %q", text)
	}
}
