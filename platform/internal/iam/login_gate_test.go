package iam

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

func TestClientIPTrustBoundary(t *testing.T) {
	proxies, err := ParseTrustedProxies("10.0.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ peer, forwarded, want string }{
		{"192.0.2.1:1234", "203.0.113.2", "192.0.2.1"},
		{"10.0.0.2:1234", "203.0.113.2", "203.0.113.2"},
		{"10.0.0.2:1234", "198.51.100.1, 192.0.2.1", "192.0.2.1"},
		{"10.0.0.2:1234", "forged", "10.0.0.2"},
	} {
		r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
		r.RemoteAddr = test.peer
		r.Header.Set("X-Forwarded-For", test.forwarded)
		if got := trustedClientIP(r, proxies); got != test.want {
			t.Fatalf("%+v got=%s", test, got)
		}
	}
	if _, err := ParseTrustedProxies("trust-everyone"); err == nil {
		t.Fatal("bad CIDR accepted")
	}
}

func TestPostgresLoginReservationsAreGlobalAtomicAndSurviveReconnect(t *testing.T) {
	url := os.Getenv("IAM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IAM_TEST_DATABASE_URL is required")
	}
	if err := Migrate(t.Context(), url); err != nil {
		t.Fatal(err)
	}
	a, err := OpenPostgresStore(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenPostgresStore(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	suffix, _ := randomUUID()
	key := t.Name() + suffix
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := a
			if i%2 == 0 {
				store = b
			}
			_, allowed, err := store.Reserve(t.Context(), key)
			if err != nil {
				t.Error(err)
			}
			if allowed {
				accepted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 5 {
		t.Fatalf("accepted %d concurrent attempts", accepted.Load())
	}
	c, err := OpenPostgresStore(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, allowed, err := c.Reserve(t.Context(), key); err != nil || allowed {
		t.Fatalf("reconnect reset quota: allowed=%v err=%v", allowed, err)
	}
	if _, err := a.pool.Exec(t.Context(), `UPDATE iam.login_limits SET expires_at=clock_timestamp()-interval '1 second' WHERE key_hash=$1`, fmt.Sprintf("%x", sha256.Sum256([]byte(key)))); err != nil {
		t.Fatal(err)
	}
	ticket, allowed, err := b.Reserve(t.Context(), key)
	if err != nil || !allowed {
		t.Fatalf("expired window not renewed: %v %v", allowed, err)
	}
	if err = b.ReleaseSuccess(t.Context(), key, ticket); err != nil {
		t.Fatal(err)
	}
}
