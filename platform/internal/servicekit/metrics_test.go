package servicekit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func isolatedMetrics(t *testing.T) *Metrics {
	t.Helper()
	serviceMetrics.Delete(t.Name())
	t.Cleanup(func() { serviceMetrics.Delete(t.Name()) })
	return MetricsFor(t.Name())
}
func metricCount(h *durationMetric) uint64 {
	var count uint64
	for i := range h.buckets {
		count += h.buckets[i].Load()
	}
	return count
}

func TestMetricsNormalizeRoutesAndRejectUntrustedScrapes(t *testing.T) {
	m := isolatedMetrics(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /assets/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := m.InstrumentHTTP(mux, mux)
	for _, id := range []string{"private-dynamic-id-one", "private-dynamic-id-two"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/assets/"+id, nil))
	}
	for _, id := range []string{"unmatched-private-one", "unmatched-private-two"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/"+id, nil))
	}
	var output strings.Builder
	m.write(&output)
	text := output.String()
	for _, private := range []string{"private-dynamic", "unmatched-private", "query_fingerprint"} {
		if strings.Contains(text, private) {
			t.Fatalf("unbounded/private label: %s", private)
		}
	}
	if len(m.http) != 2 || !strings.Contains(text, `route="GET /assets/{id}"`) || !strings.Contains(text, `route="unmatched"`) {
		t.Fatalf("route normalization failed: %s", text)
	}
	for _, test := range []struct {
		remote, token string
		expected      int
	}{
		{"192.0.2.1:1234", "", 403}, {"192.0.2.1:1234", "wrong", 403}, {"192.0.2.1:1234", "scrape-secret", 200}, {"127.0.0.1:1234", "", 200}, {"[::1]:1234", "", 200},
	} {
		r := httptest.NewRequest("GET", "/metrics/service", nil)
		r.RemoteAddr = test.remote
		r.Header.Set("X-Metrics-Token", test.token)
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		w := httptest.NewRecorder()
		m.Handler("scrape-secret").ServeHTTP(w, r)
		if w.Code != test.expected || strings.Contains(w.Body.String(), "scrape-secret") {
			t.Fatalf("scrape authorization=%d expected=%d", w.Code, test.expected)
		}
	}
}

func TestUpstreamMetricsObserveBodyCompletionExactlyOnce(t *testing.T) {
	m := isolatedMetrics(t)
	gate := make(chan struct{})
	seenID := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenID <- r.Header.Get("X-Request-ID")
		_, _ = w.Write([]byte("first"))
		w.(http.Flusher).Flush()
		<-gate
		_, _ = w.Write([]byte("second"))
	}))
	defer server.Close()
	ctx := context.WithValue(t.Context(), requestIDKey, "correlation-id")
	r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/private-object-key", nil)
	client := http.Client{Transport: ObservedTransport(nil, m, "object_store")}
	response, err := client.Do(r)
	if err != nil {
		close(gate)
		t.Fatal(err)
	}
	if <-seenID != "correlation-id" || r.Header.Get("X-Request-ID") != "" {
		close(gate)
		t.Fatal("request propagation mutated the caller")
	}
	metric := m.upstreamMetric("object_store", 200)
	if metricCount(&metric.durationMetric) != 0 {
		close(gate)
		t.Fatal("upstream duration ended at headers")
	}
	close(gate)
	data, err := io.ReadAll(response.Body)
	if err != nil || string(data) != "firstsecond" {
		t.Fatalf("body=%q err=%v", data, err)
	}
	_ = response.Body.Close()
	_ = response.Body.Close()
	if metricCount(&metric.durationMetric) != 1 || metric.receivedBytes.Load() != 11 {
		t.Fatalf("duplicate duration or wrong body bytes: count=%d bytes=%d", metricCount(&metric.durationMetric), metric.receivedBytes.Load())
	}
	var output strings.Builder
	m.write(&output)
	if strings.Contains(output.String(), "private-object-key") {
		t.Fatal("object path became a metric label")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestUpstreamTimeoutAndOriginalTransport(t *testing.T) {
	m := isolatedMetrics(t)
	base := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	transport := ObservedTransport(base, m, "asset")
	r, _ := http.NewRequest("POST", "http://example.invalid/private-id", strings.NewReader("payload"))
	_, err := transport.RoundTrip(r)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	metric := m.upstreamMetric("asset", 0)
	if metric.timeouts.Load() != 1 || metric.errors.Load() != 1 || metric.declaredSentBytes.Load() != 7 || metricCount(&metric.durationMetric) != 1 {
		t.Fatal("timeout/bytes accounting failed")
	}
}

func TestDiagnosticsRejectPublicListeners(t *testing.T) {
	for _, address := range []string{"0.0.0.0:6060", ":6060", "[::]:6060", "192.0.2.1:6060", "localhost:6060", "bad"} {
		if validatePprofAddress(address) == nil {
			t.Fatalf("public/ambiguous listener accepted: %s", address)
		}
	}
	for _, address := range []string{"127.0.0.1:6060", "[::1]:6060"} {
		if err := validatePprofAddress(address); err != nil {
			t.Fatal(err)
		}
	}
}
