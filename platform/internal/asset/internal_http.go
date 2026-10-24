package asset

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"asset-platform/internal/servicekit"
)

type InternalHTTPHandler struct {
	store        *Store
	token        string
	maxBodyBytes int64
}

func NewInternalHTTPHandler(store *Store, token string, outerBodyLimit ...int64) *InternalHTTPHandler {
	limit := int64(1 << 20)
	if len(outerBodyLimit) > 0 && outerBodyLimit[0] > 0 {
		limit = outerBodyLimit[0]
	}
	return &InternalHTTPHandler{store: store, token: strings.TrimSpace(token), maxBodyBytes: min(limit, InternalDecodeLimitBytes)}
}
func (h *InternalHTTPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/v1/assets/batch-limits", h.batchLimits)
	mux.HandleFunc("POST /internal/v1/assets/counts", h.counts)
	mux.HandleFunc("POST /internal/v1/assets/{type}/batch", h.batch)
	mux.HandleFunc("POST /internal/v1/assets/{type}/export-page", h.exportPage)
}
func (h *InternalHTTPHandler) allowed(r *http.Request) bool {
	provided := r.Header.Get("X-Internal-Token")
	return h.token != "" && len(provided) == len(h.token) && subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) == 1
}

type batchRequest = ImportBatchRequest

func (h *InternalHTTPHandler) batchLimits(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(r) {
		servicekit.WriteError(w, r, 403, "FORBIDDEN", "internal credentials required")
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, BatchLimits{MaxRows: MaxImportBatchRows, MaxBodyBytes: h.maxBodyBytes})
}

func (h *InternalHTTPHandler) batch(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(r) {
		servicekit.WriteError(w, r, 403, "FORBIDDEN", "internal credentials required")
		return
	}
	var q batchRequest
	if decodeInternal(r, &q) != nil {
		servicekit.WriteError(w, r, 400, "INVALID_REQUEST", "invalid batch")
		return
	}
	result, err := h.store.CreateBatchFast(r.Context(), q.TaskID, r.PathValue("type"), q.OwnerID, q.DepartmentID, q.Rows)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, 200, result)
}

type exportRequest struct {
	OwnerID       string   `json:"owner_id"`
	DepartmentIDs []string `json:"department_ids"`
	Global        bool     `json:"global"`
	Offset        int      `json:"offset"`
	Limit         int      `json:"limit"`
	ViewSensitive bool     `json:"view_sensitive"`
}

type countRequest struct {
	OwnerID       string   `json:"owner_id"`
	DepartmentIDs []string `json:"department_ids"`
	Global        bool     `json:"global"`
}

func (h *InternalHTTPHandler) counts(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(r) {
		servicekit.WriteError(w, r, 403, "FORBIDDEN", "internal credentials required")
		return
	}
	var q countRequest
	if decodeInternal(r, &q) != nil {
		servicekit.WriteError(w, r, 400, "INVALID_REQUEST", "invalid count query")
		return
	}
	counts, err := h.store.Counts(r.Context(), ListFilter{OwnerID: q.OwnerID, DepartmentIDs: q.DepartmentIDs, Global: q.Global})
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, 200, counts)
}

func (h *InternalHTTPHandler) exportPage(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(r) {
		servicekit.WriteError(w, r, 403, "FORBIDDEN", "internal credentials required")
		return
	}
	var q exportRequest
	if decodeInternal(r, &q) != nil {
		servicekit.WriteError(w, r, 400, "INVALID_REQUEST", "invalid export query")
		return
	}
	page, err := h.store.ExportPage(r.Context(), r.PathValue("type"), ListFilter{OwnerID: q.OwnerID, DepartmentIDs: q.DepartmentIDs, Global: q.Global, Limit: q.Limit, Offset: q.Offset, Internal: true}, q.ViewSensitive)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, 200, page)
}

func writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrUnknownType), errors.Is(err, ErrNotFound):
		servicekit.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	case errors.Is(err, ErrValidation):
		servicekit.WriteError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "asset validation failed")
	default:
		// A connection failure or an uncertain transaction commit must remain
		// retryable; callers replay stable import row keys after recovery.
		servicekit.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

func decodeInternal(r *http.Request, target any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, InternalDecodeLimitBytes))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("one value required")
	}
	return nil
}
