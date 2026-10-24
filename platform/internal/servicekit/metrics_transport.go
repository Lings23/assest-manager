package servicekit

import (
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type observedTransport struct {
	base      http.RoundTripper
	metrics   *Metrics
	component string
}

// ObservedTransport preserves the supplied transport and observes through body
// EOF/Close, including streamed downloads. It never changes connection budgets.
func ObservedTransport(base http.RoundTripper, metrics *Metrics, component string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if metrics == nil {
		return base
	}
	return &observedTransport{base: base, metrics: metrics, component: boundedComponent(component)}
}
func (t *observedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if id := RequestID(request.Context()); id != "" && request.Header.Get("X-Request-ID") == "" {
		request = request.Clone(request.Context())
		request.Header.Set("X-Request-ID", id)
	}
	started := time.Now()
	response, err := t.base.RoundTrip(request)
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	metric := t.metrics.upstreamMetric(t.component, status)
	if request.ContentLength > 0 {
		metric.declaredSentBytes.Add(uint64(request.ContentLength))
	}
	var once sync.Once
	finish := func(readErr error) {
		once.Do(func() {
			metric.observe(time.Since(started), err != nil || readErr != nil || status >= 400, isTimeout(err) || isTimeout(readErr))
		})
	}
	if err != nil {
		finish(nil)
		return response, err
	}
	if response.Body == nil || response.Body == http.NoBody {
		finish(nil)
		return response, nil
	}
	response.Body = &observedBody{ReadCloser: response.Body, bytes: &metric.receivedBytes, finish: finish}
	return response, nil
}

type observedBody struct {
	io.ReadCloser
	bytes  *atomic.Uint64
	finish func(error)
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.bytes.Add(uint64(n))
	}
	if err == io.EOF {
		b.finish(nil)
	} else if err != nil {
		b.finish(err)
	}
	return n, err
}
func (b *observedBody) Close() error { err := b.ReadCloser.Close(); b.finish(err); return err }
