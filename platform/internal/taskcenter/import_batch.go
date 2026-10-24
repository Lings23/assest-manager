package taskcenter

import (
	"encoding/json"
	"fmt"

	"asset-platform/internal/asset"
)

// importBatcher uses the same request type and encoder as HTTPAssetClient.
// It partitions before each durable progress commit, preserving CSV row keys
// and the exact range committed after every real asset-service transaction.
type importBatcher struct {
	template      asset.ImportBatchRequest
	limits        asset.BatchLimits
	envelopeBytes int64
	targetBytes   int64
}

func newImportBatcher(task Task, limits asset.BatchLimits) (*importBatcher, error) {
	if limits.MaxRows < 1 || limits.MaxRows > asset.MaxImportBatchRows || limits.MaxBodyBytes < 1 || limits.MaxBodyBytes > asset.InternalDecodeLimitBytes {
		return nil, fmt.Errorf("invalid asset batch limits")
	}
	template := asset.ImportBatchRequest{TaskID: task.ID, OwnerID: task.RequestedBy, DepartmentID: task.DepartmentID, Rows: []asset.ImportRow{}}
	encoded, err := json.Marshal(template)
	if err != nil {
		return nil, PermanentFailure(fmt.Errorf("encode import envelope: %w", err))
	}
	return &importBatcher{template: template, limits: limits, envelopeBytes: int64(len(encoded)), targetBytes: limits.MaxBodyBytes * 95 / 100}, nil
}

func (b *importBatcher) next(all []map[string]any, start int) ([]asset.ImportRow, int, int64, error) {
	rows := make([]asset.ImportRow, 0, min(b.limits.MaxRows, len(all)-start))
	size := b.envelopeBytes
	for end := start; end < len(all) && len(rows) < b.limits.MaxRows; end++ {
		row := asset.ImportRow{Key: fmt.Sprintf("%s:%d", b.template.TaskID, end+2), Fields: all[end]}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, start, 0, PermanentFailure(fmt.Errorf("encode CSV row %d: %w", end+2, err))
		}
		nextSize := size + int64(len(encoded))
		if len(rows) > 0 {
			nextSize++
		} // delimiter between encoded rows
		if len(rows) == 0 && nextSize > b.limits.MaxBodyBytes {
			return nil, start, 0, PermanentFailure(fmt.Errorf("CSV row %d serializes to %d bytes, exceeding asset batch limit %d bytes", end+2, nextSize, b.limits.MaxBodyBytes))
		}
		if len(rows) > 0 && nextSize > b.targetBytes {
			break
		}
		rows = append(rows, row)
		size = nextSize
		// A single legal near-limit row may exceed the normal safety target.
		// Reject only the actual hard limit, never a valid row in this margin.
		if size > b.targetBytes {
			break
		}
	}
	return rows, start + len(rows), size, nil
}
