package utils

import (
	"errors"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	jwtSecret []byte
	jwtTTL    = 2 * time.Hour
	revokedMu sync.Mutex
	revoked   = make(map[string]time.Time)
)

// Claims JWT声明结构。
type Claims struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func ConfigureJWT(secret string, ttl time.Duration) {
	jwtSecret = []byte(secret)
	if ttl > 0 {
		jwtTTL = ttl
	}
}

// GenerateToken 生成带唯一JTI的短期JWT令牌。
func GenerateToken(userID uint, username, role string) (string, error) {
	if len(jwtSecret) < 32 {
		return "", errors.New("JWT密钥未正确配置")
	}
	now := time.Now()
	claims := Claims{
		UserID: userID, Username: username, Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			Issuer:    "asset-manager",
			ExpiresAt: jwt.NewNumericDate(now.Add(jwtTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// ParseToken 只接受HS256，并校验签名、签发者和标准时间声明。
func ParseToken(tokenString string) (*Claims, error) {
	if len(jwtSecret) < 32 {
		return nil, errors.New("JWT密钥未正确配置")
	}
	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(token *jwt.Token) (interface{}, error) { return jwtSecret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer("asset-manager"),
	)
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid || claims.ID == "" {
		return nil, jwt.ErrSignatureInvalid
	}
	if IsTokenRevoked(claims.ID) {
		return nil, errors.New("令牌已注销")
	}
	return claims, nil
}

func RevokeToken(tokenID string, expiresAt time.Time) {
	if tokenID == "" {
		return
	}
	revokedMu.Lock()
	defer revokedMu.Unlock()
	deleteExpiredRevocations(time.Now())
	revoked[tokenID] = expiresAt
}

func IsTokenRevoked(tokenID string) bool {
	revokedMu.Lock()
	defer revokedMu.Unlock()
	now := time.Now()
	deleteExpiredRevocations(now)
	_, exists := revoked[tokenID]
	return exists
}

func deleteExpiredRevocations(now time.Time) {
	for tokenID, expiresAt := range revoked {
		if !expiresAt.After(now) {
			delete(revoked, tokenID)
		}
	}
}
