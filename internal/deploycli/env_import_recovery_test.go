package deploycli

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jmeiracorbal/docker-blue-green/internal/config"
)

// A readable symlink lets config.Load succeed while atomicYAML refuses to
// replace the nonregular contract after a successful remote KV write.
func TestImportRecoversAfterContractSaveFailure(t *testing.T) {
	root := prepareImportFixture(t)
	envFile := filepath.Join(root, ".env.local")
	const secretValue = "private-example"
	if err := os.WriteFile(envFile, []byte("DATABASE_PASSWORD="+secretValue+"\nJWT_SECRET=second-private-example\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	stored := map[string]string{}
	version, writes := 0, 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.URL.Path != "/v1/kv/data/projects/myapi/env/api" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Vault-Token") != "test-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if version == 0 {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"data": stored, "metadata": map[string]int{"version": version}},
			})
		case http.MethodPost:
			var payload struct {
				Data    map[string]string `json:"data"`
				Options struct {
					CAS int `json:"cas"`
				} `json:"options"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Options.CAS != version {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			stored = payload.Data
			writes++
			version++
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	ca := filepath.Join(t.TempDir(), "ca.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(ca, cert, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BAO_ADDR", server.URL)
	t.Setenv("BAO_TOKEN", "test-token")
	t.Setenv("BAO_CACERT", ca)

	contract := config.Path(root)
	backing := filepath.Join(root, "deploy", ".original-bluegreen.yml")
	if err := os.Rename(contract, backing); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(backing, contract); err != nil {
		t.Fatal(err)
	}

	args := []string{"--dir", root, "--service", "api", "--mount", "kv", "--env-file", envFile}
	var output bytes.Buffer
	err := importSecrets(args, strings.NewReader("yes\n"), &output)
	if err == nil || !strings.Contains(err.Error(), "local contract could not be saved") {
		t.Fatalf("expected a partial-persistence error, got: %v", err)
	}
	if strings.Contains(err.Error(), secretValue) || strings.Contains(output.String(), secretValue) {
		t.Fatal("partial-persistence error exposed a secret value")
	}

	cfg, err := config.Load(root)
	if err != nil || cfg.OpenBao != nil {
		t.Fatalf("failed save unexpectedly changed the YAML contract: %v", err)
	}
	mu.Lock()
	if version != 1 || writes != 1 || stored["DATABASE_PASSWORD"] != secretValue ||
		stored["JWT_SECRET"] != "second-private-example" {
		mu.Unlock()
		t.Fatal("OpenBao did not retain the write after the local YAML save failed")
	}
	mu.Unlock()

	if err := os.Remove(contract); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backing, contract); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		output.Reset()
		if err := importSecrets(args, strings.NewReader("yes\n"), &output); err != nil {
			t.Fatalf("recovery attempt %d failed: %v", i+1, err)
		}
		if strings.Contains(output.String(), secretValue) {
			t.Fatal("recovery output exposed a secret value")
		}
	}

	cfg, err = config.Load(root)
	if err != nil || cfg.OpenBao == nil || len(cfg.OpenBao.Secrets) != 2 {
		t.Fatalf("recovery did not persist exactly two references: %v", err)
	}
	if cfg.OpenBao.Secrets[0].Field != "DATABASE_PASSWORD" ||
		cfg.OpenBao.Secrets[0].Name != "database_password" ||
		cfg.OpenBao.Secrets[1].Field != "JWT_SECRET" {
		t.Fatal("recovered references differ from the original dotenv")
	}
	mu.Lock()
	defer mu.Unlock()
	if version != 1 || writes != 1 || stored["DATABASE_PASSWORD"] != secretValue {
		t.Fatal("retries created another KV version or lost the imported data")
	}
}
