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

func TestDotenvParserPreservesValuesAndRejectsConflicts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "secrets.env")
	content := "# comment\nDATABASE_PASSWORD='sp ace # protected'\nJWT_SECRET=\"two\\nlines\"\nDOLLARS=abc$123\nHASH=value#literal\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil { t.Fatal(err) }
	got, names, err := readDotenv(path)
	if err != nil { t.Fatal(err) }
	if len(names) != 4 || got["DATABASE_PASSWORD"] != "sp ace # protected" ||
		got["JWT_SECRET"] != "two\nlines" || got["DOLLARS"] != "abc$123" ||
		got["HASH"] != "value#literal" {
		t.Fatalf("dotenv values were not preserved: names=%v", names)
	}
	bad := []string{
		"A=value\nA=duplicate\n",
		"A=value\na=collision\n",
		"A=not-empty\nB=\n",
		"A=${EXPANSION}\n",
		"A=$(command)\n",
		"A=\x60command\x60\n",
		"A=\"unterminated\n",
		"A=\"quoted\" and trailing text\n",
		"INVALID-KEY=x\n",
	}
	for _, content := range bad {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil { t.Fatal(err) }
		if _, _, err := readDotenv(path); err == nil {
			t.Fatal("accepted malformed or ambiguous dotenv content")
		}
	}
}

func TestImportCancellationNeverTouchesContract(t *testing.T) {
	root := prepareImportFixture(t)
	envFile := filepath.Join(root, ".env.local")
	if err := os.WriteFile(envFile, []byte("DATABASE_PASSWORD=private-example\n"), 0600); err != nil { t.Fatal(err) }
	var out bytes.Buffer
	err := importSecrets([]string{"--dir", root, "--service", "api", "--mount", "kv", "--env-file", envFile},
		strings.NewReader("no\n"), &out)
	if err == nil || !strings.Contains(err.Error(), "cancelled") { t.Fatalf("confirmation was not required: %v", err) }
	if strings.Contains(out.String(), "private-example") { t.Fatal("import preview exposed a secret value") }
	cfg, err := config.Load(root)
	if err != nil || cfg.OpenBao != nil { t.Fatalf("cancelled import modified contract: %v", err) }
}

func TestImportWritesOnceAndReconcilesReferences(t *testing.T) {
	root := prepareImportFixture(t)
	envFile := filepath.Join(root, ".env.local")
	if err := os.WriteFile(envFile, []byte("DATABASE_PASSWORD=private-example\nJWT_SECRET=other-private-example\n"), 0600); err != nil { t.Fatal(err) }
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
		if r.Method == http.MethodGet {
			if version == 0 { http.NotFound(w, r); return }
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"data": stored, "metadata": map[string]int{"version": version}},
			})
			return
		}
		var payload struct {
			Data map[string]string `json:"data"`
			Options struct { CAS int `json:"cas"` } `json:"options"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&payload) != nil ||
			payload.Options.CAS != version {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		stored = payload.Data
		writes++
		version++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	pemData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pemData, 0600); err != nil { t.Fatal(err) }
	t.Setenv("BAO_ADDR", server.URL)
	t.Setenv("BAO_TOKEN", "test-token")
	t.Setenv("BAO_CACERT", ca)
	args := []string{"--dir", root, "--service", "api", "--mount", "kv", "--env-file", envFile}
	var out bytes.Buffer
	for i := 0; i < 2; i++ {
		out.Reset()
		if err := importSecrets(args, strings.NewReader("yes\n"), &out); err != nil { t.Fatal(err) }
		if strings.Contains(out.String(), "private-example") { t.Fatal("secret appeared in output") }
	}
	cfg, err := config.Load(root)
	if err != nil || cfg.OpenBao == nil || len(cfg.OpenBao.Secrets) != 2 {
		t.Fatalf("references were not reconciled: %v", err)
	}
	if cfg.OpenBao.Secrets[0].Field != "DATABASE_PASSWORD" ||
		cfg.OpenBao.Secrets[0].Name != "database_password" ||
		cfg.OpenBao.Secrets[0].Path != "env/api" {
		t.Fatalf("reference mapping incorrect: %+v", cfg.OpenBao.Secrets[0])
	}
	mu.Lock()
	defer mu.Unlock()
	if version != 1 || writes != 1 || stored["DATABASE_PASSWORD"] != "private-example" {
		t.Fatal("duplicate versions were created or imported data was lost")
	}
}

func prepareImportFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	prepareProject(t, root, "api")
	cfg := config.Document{Version: 1, Project: "myapi"}
	if err := config.Save(root, cfg); err != nil { t.Fatal(err) }
	return root
}
