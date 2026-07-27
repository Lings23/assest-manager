package iam

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

type TokenSigner struct {
	private  *rsa.PrivateKey
	public   *rsa.PublicKey
	issuer   string
	audience string
	now      func() time.Time
}

type accessClaims struct {
	Subject            string    `json:"sub"`
	Username           string    `json:"username"`
	DisplayName        string    `json:"display_name"`
	DepartmentID       string    `json:"department_id,omitempty"`
	Roles              []Role    `json:"roles"`
	DataScope          DataScope `json:"data_scope"`
	PermissionsVersion int64     `json:"pv"`
	MustChangePassword bool      `json:"must_change_password"`
	Issuer             string    `json:"iss"`
	Audience           string    `json:"aud"`
	IssuedAt           int64     `json:"iat"`
	ExpiresAt          int64     `json:"exp"`
	JWTID              string    `json:"jti"`
}

func NewTokenSigner(private *rsa.PrivateKey, issuer, audience string) (*TokenSigner, error) {
	if private == nil || private.N.BitLen() < 2048 {
		return nil, fmt.Errorf("RSA private key must be at least 2048 bits")
	}
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(audience) == "" {
		return nil, fmt.Errorf("issuer and audience are required")
	}
	return &TokenSigner{
		private: private, public: &private.PublicKey,
		issuer: issuer, audience: audience, now: time.Now,
	}, nil
}

func GenerateRSAKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}

func ParseRSAPrivateKeyPEM(value []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(value)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM private key")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
		return rsaKey, nil
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA private key: %w", err)
	}
	return key, nil
}

func (s *TokenSigner) Sign(principal Principal, ttl time.Duration) (string, time.Time, error) {
	now := s.now().UTC()
	expiresAt := now.Add(ttl)
	jti, err := randomOpaqueToken(16)
	if err != nil {
		return "", time.Time{}, err
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := accessClaims{
		Subject: principal.UserID, Username: principal.Username, DisplayName: principal.DisplayName,
		DepartmentID: principal.DepartmentID, Roles: principal.Roles, DataScope: principal.DataScope,
		PermissionsVersion: principal.PermissionsVersion, MustChangePassword: principal.MustChangePassword,
		Issuer: s.issuer, Audience: s.audience, IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), JWTID: jti,
	}
	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.private, crypto.SHA256, digest[:])
	if err != nil {
		return "", time.Time{}, err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), expiresAt, nil
}

func (s *TokenSigner) Verify(token string) (Principal, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Principal{}, ErrUnauthorized
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if json.Unmarshal(headerJSON, &header) != nil || header.Algorithm != "RS256" || header.Type != "JWT" {
		return Principal{}, ErrUnauthorized
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(s.public, crypto.SHA256, digest[:], signature) != nil {
		return Principal{}, ErrUnauthorized
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	var claims accessClaims
	if json.Unmarshal(claimsJSON, &claims) != nil ||
		claims.Issuer != s.issuer || claims.Audience != s.audience ||
		claims.Subject == "" || claims.JWTID == "" ||
		claims.ExpiresAt <= s.now().UTC().Unix() {
		return Principal{}, ErrUnauthorized
	}
	if err := validateAccess(claims.Roles, claims.DataScope); err != nil {
		return Principal{}, ErrUnauthorized
	}
	return Principal{
		UserID: claims.Subject, Username: claims.Username, DisplayName: claims.DisplayName,
		DepartmentID: claims.DepartmentID, Roles: claims.Roles, DataScope: claims.DataScope,
		PermissionsVersion: claims.PermissionsVersion, MustChangePassword: claims.MustChangePassword,
	}, nil
}

func randomOpaqueToken(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func randomUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
