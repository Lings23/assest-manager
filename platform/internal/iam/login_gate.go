package iam

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type LoginGate interface {
	Reserve(context.Context, string) (time.Time, bool, error)
	ReleaseSuccess(context.Context, string, time.Time) error
}

// Reserve counts failures and in-flight attempts together in a fixed 1m window.
// A lost process/response consumes its reservation until expiry (fail closed).
func (s *PostgresStore) Reserve(ctx context.Context, key string) (time.Time, bool, error) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	var opened time.Time
	err := s.pool.QueryRow(ctx, `INSERT INTO iam.login_limits(key_hash,opened_at,expires_at,attempts)
 VALUES($1,clock_timestamp(),clock_timestamp()+interval '1 minute',1)
 ON CONFLICT(key_hash) DO UPDATE SET
 opened_at=CASE WHEN iam.login_limits.expires_at<=clock_timestamp() THEN clock_timestamp() ELSE iam.login_limits.opened_at END,
 expires_at=CASE WHEN iam.login_limits.expires_at<=clock_timestamp() THEN clock_timestamp()+interval '1 minute' ELSE iam.login_limits.expires_at END,
 attempts=CASE WHEN iam.login_limits.expires_at<=clock_timestamp() THEN 1 ELSE iam.login_limits.attempts+1 END
 WHERE iam.login_limits.expires_at<=clock_timestamp() OR iam.login_limits.attempts<5 RETURNING opened_at`, hash).Scan(&opened)
	if err == pgx.ErrNoRows {
		return opened, false, nil
	}
	return opened, err == nil, err
}

func (s *PostgresStore) ReleaseSuccess(ctx context.Context, key string, opened time.Time) error {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	_, err := s.pool.Exec(ctx, `UPDATE iam.login_limits SET attempts=GREATEST(0,attempts-1) WHERE key_hash=$1 AND opened_at=$2`, hash, opened)
	return err
}

func (s *PostgresStore) CleanupLoginLimits(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, _ = s.pool.Exec(checkCtx, `DELETE FROM iam.login_limits WHERE key_hash IN (SELECT key_hash FROM iam.login_limits WHERE expires_at<clock_timestamp()-interval '1 minute' LIMIT 1000)`)
			cancel()
		}
	}
}

func ParseTrustedProxies(value string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, item := range strings.Split(value, ",") {
		if strings.TrimSpace(item) == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS: %w", err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func trustedClientIP(r *http.Request, proxies []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	trusted := func(ip netip.Addr) bool {
		for _, prefix := range proxies {
			if prefix.Contains(ip.Unmap()) {
				return true
			}
		}
		return false
	}
	if !trusted(peer) {
		return peer.String()
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(chain) > 16 {
		return peer.String()
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if !trusted(peer) {
			break
		}
		next, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			return host
		}
		peer = next.Unmap()
	}
	return peer.String()
}
