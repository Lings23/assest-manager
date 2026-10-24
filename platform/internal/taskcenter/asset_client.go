package taskcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"asset-platform/internal/asset"
	"asset-platform/internal/servicekit"
)

type AssetQuery struct {
	OwnerID       string   `json:"owner_id"`
	DepartmentIDs []string `json:"department_ids"`
	Global        bool     `json:"global"`
	ViewSensitive bool     `json:"view_sensitive"`
}
type AssetClient interface {
	BatchLimits(context.Context) (asset.BatchLimits, error)
	Batch(context.Context, string, string, string, string, []asset.ImportRow) (asset.BatchResult, error)
	Export(context.Context, string, AssetQuery) ([]asset.Record, error)
	Counts(context.Context, AssetQuery) ([]asset.Count, error)
}

func (c *HTTPAssetClient) BatchLimits(ctx context.Context) (asset.BatchLimits, error) {
	var out asset.BatchLimits
	err := c.call(ctx, "/internal/v1/assets/batch-limits", struct{}{}, &out)
	return out, err
}

func (c *HTTPAssetClient) Counts(ctx context.Context, q AssetQuery) ([]asset.Count, error) {
	var out []asset.Count
	err := c.call(ctx, "/internal/v1/assets/counts", map[string]any{"owner_id": q.OwnerID, "department_ids": q.DepartmentIDs, "global": q.Global}, &out)
	return out, err
}

type HTTPAssetClient struct {
	endpoint, token string
	client          *http.Client
}

func NewHTTPAssetClient(endpoint, token string, observation ...*servicekit.Metrics) (*HTTPAssetClient, error) {
	parsed, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("invalid internal asset client configuration")
	}
	var metrics *servicekit.Metrics
	if len(observation) > 0 {
		metrics = observation[0]
	}
	return &HTTPAssetClient{endpoint: parsed.String(), token: token, client: &http.Client{Timeout: 60 * time.Second, Transport: servicekit.ObservedTransport(nil, metrics, "asset")}}, nil
}
func (c *HTTPAssetClient) Batch(ctx context.Context, taskID, assetType, owner, department string, rows []asset.ImportRow) (asset.BatchResult, error) {
	var out asset.BatchResult
	err := c.call(ctx, "/internal/v1/assets/"+url.PathEscape(assetType)+"/batch", asset.ImportBatchRequest{TaskID: taskID, OwnerID: owner, DepartmentID: department, Rows: rows}, &out)
	return out, err
}
func (c *HTTPAssetClient) Export(ctx context.Context, assetType string, q AssetQuery) ([]asset.Record, error) {
	var all []asset.Record
	offset := 0
	for {
		var page asset.ExportPage
		payload := map[string]any{"owner_id": q.OwnerID, "department_ids": q.DepartmentIDs, "global": q.Global, "view_sensitive": q.ViewSensitive, "offset": offset, "limit": 5000}
		if err := c.call(ctx, "/internal/v1/assets/"+url.PathEscape(assetType)+"/export-page", payload, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.Done {
			break
		}
		if page.NextOffset <= offset {
			return nil, fmt.Errorf("asset export pagination did not advance")
		}
		offset = page.NextOffset
	}
	return all, nil
}
func (c *HTTPAssetClient) call(ctx context.Context, path string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return PermanentFailure(fmt.Errorf("encode asset request: %w", err))
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", c.token)
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		err := fmt.Errorf("asset service returned %d: %s", res.StatusCode, strings.TrimSpace(string(message)))
		var envelope servicekit.ErrorResponse
		if json.Unmarshal(message, &envelope) == nil {
			// Match the internal API's deterministic business errors only. Older
			// BATCH_FAILED/EXPORT_FAILED envelopes also covered database outages.
			if res.StatusCode == http.StatusBadRequest && (envelope.Error.Code == "INVALID_REQUEST" || envelope.Error.Code == "VALIDATION_FAILED") ||
				res.StatusCode == http.StatusNotFound && envelope.Error.Code == "NOT_FOUND" {
				return PermanentFailure(err)
			}
		}
		return err
	}
	return json.NewDecoder(res.Body).Decode(output)
}
