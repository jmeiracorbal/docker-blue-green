package deploycli

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretCheckCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "deploy"), 0700); err != nil {
		t.Fatal(err)
	}
	data := `{"mount":"kv","secrets":[{"service":"api","name":"database_password","path":"database","field":"password"}]}`
	if err := os.WriteFile(filepath.Join(root, "deploy", "secrets.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "proxy.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/kv/data/projects/myapp/database" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"data":{"password":"secret-example"},"metadata":{"version":1}}}`))
	})}
	t.Cleanup(func() { _ = srv.Close(); _ = listener.Close() })
	go func() { _ = srv.Serve(listener) }()
	var out bytes.Buffer
	err = checkSecrets([]string{"check", "--dir", root, "--project", "myapp", "--socket", socket}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 OpenBao secret references verified") || strings.Contains(out.String(), "secret-example") {
		t.Fatalf("unsafe output: %s", out.String())
	}
}

func TestSecretCheckRequiresExplicitSocket(t *testing.T) {
	if err := checkSecrets([]string{"check", "--project", "myapp"}, &bytes.Buffer{}); err == nil {
		t.Fatal("accepted implicit proxy endpoint")
	}
}
