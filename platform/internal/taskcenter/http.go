package taskcenter

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"asset-platform/internal/asset"
	"asset-platform/internal/iam"
	"asset-platform/internal/servicekit"
)

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service) *HTTPHandler { return &HTTPHandler{service: service} }

type createRequest struct {
	AssetType      string `json:"asset_type"`
	IdempotencyKey string `json:"idempotency_key"`
	DepartmentID   string `json:"department_id"`
	CSV            string `json:"csv,omitempty"`
}

func (h *HTTPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/import-jobs", func(w http.ResponseWriter, r *http.Request) { h.list(w, r, "import") })
	mux.HandleFunc("POST /api/v1/import-jobs", func(w http.ResponseWriter, r *http.Request) { h.create(w, r, "import") })
	mux.HandleFunc("GET /api/v1/export-jobs", func(w http.ResponseWriter, r *http.Request) { h.list(w, r, "export") })
	mux.HandleFunc("POST /api/v1/export-jobs", func(w http.ResponseWriter, r *http.Request) { h.create(w, r, "export") })
	mux.HandleFunc("GET /api/v1/tasks/{id}", h.get)
	mux.HandleFunc("POST /api/v1/tasks/{id}/cancel", h.cancel)
	mux.HandleFunc("POST /api/v1/import-jobs/{id}/confirm", h.confirm)
	mux.HandleFunc("GET /api/v1/import-jobs/{id}/errors", h.downloadImportErrors)
	mux.HandleFunc("GET /api/v1/export-jobs/{id}/download", h.download)
	mux.HandleFunc("GET /api/v1/reporting/{report}", h.report)
}
func (h *HTTPHandler) create(w http.ResponseWriter, r *http.Request, kind string) {
	var q createRequest
	if err := decode(r, &q); err != nil || strings.TrimSpace(q.AssetType) == "" || strings.TrimSpace(q.IdempotencyKey) == "" || (kind == "import" && q.CSV == "") {
		servicekit.WriteRequestError(w, r, err, "INVALID_REQUEST", "asset_type, idempotency_key and import csv are required")
		return
	}
	t, created, err := h.service.Create(r.Context(), r.Header.Get("Authorization"), kind, q.AssetType, q.IdempotencyKey, q.DepartmentID, q.CSV)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	status := http.StatusAccepted
	if !created {
		status = http.StatusOK
	}
	servicekit.WriteJSON(w, status, map[string]any{"task": publicTask(t), "created": created})
}
func (h *HTTPHandler) list(w http.ResponseWriter, r *http.Request, kind string) {
	v, e := h.service.List(r.Context(), r.Header.Get("Authorization"), kind)
	if e != nil {
		h.writeError(w, r, e)
		return
	}
	servicekit.WriteJSON(w, 200, map[string]any{"items": publicTasks(v)})
}
func (h *HTTPHandler) get(w http.ResponseWriter, r *http.Request) {
	v, e := h.service.Get(r.Context(), r.Header.Get("Authorization"), r.PathValue("id"))
	if e != nil {
		h.writeError(w, r, e)
		return
	}
	servicekit.WriteJSON(w, 200, map[string]any{"task": publicTask(v)})
}
func (h *HTTPHandler) cancel(w http.ResponseWriter, r *http.Request) {
	v, e := h.service.Cancel(r.Context(), r.Header.Get("Authorization"), r.PathValue("id"))
	if e != nil {
		h.writeError(w, r, e)
		return
	}
	servicekit.WriteJSON(w, 200, map[string]any{"task": publicTask(v)})
}
func (h *HTTPHandler) confirm(w http.ResponseWriter, r *http.Request) {
	v, e := h.service.Confirm(r.Context(), r.Header.Get("Authorization"), r.PathValue("id"))
	if e != nil {
		h.writeError(w, r, e)
		return
	}
	servicekit.WriteJSON(w, 202, map[string]any{"task": publicTask(v)})
}
func (h *HTTPHandler) download(w http.ResponseWriter, r *http.Request) {
	body, contentType, err := h.service.Download(r.Context(), r.Header.Get("Authorization"), r.PathValue("id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	defer body.Close()
	if contentType == "" {
		contentType = "text/csv; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="export-`+r.PathValue("id")+`.csv"`)
	_, _ = io.Copy(w, body)
}
func (h *HTTPHandler) downloadImportErrors(w http.ResponseWriter, r *http.Request) {
	body, contentType, err := h.service.DownloadImportErrors(r.Context(), r.Header.Get("Authorization"), r.PathValue("id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	defer body.Close()
	if contentType == "" {
		contentType = "application/x-ndjson; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="import-errors-`+r.PathValue("id")+`.jsonl"`)
	_, _ = io.Copy(w, body)
}
func (h *HTTPHandler) report(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("report") != "asset-counts" {
		servicekit.WriteError(w, r, 404, "NOT_FOUND", "report not found")
		return
	}
	counts, err := h.service.AssetCounts(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, 200, map[string]any{"items": counts})
}
func (h *HTTPHandler) writeError(w http.ResponseWriter, r *http.Request, e error) {
	switch {
	case errors.Is(e, iam.ErrUnauthorized):
		servicekit.WriteError(w, r, 401, "UNAUTHENTICATED", "valid access token is required")
	case errors.Is(e, asset.ErrForbidden):
		servicekit.WriteError(w, r, 403, "FORBIDDEN", "action is not permitted")
	case errors.Is(e, ErrNotFound):
		servicekit.WriteError(w, r, 404, "NOT_FOUND", "task not found")
	case errors.Is(e, ErrConflict):
		servicekit.WriteError(w, r, 409, "STATE_CONFLICT", "task state has changed")
	case errors.Is(e, ErrInvalid) || strings.Contains(e.Error(), "preflight"):
		servicekit.WriteError(w, r, 400, "VALIDATION_FAILED", e.Error())
	default:
		servicekit.WriteError(w, r, 500, "INTERNAL_ERROR", "internal server error")
	}
}
func decode(r *http.Request, target any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(target); e != nil {
		return e
	}
	if e := d.Decode(&struct{}{}); !errors.Is(e, io.EOF) {
		return errors.New("one JSON value required")
	}
	return nil
}
