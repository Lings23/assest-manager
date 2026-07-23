package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"
)

func TestValidateWritableFieldsRejectsSystemAndUnknownFields(t *testing.T) {
	assetTypes := []string{"system-info", "hardware", "data", "supply-chain", "vulnerability", "software-stat", "responsible-dept"}
	for _, assetType := range assetTypes {
		t.Run(assetType, func(t *testing.T) {
			columns := getAssetColumns(assetType)
			if len(columns) == 0 {
				t.Fatalf("asset type %s has no server-side column allowlist", assetType)
			}
			if err := validateWritableFields(assetType, map[string]interface{}{columns[0].dbColumn: "ok"}); err != nil {
				t.Fatalf("declared field rejected: %v", err)
			}
			for _, field := range []string{"id", "created_by", "created_at", "updated_at", "is_deleted", "unknown_field"} {
				if err := validateWritableFields(assetType, map[string]interface{}{field: 1}); err == nil {
					t.Errorf("field %s should be rejected", field)
				}
			}
		})
	}
}

func TestConvertedBooleanStillTriggersConditionalValidation(t *testing.T) {
	data := completeSystemInfoData()
	data["has_external_interface"] = "是"
	data["interface_scope"] = ""
	convertBooleanFields("system-info", data)
	if err := ValidateAssetData("system-info", data); err == nil || !strings.Contains(err.Error(), "对接范围") {
		t.Fatalf("converted true value must trigger interface scope validation, got %v", err)
	}
}

func TestNormalizeBooleanFieldsReturnsJSONBooleans(t *testing.T) {
	data := map[string]interface{}{"is_legalization_done": int64(0)}
	normalizeBooleanFields("software-stat", data)
	value, ok := data["is_legalization_done"].(bool)
	if !ok || value {
		t.Fatalf("expected false bool, got %#v", data["is_legalization_done"])
	}
}

func TestImportConversionRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name, value, fieldType, assetType, fieldName string
	}{
		{"boolean", "maybe", "boolean", "system-info", "has_external_interface"},
		{"enum", "极高", "enum", "vulnerability", "severity"},
		{"number", "many", "number", "hardware", "quantity"},
		{"date", "2026-99-99", "date", "hardware", "start_use_date"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := convertValueByType(tt.value, tt.fieldType, tt.assetType, tt.fieldName); err == nil {
				t.Fatalf("invalid %s value should fail", tt.fieldType)
			}
		})
	}
}

func TestLoginFailureLimiter(t *testing.T) {
	key := "127.0.0.1|security-test"
	clearLoginFailures(key)
	now := time.Now()
	for i := 0; i < loginFailureLimit; i++ {
		recordLoginFailure(key, now)
	}
	if locked, _ := loginLocked(key, now); !locked {
		t.Fatal("expected login key to be locked after failure limit")
	}
	clearLoginFailures(key)
}

func TestStatsSummaryUsesIntegerSeverityAndRoleScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	for _, table := range []string{"system_info_assets", "hardware_software_assets", "data_assets", "supply_chain_assets"} {
		if _, err := db.Exec("CREATE TABLE " + table + " (is_deleted BOOLEAN, created_by INTEGER)"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("CREATE TABLE vulnerability_assets (is_deleted BOOLEAN, created_by INTEGER, severity INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO vulnerability_assets VALUES (0, 7, 0), (0, 8, 0), (0, 7, 1)"); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("role", "reporter")
	context.Set("user_id", uint(7))
	GetStatsSummary(db)(context)
	if recorder.Code != 200 {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data map[string]int `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data["high_risk_vulns"] != 1 || response.Data["vulnerability_count"] != 2 {
		t.Fatalf("unexpected scoped counts: %#v", response.Data)
	}
}

func TestStatsFailureReturns500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Set("role", "admin")
	GetStatsSummary(db)(context)
	if recorder.Code != 500 {
		t.Fatalf("expected 500, got %d", recorder.Code)
	}
}
