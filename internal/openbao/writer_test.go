package openbao

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriterKVv2CASIdempotenceAndUpdates(t *testing.T) {
	var mu sync.Mutex
	fields := map[string]string{}
	version := 0
	writes := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/v1/kv/data/projects/myapi/env/api" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("X-Vault-Token") != "test-token" {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if version == 0 { http.NotFound(w, r); return }
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"data": fields,
					"metadata": map[string]int{"version": version},
				},
			})
		case http.MethodPost:
			var body struct {
				Data map[string]string `json:"data"`
				Options struct { CAS int `json:"cas"` } `json:"options"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			if body.Options.CAS != version {
				http.Error(w, "CAS conflict", http.StatusBadRequest)
				return
			}
			fields = body.Data
			writes++
			version++
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	setWriterTestEnvironment(t, server)

	writer, err := NewWriter()
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	imported := map[string]string{"DATABASE_PASSWORD": "redacted-example", "JWT_SECRET": "another-example"}
	changed, err := writer.PutFields(ctx, "myapi", "kv", "env/api", imported, false)
	if err != nil || !changed { t.Fatalf("first import: %v %t", err, changed) }
	changed, err = writer.PutFields(ctx, "myapi", "kv", "env/api", imported, false)
	if err != nil || changed { t.Fatalf("idempotent import: %v %t", err, changed) }
	mu.Lock()
	firstWrites, firstVersion := writes, version
	mu.Unlock()
	if firstWrites != 1 || firstVersion != 1 { t.Fatalf("duplicate write: %d, version %d", firstWrites, firstVersion) }

	mu.Lock()
	fields["UNRELATED"] = "preserve"
	mu.Unlock()
	updated := map[string]string{"DATABASE_PASSWORD": "different", "JWT_SECRET": "another-example"}
	if _, err := writer.PutFields(ctx, "myapi", "kv", "env/api", updated, false); err == nil {
		t.Fatal("updated an existing value without explicit authorization")
	}
	changed, err = writer.PutFields(ctx, "myapi", "kv", "env/api", updated, true)
	if err != nil || !changed { t.Fatalf("CAS update: %v %t", err, changed) }
	mu.Lock()
	defer mu.Unlock()
	if fields["UNRELATED"] != "preserve" || writes != 2 || version != 2 {
		t.Fatalf("existing fields were lost or versioning failed")
	}
}

func TestWriterRejectsInsecureConnectionAndNoSession(t *testing.T) {
	t.Setenv("BAO_ADDR", "http://localhost:8200")
	t.Setenv("BAO_TOKEN", "session")
	if _, err := NewWriter(); err == nil { t.Fatal("accepted HTTP") }
	t.Setenv("BAO_ADDR", "https://localhost:8200")
	t.Setenv("BAO_TOKEN", "")
	t.Setenv("BAO_TOKEN_PATH", filepath.Join(t.TempDir(), "missing"))
	if _, err := NewWriter(); err == nil { t.Fatal("accepted absent authentication") }
}

func TestWriterNeverLeaksValuesInErrors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("sensitive-server-response"))
	}))
	defer server.Close()
	setWriterTestEnvironment(t, server)
	writer, err := NewWriter()
	if err != nil { t.Fatal(err) }
	_, err = writer.PutFields(context.Background(), "myapi", "kv", "env/api",
		map[string]string{"JWT_SECRET": "sensitive-client-value"}, false)
	if err == nil || strings.Contains(err.Error(), "sensitive-") {
		t.Fatalf("sensitive data exposed or denial was accepted: %v", err)
	}
}

func setWriterTestEnvironment(t *testing.T, server *httptest.Server) {
	t.Helper()
	cert := server.Certificate()
	parsed, err := x509.ParseCertificate(cert.Raw)
	if err != nil { t.Fatal(err) }
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: parsed.Raw})
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pemBytes, 0600); err != nil { t.Fatal(err) }
	t.Setenv("BAO_ADDR", server.URL)
	t.Setenv("BAO_TOKEN", "test-token")
	t.Setenv("BAO_CACERT", path)
}
