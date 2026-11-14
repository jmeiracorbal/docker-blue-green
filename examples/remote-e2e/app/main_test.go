package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionIncludesImmutableImageVersionAndSlot(t *testing.T) {
	t.Setenv("DEPLOYMENT_SLOT", "green")
	response := httptest.NewRecorder()
	routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/version", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d", response.Code)
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["version"] != strings.TrimSpace(imageVersion) || payload["slot"] != "green" {
		t.Fatalf("unexpected image identity: %v", payload)
	}
}

func TestHealthFailureIsExplicit(t *testing.T) {
	t.Setenv("E2E_FAIL_HEALTH", "1")
	response := httptest.NewRecorder()
	routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected unhealthy service, got HTTP %d", response.Code)
	}
}

func TestLifecycleHooksAreDeterministic(t *testing.T) {
	for _, tc := range []struct{ command, failureFlag string }{
		{"before-start", "E2E_FAIL_BEFORE_START"},
		{"before-switch", "E2E_FAIL_BEFORE_SWITCH"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			var output bytes.Buffer
			if err := run([]string{tc.command}, &output); err != nil || output.String() != "ok\n" {
				t.Fatalf("successful hook: %q, %v", output.String(), err)
			}
			t.Setenv(tc.failureFlag, "1")
			if err := run([]string{tc.command}, &output); err == nil || !strings.Contains(err.Error(), "injected") {
				t.Fatalf("expected injected failure, got %v", err)
			}
		})
	}
}
