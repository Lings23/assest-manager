package asset

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"asset-platform/internal/servicekit"
)

type HTTPHandler struct {
	service *Service
}

type createRequest struct {
	DepartmentID string         `json:"department_id"`
	Fields       map[string]any `json:"fields"`
}

type updateRequest struct {
	Version int64          `json:"version"`
	Fields  map[string]any `json:"fields"`
}

type versionRequest struct {
	Version int64 `json:"version"`
}

func NewHTTPHandler(service *Service) *HTTPHandler {
	return &HTTPHandler{service: service}
}

func (h *HTTPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/asset-types", h.listTypes)
	mux.HandleFunc("GET /api/v1/asset-types/{type}/schema", h.getSchema)
	mux.HandleFunc("GET /api/v1/assets/{type}", h.list)
	mux.HandleFunc("POST /api/v1/assets/{type}", h.create)
	mux.HandleFunc("GET /api/v1/assets/{type}/{id}", h.get)
	mux.HandleFunc("PATCH /api/v1/assets/{type}/{id}", h.update)
	mux.HandleFunc("DELETE /api/v1/assets/{type}/{id}", h.delete)
	mux.HandleFunc("POST /api/v1/assets/{type}/{id}/restore", h.restore)
	mux.HandleFunc("GET /api/v1/assets/{type}/{id}/versions", h.versions)
}

func (h *HTTPHandler) listTypes(w http.ResponseWriter, r *http.Request) {
	if _, err := h.service.authorizer.Authorize(r.Context(), r.Header.Get("Authorization"), "asset:read"); err != nil {
		h.writeError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{"asset_types": h.service.Schemas()})
}

func (h *HTTPHandler) getSchema(w http.ResponseWriter, r *http.Request) {
	if _, err := h.service.authorizer.Authorize(r.Context(), r.Header.Get("Authorization"), "asset:read"); err != nil {
		h.writeError(w, r, err)
		return
	}
	schema, err := h.service.Schema(r.PathValue("type"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{"schema": schema})
}

func (h *HTTPHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	records, err := h.service.List(r.Context(), r.Header.Get("Authorization"), r.PathValue("type"), ListFilter{
		IncludeDeleted: r.URL.Query().Get("include_deleted") == "true",
		Search:         r.URL.Query().Get("search"), SortField: r.URL.Query().Get("sort"),
		SortDescending: strings.EqualFold(r.URL.Query().Get("direction"), "desc"),
		Limit:          limit, Offset: offset,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{"items": records, "limit": limit, "offset": offset})
}

func (h *HTTPHandler) create(w http.ResponseWriter, r *http.Request) {
	var request createRequest
	if err := decodeRequest(r, &request); err != nil || request.Fields == nil {
		servicekit.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid asset request")
		return
	}
	record, err := h.service.Create(
		r.Context(), r.Header.Get("Authorization"), r.PathValue("type"),
		request.DepartmentID, request.Fields,
	)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, http.StatusCreated, map[string]any{"asset": record})
}

func (h *HTTPHandler) get(w http.ResponseWriter, r *http.Request) {
	record, err := h.service.Get(
		r.Context(), r.Header.Get("Authorization"), r.PathValue("type"),
		r.PathValue("id"), r.URL.Query().Get("include_deleted") == "true",
	)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{"asset": record})
}

func (h *HTTPHandler) update(w http.ResponseWriter, r *http.Request) {
	var request updateRequest
	if err := decodeRequest(r, &request); err != nil || request.Version < 1 || request.Fields == nil {
		servicekit.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "version and fields are required")
		return
	}
	record, err := h.service.Update(
		r.Context(), r.Header.Get("Authorization"), r.PathValue("type"),
		r.PathValue("id"), request.Version, request.Fields,
	)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{"asset": record})
}

func (h *HTTPHandler) delete(w http.ResponseWriter, r *http.Request) {
	version, err := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
	if err != nil || version < 1 {
		servicekit.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "version is required")
		return
	}
	err = h.service.Delete(
		r.Context(), r.Header.Get("Authorization"), r.PathValue("type"), r.PathValue("id"), version,
	)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) restore(w http.ResponseWriter, r *http.Request) {
	var request versionRequest
	if err := decodeRequest(r, &request); err != nil || request.Version < 1 {
		servicekit.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "version is required")
		return
	}
	record, err := h.service.Restore(
		r.Context(), r.Header.Get("Authorization"), r.PathValue("type"), r.PathValue("id"), request.Version,
	)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{"asset": record})
}

func (h *HTTPHandler) versions(w http.ResponseWriter, r *http.Request) {
	versions, err := h.service.Versions(
		r.Context(), r.Header.Get("Authorization"), r.PathValue("type"), r.PathValue("id"),
	)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (h *HTTPHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case IsUnauthorized(err):
		servicekit.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "valid access token is required")
	case errors.Is(err, ErrForbidden):
		servicekit.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "action is not permitted")
	case errors.Is(err, ErrUnknownType), errors.Is(err, ErrNotFound):
		servicekit.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	case errors.Is(err, ErrValidation):
		servicekit.WriteError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "asset validation failed")
	case errors.Is(err, ErrConflict):
		servicekit.WriteError(w, r, http.StatusConflict, "VERSION_CONFLICT", "asset version has changed")
	default:
		servicekit.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

func decodeRequest(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}
