package asset

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

	"asset-platform/internal/iam"
)

type Authorizer interface {
	Authorize(context.Context, string, string) (iam.AuthorizationContext, error)
}

type IAMAuthorizer struct {
	endpoint string
	client   *http.Client
}

func NewIAMAuthorizer(rawURL string) (*IAMAuthorizer, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("invalid IAM service URL")
	}
	return &IAMAuthorizer{
		endpoint: strings.TrimRight(parsed.String(), "/") + "/api/v1/auth/authorize",
		client:   &http.Client{Timeout: 5 * time.Second},
	}, nil
}

func (a *IAMAuthorizer) Authorize(
	ctx context.Context, authorizationHeader, action string,
) (iam.AuthorizationContext, error) {
	body, _ := json.Marshal(map[string]string{"action": action})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return iam.AuthorizationContext{}, err
	}
	request.Header.Set("Authorization", authorizationHeader)
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return iam.AuthorizationContext{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return iam.AuthorizationContext{}, iam.ErrUnauthorized
	}
	if response.StatusCode == http.StatusForbidden {
		return iam.AuthorizationContext{}, ErrForbidden
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return iam.AuthorizationContext{}, fmt.Errorf("IAM authorization returned status %d", response.StatusCode)
	}
	var result iam.AuthorizationContext
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return iam.AuthorizationContext{}, err
	}
	return result, nil
}
