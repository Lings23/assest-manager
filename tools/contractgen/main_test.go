package main

import (
	"strings"
	"testing"
)

func TestGenerateRoutes(t *testing.T) {
	output, err := generate([]byte("paths:\n  /api/v1/auth/login:\n    post: {}\n  /health/live:\n    get: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(output)
	for _, expected := range []string{`"/api/v1/auth/login"`, `"/health/live"`, "OpenAPISHA256"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("generated output missing %s: %s", expected, text)
		}
	}
}
