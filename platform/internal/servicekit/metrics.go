package servicekit

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var durationBounds = [...]time.Duration{time.Millisecond, 2500 * time.Microsecond, 5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2500 * time.Millisecond, 5 * time.Second, 10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 20 * time.Minute}

type durationMetric struct {
	buckets  [len(durationBounds) + 1]atomic.Uint64
	nanos    atomic.Uint64
	errors   atomic.Uint64
	timeouts atomic.Uint64
}

func (h *durationMetric) observe(elapsed time.Duration, failed, timedOut bool) {
	i := len(durationBounds)
	for n, bound := range durationBounds {
		if elapsed <= bound {
			i = n
			break
		}
	}
	h.buckets[i].Add(1)
	h.nanos.Add(uint64(max(elapsed, 0)))
	if failed {
		h.errors.Add(1)
	}
	if timedOut {
		h.timeouts.Add(1)
	}
}
func (h *durationMetric) write(w io.Writer, name, labels string) {
	var count uint64
	for i := range h.buckets {
		count += h.buckets[i].Load()
		bound := "+Inf"
		if i < len(durationBounds) {
			bound = strconv.FormatFloat(durationBounds[i].Seconds(), 'g', -1, 64)
		}
		fmt.Fprintf(w, "%s_bucket{%s,le=%q} %d\n", name, labels, bound, count)
	}
	fmt.Fprintf(w, "%s_sum{%s} %g\n%s_count{%s} %d\n", name, labels, float64(h.nanos.Load())/float64(time.Second), name, labels, count)
}

type httpRouteKey struct{ route, method string }
type httpStatusKey struct {
	httpRouteKey
	status int
}
type upstreamKey struct {
	component string
	status    int
}
type dbKey struct{ pool, route, operation string }
type upstreamMetric struct {
	durationMetric
	receivedBytes     atomic.Uint64
	declaredSentBytes atomic.Uint64
}

// Metrics uses only application-controlled route patterns and bounded labels.
// IDs, tokens, raw paths, SQL statements and query fingerprints are never labels.
type Metrics struct {
	service  string
	mu       sync.RWMutex
	http     map[httpStatusKey]*durationMetric
	inflight map[httpRouteKey]*atomic.Int64
	upstream map[upstreamKey]*upstreamMetric
	database map[dbKey]*durationMetric
	stages   map[string]*durationMetric
	pools    map[string]func() PoolSnapshot
}

var serviceMetrics sync.Map

func MetricsFor(service string) *Metrics {
	if actual, ok := serviceMetrics.Load(service); ok {
		return actual.(*Metrics)
	}
	metric := &Metrics{service: service, http: map[httpStatusKey]*durationMetric{}, inflight: map[httpRouteKey]*atomic.Int64{}, upstream: map[upstreamKey]*upstreamMetric{}, database: map[dbKey]*durationMetric{}, stages: map[string]*durationMetric{}, pools: map[string]func() PoolSnapshot{}}
	actual, _ := serviceMetrics.LoadOrStore(service, metric)
	return actual.(*Metrics)
}

type metricRouteContextKey struct{}
type metricOperationContextKey struct{}

func normalizedMethod(method string) string {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		return method
	default:
		return "OTHER"
	}
}
func boundedStatus(status int) int {
	if status < 100 || status > 599 {
		return 0
	}
	return status
}
func metricRoute(ctx context.Context) string {
	if route, ok := ctx.Value(metricRouteContextKey{}).(string); ok {
		return route
	}
	return "background"
}

// WithDatabaseOperation accepts this finite list only; external values collapse.
func WithDatabaseOperation(ctx context.Context, operation string) context.Context {
	switch operation {
	case "asset_list", "asset_search", "asset_deep", "asset_count", "asset_detail", "asset_create", "asset_update", "asset_delete", "asset_versions", "asset_batch", "task_record_rows", "task_mark_progress", "task_final_audit":
	default:
		operation = "other"
	}
	return context.WithValue(ctx, metricOperationContextKey{}, operation)
}

type metricResponse struct {
	http.ResponseWriter
	status   int
	writeErr error
}

func (w *metricResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *metricResponse) WriteHeader(status int) {
	// Informational responses do not commit the final status.
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *metricResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		w.writeErr = err
	}
	return n, err
}

func (m *Metrics) InstrumentHTTP(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Scraping must not change the business request-rate denominator.
		if r.URL.Path == "/metrics/service" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		_, pattern := mux.Handler(r)
		if pattern == "" {
			pattern = "unmatched"
		}
		key := httpRouteKey{route: pattern, method: normalizedMethod(r.Method)}
		m.mu.Lock()
		inflight := m.inflight[key]
		if inflight == nil {
			inflight = &atomic.Int64{}
			m.inflight[key] = inflight
		}
		m.mu.Unlock()
		inflight.Add(1)
		started := time.Now()
		recorder := &metricResponse{ResponseWriter: w}
		defer func() {
			inflight.Add(-1)
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			statusKey := httpStatusKey{httpRouteKey: key, status: boundedStatus(status)}
			m.mu.Lock()
			metric := m.http[statusKey]
			if metric == nil {
				metric = &durationMetric{}
				m.http[statusKey] = metric
			}
			m.mu.Unlock()
			timedOut := errors.Is(r.Context().Err(), context.DeadlineExceeded) || isTimeout(recorder.writeErr) || status == 408 || status == 504
			metric.observe(time.Since(started), status >= 500 || recorder.writeErr != nil, timedOut)
		}()
		next.ServeHTTP(recorder, r.WithContext(context.WithValue(r.Context(), metricRouteContextKey{}, pattern)))
	})
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var network net.Error
	return errors.As(err, &network) && network.Timeout()
}
func boundedComponent(name string) string {
	switch name {
	case "iam_authorize", "asset", "object_store", "proxy_iam", "proxy_asset", "proxy_tasks", "proxy_governance", "primary":
		return name
	default:
		return "other"
	}
}
func (m *Metrics) upstreamMetric(component string, status int) *upstreamMetric {
	key := upstreamKey{component: boundedComponent(component), status: boundedStatus(status)}
	m.mu.Lock()
	defer m.mu.Unlock()
	metric := m.upstream[key]
	if metric == nil {
		metric = &upstreamMetric{}
		m.upstream[key] = metric
	}
	return metric
}
func (m *Metrics) databaseMetric(key dbKey) *durationMetric {
	m.mu.Lock()
	defer m.mu.Unlock()
	metric := m.database[key]
	if metric == nil {
		metric = &durationMetric{}
		m.database[key] = metric
	}
	return metric
}
func (m *Metrics) ObserveStage(stage string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	switch stage {
	case "import_parse", "import_record_rows", "import_asset_batch", "import_mark_progress", "import_final_audit", "export_read", "export_encode", "export_put", "dispatch_wait":
	default:
		stage = "other"
	}
	m.mu.Lock()
	metric := m.stages[stage]
	if metric == nil {
		metric = &durationMetric{}
		m.stages[stage] = metric
	}
	m.mu.Unlock()
	metric.observe(duration, err != nil, isTimeout(err))
}

func (m *Metrics) Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		provided := r.Header.Get("X-Metrics-Token")
		authorized := token != "" && len(provided) == len(token) && subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
		if (ip == nil || !ip.IsLoopback()) && !authorized {
			WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "metrics credentials required")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodHead {
			m.write(w)
		}
	})
}

func (m *Metrics) write(w io.Writer) {
	type entry struct {
		key  string
		emit func()
	}
	entries := []entry{}
	service := fmt.Sprintf("service=%q", m.service)
	m.mu.RLock()
	for key, value := range m.http {
		labels := fmt.Sprintf("%s,route=%q,method=%q,status=%q", service, key.route, key.method, strconv.Itoa(key.status))
		metric := value
		entries = append(entries, entry{key: "http" + labels, emit: func() {
			metric.write(w, "service_http_duration_seconds", labels)
			var count uint64
			for i := range metric.buckets {
				count += metric.buckets[i].Load()
			}
			fmt.Fprintf(w, "service_http_requests_total{%s} %d\nservice_http_timeouts_total{%s} %d\n", labels, count, labels, metric.timeouts.Load())
		}})
	}
	for key, value := range m.inflight {
		labels := fmt.Sprintf("%s,route=%q,method=%q", service, key.route, key.method)
		metric := value
		entries = append(entries, entry{key: "inflight" + labels, emit: func() { fmt.Fprintf(w, "service_http_inflight{%s} %d\n", labels, metric.Load()) }})
	}
	for key, value := range m.upstream {
		labels := fmt.Sprintf("%s,component=%q,status=%q", service, key.component, strconv.Itoa(key.status))
		metric := value
		entries = append(entries, entry{key: "upstream" + labels, emit: func() {
			metric.write(w, "service_upstream_duration_seconds", labels)
			fmt.Fprintf(w, "service_upstream_errors_total{%s} %d\nservice_upstream_timeouts_total{%s} %d\nservice_upstream_response_body_bytes_total{%s} %d\nservice_upstream_declared_request_body_bytes_total{%s} %d\n", labels, metric.errors.Load(), labels, metric.timeouts.Load(), labels, metric.receivedBytes.Load(), labels, metric.declaredSentBytes.Load())
		}})
	}
	for key, value := range m.database {
		labels := fmt.Sprintf("%s,pool=%q,route=%q,operation=%q", service, key.pool, key.route, key.operation)
		metric := value
		entries = append(entries, entry{key: "database" + labels, emit: func() {
			metric.write(w, "service_database_duration_seconds", labels)
			fmt.Fprintf(w, "service_database_errors_total{%s} %d\n", labels, metric.errors.Load())
		}})
	}
	for stage, value := range m.stages {
		labels := fmt.Sprintf("%s,stage=%q", service, stage)
		metric := value
		entries = append(entries, entry{key: "stage" + labels, emit: func() {
			metric.write(w, "service_task_stage_duration_seconds", labels)
			fmt.Fprintf(w, "service_task_stage_errors_total{%s} %d\n", labels, metric.errors.Load())
		}})
	}
	for name, source := range m.pools {
		labels := fmt.Sprintf("%s,pool=%q", service, name)
		snapshot := source
		entries = append(entries, entry{key: "pool" + labels, emit: func() { snapshot().write(w, labels) }})
	}
	m.mu.RUnlock()
	// Headers are written once; histogram samples remain cumulative and atomic.
	fmt.Fprintln(w, "# HELP service_http_duration_seconds Handler lifetime; includes nested operations and excludes post-return server flushing.")
	fmt.Fprintln(w, "# HELP service_upstream_duration_seconds Client call lifetime through body EOF or Close, including consumer pacing; do not add to enclosing spans.")
	fmt.Fprintln(w, "# HELP service_database_duration_seconds Conn query/COPY lifetime includes row consumption and excludes pool acquisition; ACQUIRE measures acquisition and possible connection construction.")
	fmt.Fprintln(w, "# HELP service_task_stage_duration_seconds Worker stages; dispatch_wait is claim database time minus scheduled due time, excluding planned retry delay.")
	for _, name := range []string{"service_http_duration_seconds", "service_upstream_duration_seconds", "service_database_duration_seconds", "service_task_stage_duration_seconds"} {
		fmt.Fprintf(w, "# TYPE %s histogram\n", name)
	}
	for _, name := range []string{"service_http_requests_total", "service_http_timeouts_total", "service_upstream_errors_total", "service_upstream_timeouts_total", "service_upstream_response_body_bytes_total", "service_upstream_declared_request_body_bytes_total", "service_database_errors_total", "service_task_stage_errors_total", "service_db_pool_wait_total", "service_db_pool_wait_seconds_total", "service_db_pool_canceled_acquires_total", "service_db_pool_acquires_total", "service_db_pool_acquire_seconds_total", "service_go_gc_cycles_total", "service_go_gc_pause_seconds_total", "service_process_cpu_seconds_total"} {
		fmt.Fprintf(w, "# TYPE %s counter\n", name)
	}
	for _, name := range []string{"service_http_inflight", "service_db_pool_max_open_connections", "service_db_pool_open_connections", "service_db_pool_in_use_connections", "service_db_pool_idle_connections", "service_db_pool_constructing_connections", "service_go_goroutines", "service_go_heap_alloc_bytes", "service_go_heap_inuse_bytes", "service_process_cpu_available", "service_process_rss_available", "service_process_resident_memory_bytes"} {
		fmt.Fprintf(w, "# TYPE %s gauge\n", name)
	}
	fmt.Fprintln(w, "# HELP service_db_pool_wait_total Successful acquires waiting for release or construction; canceled acquires are separate.")
	fmt.Fprintln(w, "# HELP service_db_pool_wait_seconds_total Time spent in successful empty-pool waits; does not include ongoing or canceled waits.")
	fmt.Fprintln(w, "# HELP service_upstream_declared_request_body_bytes_total Positive ContentLength summed over calls; unknown lengths and transport replays are not measured wire traffic.")
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	for _, entry := range entries {
		entry.emit()
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	fmt.Fprintf(w, "service_go_goroutines{%s} %d\nservice_go_heap_alloc_bytes{%s} %d\nservice_go_heap_inuse_bytes{%s} %d\nservice_go_gc_cycles_total{%s} %d\nservice_go_gc_pause_seconds_total{%s} %g\n", service, runtime.NumGoroutine(), service, memory.HeapAlloc, service, memory.HeapInuse, service, memory.NumGC, service, float64(memory.PauseTotalNs)/float64(time.Second))
	process := readProcessStats()
	fmt.Fprintf(w, "service_process_cpu_available{%s} %d\nservice_process_rss_available{%s} %d\n", service, boolInt(process.cpuAvailable), service, boolInt(process.rssAvailable))
	if process.cpuAvailable {
		fmt.Fprintf(w, "service_process_cpu_seconds_total{%s} %g\n", service, process.cpuSeconds)
	}
	if process.rssAvailable {
		fmt.Fprintf(w, "service_process_resident_memory_bytes{%s} %d\n", service, process.rssBytes)
	}
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type processStats struct {
	cpuSeconds                 float64
	rssBytes                   int64
	cpuAvailable, rssAvailable bool
}
