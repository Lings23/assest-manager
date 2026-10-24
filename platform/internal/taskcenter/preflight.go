package taskcenter

import (
	"bytes"
	"context"
	"fmt"
)

func (w *Worker) runPreflight(ctx context.Context, execution Execution) (map[string]any, error) {
	key := stringInput(execution.Input, "source_object_key")
	if key == "" {
		return nil, PermanentFailure(fmt.Errorf("preflight source is missing"))
	}
	body, _, err := w.objects.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	check, err := PreflightCSV(execution.AssetType, body, false)
	closeErr := body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	result := map[string]any{"total": check.Total, "valid": check.Valid, "error_count": len(check.Errors), "source_object_key": key}
	if len(check.Errors) > 0 {
		var report bytes.Buffer
		if err = EncodeErrorReport(&report, check.Errors); err != nil {
			return nil, PermanentFailure(err)
		}
		errorKey := "import-errors/" + execution.ID + "/" + execution.Token + ".jsonl"
		if err = w.objects.Put(ctx, errorKey, report.Bytes(), "application/x-ndjson; charset=utf-8"); err != nil {
			return nil, err
		}
		result["error_object_key"] = errorKey
		result["error_download_url"] = "/api/v1/import-jobs/" + execution.ID + "/errors"
	}
	// Complete publishes these pointers only under the current execution lease.
	return result, nil
}
