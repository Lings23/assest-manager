package taskcenter

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type persistentImportReadError struct {
	err   error
	calls int
}

func (r *persistentImportReadError) Read([]byte) (int, error) {
	r.calls++
	if r.calls > 8 {
		// Bound a broken parser so a regression fails instead of hanging tests.
		return 0, io.EOF
	}
	return 0, r.err
}

func TestPreflightTransportErrorsRemainRetryableAndStopReading(t *testing.T) {
	for _, prefix := range []string{"", "department_name\nValid\n", "department_name\n\"partial quoted row"} {
		t.Run(prefix, func(t *testing.T) {
			cause := errors.New("object stream connection reset")
			broken := &persistentImportReadError{err: cause}
			_, err := PreflightCSV("responsible-department", io.MultiReader(strings.NewReader(prefix), broken), true)
			if !errors.Is(err, cause) || IsPermanentTaskError(err) || broken.calls != 1 {
				t.Fatalf("stream error misclassified or read repeatedly: error=%v permanent=%v calls=%d", err, IsPermanentTaskError(err), broken.calls)
			}
		})
	}
}

func TestPreflightImmutableHeaderErrorsArePermanent(t *testing.T) {
	for _, test := range []struct{ name, assetType, source string }{
		{"unknown schema", "not-a-real-type", "department_name\nValid\n"},
		{"empty source", "responsible-department", ""},
		{"malformed header", "responsible-department", "\"department_name\n"},
		{"unknown column", "responsible-department", "unknown_column\nValue\n"},
		{"duplicate column", "responsible-department", "department_name,department_name\nA,B\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := PreflightCSV(test.assetType, strings.NewReader(test.source), true)
			if err == nil || !IsPermanentTaskError(err) {
				t.Fatalf("immutable header error was not permanent: %v", err)
			}
		})
	}
}

func TestPreflightContinuesAfterCSVParseErrorForErrorReport(t *testing.T) {
	result, err := PreflightCSV("responsible-department", strings.NewReader("department_name\nBad\"quote\nValid\n"), true)
	if err != nil || len(result.Errors) != 1 || result.Valid != 1 || len(result.Rows) != 1 || result.Rows[0]["department_name"] != "Valid" {
		t.Fatalf("CSV parse error did not preserve report and subsequent rows: result=%+v error=%v", result, err)
	}
}

type streamingFailureObjects struct {
	workerObjects
	source io.Reader
}

func (o *streamingFailureObjects) Get(context.Context, string) (io.ReadCloser, string, error) {
	return io.NopCloser(o.source), "text/csv", nil
}

func TestWorkerImportStreamFailureRetriesInsteadOfPermanentDeath(t *testing.T) {
	store := newWorkerMemoryStore()
	store.task.Kind = "import"
	store.task.Input = map[string]any{"source_object_key": "imports/test.csv"}
	broken := &persistentImportReadError{err: errors.New("object stream connection reset")}
	worker := testWorker(store, workerAssets{})
	worker.objects = &streamingFailureObjects{source: io.MultiReader(strings.NewReader("department_name\nValid\n"), broken)}
	action := worker.process(t.Context(), workerDelivery("import", 1))
	if !action.ack || store.retries.Load() != 1 || store.terminated.Load() != 0 || store.completes.Load() != 0 || broken.calls != 1 {
		t.Fatalf("stream failure permanently terminated task: action=%+v retry=%d dead=%d reads=%d", action, store.retries.Load(), store.terminated.Load(), broken.calls)
	}
}
