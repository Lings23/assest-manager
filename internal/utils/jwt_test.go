package utils

import (
	"testing"
	"time"
)

func TestTokenCanBeRevokedByJTI(t *testing.T) {
	ConfigureJWT("0123456789abcdef0123456789abcdef", time.Hour)
	token, err := GenerateToken(7, "tester", "reporter")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.ID == "" {
		t.Fatal("token must contain JTI")
	}
	RevokeToken(claims.ID, claims.ExpiresAt.Time)
	if _, err := ParseToken(token); err == nil {
		t.Fatal("revoked token must be rejected")
	}
}

func TestJWTRequiresConfiguredSecretLength(t *testing.T) {
	ConfigureJWT("short", time.Hour)
	if _, err := GenerateToken(1, "tester", "admin"); err == nil {
		t.Fatal("short JWT secret must be rejected")
	}
}
