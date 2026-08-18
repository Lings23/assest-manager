package iam

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"asset-platform/internal/servicekit"
)

const refreshCookieName = "asset_refresh"

type HTTPConfig struct {
	CookieSecure bool
}

type HTTPHandler struct {
	service *Service
	config  HTTPConfig
	logger  *slog.Logger
	limiter *loginLimiter
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type createUserRequest struct {
	Username           string    `json:"username"`
	DisplayName        string    `json:"display_name"`
	Password           string    `json:"password"`
	DepartmentID       string    `json:"department_id"`
	Roles              []Role    `json:"roles"`
	DataScope          DataScope `json:"data_scope"`
	MustChangePassword bool      `json:"must_change_password"`
}

type updateAccessRequest struct {
	Enabled      bool      `json:"enabled"`
	DepartmentID string    `json:"department_id"`
	Roles        []Role    `json:"roles"`
	DataScope    DataScope `json:"data_scope"`
}

type createDepartmentRequest struct {
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

type authorizeRequest struct {
	Action string `json:"action"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func NewHTTPHandler(service *Service, config HTTPConfig, logger *slog.Logger) *HTTPHandler {
	return &HTTPHandler{
		service: service, config: config, logger: logger,
		limiter: newLoginLimiter(5, time.Minute),
	}
}

func (h *HTTPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.HandleFunc("GET /api/v1/auth/me", h.me)
	mux.HandleFunc("POST /api/v1/auth/change-password", h.changePassword)
	mux.HandleFunc("POST /api/v1/auth/authorize", h.authorize)
	mux.HandleFunc("GET /api/v1/users", h.requireAdmin(h.listUsers))
	mux.HandleFunc("POST /api/v1/users", h.requireAdmin(h.createUser))
	mux.HandleFunc("PATCH /api/v1/users/{id}/access", h.requireAdmin(h.updateAccess))
	mux.HandleFunc("GET /api/v1/departments", h.requireAdmin(h.listDepartments))
	mux.HandleFunc("POST /api/v1/departments", h.requireAdmin(h.createDepartment))
}

func (h *HTTPHandler) changePassword(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request changePasswordRequest
	if err := decodeJSON(r, &request); err != nil {
		servicekit.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid password change request")
		return
	}
	if err := h.service.ChangePassword(r.Context(), principal, request.CurrentPassword, request.NewPassword); err != nil {
		switch {
		case errors.Is(err, ErrInvalidCredentials):
			servicekit.WriteError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "current password is incorrect")
		case errors.Is(err, ErrInvalidInput):
			servicekit.WriteError(w, r, http.StatusBadRequest, "INVALID_PASSWORD", "new password does not meet requirements")
		default:
			servicekit.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "password change failed")
		}
		return
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) authorize(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request authorizeRequest
	if err := decodeJSON(r, &request); err != nil {
		servicekit.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid authorization request")
		return
	}
	authorization, err := h.service.Authorize(r.Context(), principal, request.Action)
	if errors.Is(err, ErrForbidden) {
		servicekit.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "action is not permitted")
		return
	}
	if err != nil {
		servicekit.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "authorization failed")
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, authorization)
}

func (h *HTTPHandler) listDepartments(w http.ResponseWriter, r *http.Request, _ Principal) {
	departments, err := h.service.ListDepartments(r.Context())
	if err != nil {
		servicekit.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list departments")
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{"departments": departments})
}

func (h *HTTPHandler) createDepartment(w http.ResponseWriter, r *http.Request, _ Principal) {
	var request createDepartmentRequest
	if err := decodeJSON(r, &request); err != nil {
		servicekit.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid department")
		return
	}
	department, err := h.service.CreateDepartment(r.Context(), NewDepartment{
		Name: request.Name, ParentID: request.ParentID,
	})
	if err != nil {
		h.writeDomainError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, http.StatusCreated, map[string]any{"department": department})
}

func (h *HTTPHandler) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := decodeJSON(r, &request); err != nil {
		servicekit.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid login request")
		return
	}
	limiterKey := clientIP(r) + "\x00" + normalizeUsername(request.Username)
	if !h.limiter.Allow(limiterKey, time.Now().UTC()) {
		servicekit.WriteError(w, r, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", "too many failed login attempts")
		return
	}
	pair, err := h.service.Login(r.Context(), request.Username, request.Password)
	if err != nil {
		h.limiter.Failed(limiterKey, time.Now().UTC())
		servicekit.WriteError(w, r, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid username or password")
		return
	}
	h.limiter.Succeeded(limiterKey)
	h.writeTokenPair(w, pair)
}

func (h *HTTPHandler) refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		servicekit.WriteError(w, r, http.StatusUnauthorized, "INVALID_SESSION", "refresh session is invalid")
		return
	}
	pair, err := h.service.Refresh(r.Context(), cookie.Value)
	if err != nil {
		h.clearRefreshCookie(w)
		code := "INVALID_SESSION"
		if errors.Is(err, ErrRefreshReuse) {
			code = "REFRESH_REUSE_DETECTED"
			h.logger.Warn("refresh token reuse detected", "request_id", servicekit.RequestID(r.Context()))
		}
		servicekit.WriteError(w, r, http.StatusUnauthorized, code, "refresh session is invalid")
		return
	}
	h.writeTokenPair(w, pair)
}

func (h *HTTPHandler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(refreshCookieName); err == nil {
		_ = h.service.Logout(r.Context(), cookie.Value)
	}
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) me(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{"user": principal})
}

func (h *HTTPHandler) listUsers(w http.ResponseWriter, r *http.Request, _ Principal) {
	users, err := h.service.ListUsers(r.Context())
	if err != nil {
		servicekit.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list users")
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (h *HTTPHandler) createUser(w http.ResponseWriter, r *http.Request, _ Principal) {
	var request createUserRequest
	if err := decodeJSON(r, &request); err != nil {
		servicekit.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid user")
		return
	}
	user, err := h.service.CreateUser(r.Context(), NewUser{
		Username: request.Username, DisplayName: request.DisplayName, Password: request.Password,
		DepartmentID: request.DepartmentID, Roles: request.Roles, DataScope: request.DataScope,
		MustChangePassword: request.MustChangePassword,
	})
	if err != nil {
		h.writeDomainError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (h *HTTPHandler) updateAccess(w http.ResponseWriter, r *http.Request, principal Principal) {
	var request updateAccessRequest
	if err := decodeJSON(r, &request); err != nil {
		servicekit.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid access policy")
		return
	}
	if r.PathValue("id") == principal.UserID && (!request.Enabled || !containsRole(request.Roles, RoleAdmin)) {
		servicekit.WriteError(w, r, http.StatusConflict, "LAST_ADMIN_GUARD", "administrators cannot remove their own access")
		return
	}
	user, err := h.service.UpdateAccess(r.Context(), UpdateAccessParams{
		UserID: r.PathValue("id"), Enabled: request.Enabled, DepartmentID: request.DepartmentID,
		Roles: request.Roles, DataScope: request.DataScope,
	})
	if err != nil {
		h.writeDomainError(w, r, err)
		return
	}
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h *HTTPHandler) requireAdmin(next func(http.ResponseWriter, *http.Request, Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := h.authenticate(w, r)
		if !ok {
			return
		}
		if !principal.HasRole(RoleAdmin) {
			servicekit.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "administrator role is required")
			return
		}
		next(w, r, principal)
	}
}

func (h *HTTPHandler) authenticate(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")) == "" {
		servicekit.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "valid access token is required")
		return Principal{}, false
	}
	principal, err := h.service.Authenticate(r.Context(), strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
	if err != nil {
		servicekit.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "valid access token is required")
		return Principal{}, false
	}
	return principal, true
}

func (h *HTTPHandler) writeTokenPair(w http.ResponseWriter, pair TokenPair) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Value: pair.RefreshToken, Path: "/api/v1/auth",
		HttpOnly: true, Secure: h.config.CookieSecure, SameSite: http.SameSiteStrictMode,
		Expires: time.Now().UTC().Add(refreshTTL), MaxAge: int(refreshTTL.Seconds()),
	})
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{
		"access_token": pair.AccessToken, "token_type": "Bearer",
		"expires_in": int(accessTTL.Seconds()), "user": pair.Principal,
	})
}

func (h *HTTPHandler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Value: "", Path: "/api/v1/auth",
		HttpOnly: true, Secure: h.config.CookieSecure, SameSite: http.SameSiteStrictMode,
		MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

func (h *HTTPHandler) writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		servicekit.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "request validation failed")
	case errors.Is(err, ErrConflict):
		servicekit.WriteError(w, r, http.StatusConflict, "CONFLICT", "resource already exists")
	case errors.Is(err, ErrNotFound):
		servicekit.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	default:
		servicekit.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON value")
	}
	return nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func containsRole(roles []Role, wanted Role) bool {
	for _, role := range roles {
		if role == wanted {
			return true
		}
	}
	return false
}

type attemptWindow struct {
	failures []time.Time
}

type loginLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	items  map[string]attemptWindow
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{limit: limit, window: window, items: make(map[string]attemptWindow)}
}

func (l *loginLimiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	item := l.items[key]
	item.failures = recentFailures(item.failures, now.Add(-l.window))
	l.items[key] = item
	return len(item.failures) < l.limit
}

func (l *loginLimiter) Failed(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	item := l.items[key]
	item.failures = append(recentFailures(item.failures, now.Add(-l.window)), now)
	l.items[key] = item
}

func (l *loginLimiter) Succeeded(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.items, key)
}

func recentFailures(failures []time.Time, cutoff time.Time) []time.Time {
	result := failures[:0]
	for _, failure := range failures {
		if failure.After(cutoff) {
			result = append(result, failure)
		}
	}
	return result
}
