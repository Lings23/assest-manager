package iam

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"asset-platform/internal/servicekit"
)

func newIAMHTTPTestHandler(t *testing.T) http.Handler {
	t.Helper()
	service, _, _ := newTestService(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	api := NewHTTPHandler(service, HTTPConfig{CookieSecure: true}, logger)
	cfg := servicekit.Config{
		ServiceName: "iam-service", HTTPAddr: ":8081",
		ShutdownTimeout: time.Second, MaxBodyBytes: 1 << 20,
	}
	return servicekit.NewHandler(cfg, logger, api.Register)
}

func TestLoginMeRefreshAndLogoutHTTP(t *testing.T) {
	handler := newIAMHTTPTestHandler(t)
	loginBody := []byte(`{"username":"admin","password":"correct horse battery staple"}`)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	login.RemoteAddr = "192.0.2.1:1234"
	login.Header.Set("Content-Type", "application/json")
	loginResult := httptest.NewRecorder()
	handler.ServeHTTP(loginResult, login)
	if loginResult.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginResult.Code, loginResult.Body)
	}
	var loginResponse struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginResult.Body.Bytes(), &loginResponse); err != nil || loginResponse.AccessToken == "" {
		t.Fatalf("invalid login response: %v %s", err, loginResult.Body)
	}
	cookies := loginResult.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("refresh cookie is not hardened: %+v", cookies)
	}

	me := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	me.Header.Set("Authorization", "Bearer "+loginResponse.AccessToken)
	meResult := httptest.NewRecorder()
	handler.ServeHTTP(meResult, me)
	if meResult.Code != http.StatusOK || !strings.Contains(meResult.Body.String(), `"username":"admin"`) {
		t.Fatalf("me status=%d body=%s", meResult.Code, meResult.Body)
	}

	refresh := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	refresh.AddCookie(cookies[0])
	refreshResult := httptest.NewRecorder()
	handler.ServeHTTP(refreshResult, refresh)
	if refreshResult.Code != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", refreshResult.Code, refreshResult.Body)
	}

	logout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logout.AddCookie(refreshResult.Result().Cookies()[0])
	logoutResult := httptest.NewRecorder()
	handler.ServeHTTP(logoutResult, logout)
	if logoutResult.Code != http.StatusNoContent || logoutResult.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("logout status=%d cookies=%+v", logoutResult.Code, logoutResult.Result().Cookies())
	}
}

func TestLoginRateLimitAndUnknownFields(t *testing.T) {
	handler := newIAMHTTPTestHandler(t)
	for attempt := 0; attempt < 5; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"username":"admin","password":"wrong password value"}`))
		request.RemoteAddr = "192.0.2.8:1234"
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d", attempt, result.Code)
		}
	}
	blocked := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"admin","password":"correct horse battery staple"}`))
	blocked.RemoteAddr = "192.0.2.8:5678"
	blockedResult := httptest.NewRecorder()
	handler.ServeHTTP(blockedResult, blocked)
	if blockedResult.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit status=%d body=%s", blockedResult.Code, blockedResult.Body)
	}

	unknown := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"admin","password":"value","admin":true}`))
	unknown.RemoteAddr = "192.0.2.9:1234"
	unknownResult := httptest.NewRecorder()
	handler.ServeHTTP(unknownResult, unknown)
	if unknownResult.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", unknownResult.Code, unknownResult.Body)
	}
}

func TestNonAdminCannotManageUsers(t *testing.T) {
	service, store, _ := newTestService(t)
	reporter, err := service.CreateUser(t.Context(), NewUser{
		Username: "reporter", DisplayName: "填报员", Password: "reporter secure password",
		Roles: []Role{RoleReporter}, DataScope: ScopeOwn,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = store
	pair, err := service.Login(t.Context(), reporter.Username, "reporter secure password")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	api := NewHTTPHandler(service, HTTPConfig{}, logger)
	handler := servicekit.NewHandler(servicekit.Config{
		ServiceName: "iam-service", HTTPAddr: ":8081",
		ShutdownTimeout: time.Second, MaxBodyBytes: 1 << 20,
	}, logger, api.Register)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	request.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusForbidden {
		t.Fatalf("non-admin status=%d body=%s", result.Code, result.Body)
	}
}
