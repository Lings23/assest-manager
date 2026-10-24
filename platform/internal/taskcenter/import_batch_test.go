package taskcenter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"asset-platform/internal/asset"
)

func TestImportBatcherRealEncodingAndStableRows(t *testing.T) {
	task := Task{ID: workerTaskID, RequestedBy: "owner<&中文", DepartmentID: "dept>"}
	for _, count := range []int{500, 1000, 10000, 100000} {
		for _, wide := range []bool{false, true} {
			if !wide && count != 500 {
				continue
			}
			t.Run(fmt.Sprintf("rows%d-wide%v", count, wide), func(t *testing.T) {
				fields := map[string]any{"department_name": "name中文", "department_code": "value"}
				if wide {
					fields["remarks"] = strings.Repeat("<&>中文", 180)
				}
				all := make([]map[string]any, count)
				for i := range all {
					all[i] = fields
				}
				limits := asset.BatchLimits{MaxRows: 500, MaxBodyBytes: 1 << 20}
				batcher, err := newImportBatcher(task, limits)
				if err != nil {
					t.Fatal(err)
				}
				batches := 0
				for start := 0; start < len(all); {
					rows, end, size, err := batcher.next(all, start)
					if err != nil {
						t.Fatal(err)
					}
					if end <= start || len(rows) > 500 {
						t.Fatalf("no progress or row overflow: %d %d", start, end)
					}
					request := batcher.template
					request.Rows = rows
					encoded, err := json.Marshal(request)
					if err != nil || int64(len(encoded)) != size || size > limits.MaxBodyBytes {
						t.Fatalf("real encoding=%d predicted=%d hard=%d err=%v", len(encoded), size, limits.MaxBodyBytes, err)
					}
					for i, row := range rows {
						if row.Key != fmt.Sprintf("%s:%d", task.ID, start+i+2) {
							t.Fatal("CSV key changed across byte boundaries")
						}
					}
					if wide && len(rows) >= 500 {
						t.Fatal("wide fixed 500 rows were not split")
					}
					start = end
					batches++
				}
				if !wide && batches != 1 {
					t.Fatalf("ordinary batch count=%d", batches)
				}
			})
		}
	}
}

func TestImportBatcherHardLimitAndSafetyMargin(t *testing.T) {
	task := Task{ID: workerTaskID, RequestedBy: "owner", DepartmentID: "dept"}
	const hard int64 = 1 << 20
	batcher, err := newImportBatcher(task, asset.BatchLimits{MaxRows: 500, MaxBodyBytes: hard})
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"department_name": "name", "department_code": "code", "remarks": ""}
	request := batcher.template
	request.Rows = []asset.ImportRow{{Key: task.ID + ":2", Fields: row}}
	empty, _ := json.Marshal(request)
	for _, delta := range []int64{-1, 0, 1} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			fields := map[string]any{"department_name": "name", "department_code": "code", "remarks": strings.Repeat("x", int(hard-int64(len(empty))+delta))}
			rows, end, size, err := batcher.next([]map[string]any{fields}, 0)
			if delta > 0 {
				if !IsPermanentTaskError(err) || !strings.Contains(err.Error(), "CSV row 2") || !strings.Contains(err.Error(), "bytes") {
					t.Fatalf("oversize must be explicit/permanent: %v", err)
				}
				return
			}
			if err != nil || end != 1 || len(rows) != 1 || size != hard+delta {
				t.Fatalf("valid near-limit row rejected: size=%d end=%d err=%v", size, end, err)
			}
		})
	}
}
