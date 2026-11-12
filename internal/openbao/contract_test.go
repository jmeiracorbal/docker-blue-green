package openbao

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const example = `{"mount":"kv","secrets":[{"service":"api","name":"database_password","path":"database","field":"password"}]}`

func TestParseManifest(t *testing.T) {
	m, err := Parse([]byte(example), "myapp")
	if err != nil || len(m.Secrets) != 1 {
		t.Fatalf("unexpected parse: %#v, %v", m, err)
	}
}

func TestRejectedReferences(t *testing.T) {
	cases := []string{
		`{"mount":"kv","secrets":[{"service":"api","name":"pw","path":"../other","field":"password"}]}`,
		`{"mount":"kv","secrets":[{"service":"api","name":"pw","path":"database?version=1","field":"password"}]}`,
		`{"mount":"kv","secrets":[{"service":"api","name":"pw","path":"database","field":"password"},{"service":"api","name":"pw","path":"database","field":"password"}]}`,
		`{"mount":"kv","secrets":[]}`,
		`{"mount":"kv","secrets":[{"service":"api","name":"pw","path":"database","field":"password","value":"never-store-secret-here"}]}`,
	}
	for _, c := range cases {
		if _, err := Parse([]byte(c), "myapp"); err == nil {
			t.Errorf("accepted invalid contract: %s", c)
		}
	}
	if _, err := Parse([]byte(example), "../other"); err == nil {
		t.Fatal("accepted invalid project")
	}
	if _, err := Parse([]byte(example+`{}`), "myapp"); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}

func socketServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "proxy.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	t.Cleanup(func() { _ = server.Close(); _ = listener.Close() })
	go func() { _ = server.Serve(listener) }()
	return socket
}

func TestCheckUsesScopedReadOnlyAPI(t *testing.T) {
	secret := "never-print-this-secret"
	socket := socketServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/kv/data/projects/myapp/database" || r.Header.Get("X-Vault-Token") != "" {
			t.Errorf("unexpected OpenBao API request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"data":     map[string]string{"password": secret},
			"metadata": map[string]int{"version": 3},
		}})
	})
	client, err := newClientAtSocket(socket)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Parse([]byte(example), "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Check(context.Background(), "myapp", manifest); err != nil {
		t.Fatal(err)
	}
}

func TestErrorsDoNotRevealSecretData(t *testing.T) {
	leaked := "secret-value-from-proxy"
	socket := socketServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(leaked))
	})
	client, err := newClientAtSocket(socket)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Parse([]byte(example), "myapp")
	if err != nil {
		t.Fatal(err)
	}
	checkErr := client.Check(context.Background(), "myapp", manifest)
	if checkErr == nil || strings.Contains(checkErr.Error(), leaked) {
		t.Fatalf("unsafe error: %v", checkErr)
	}
}

func TestMissingFieldRejected(t *testing.T) {
	socket := socketServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"data":{"different":"secret"},"metadata":{"version":2}}}`))
	})
	client, err := newClientAtSocket(socket)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Parse([]byte(example), "myapp")
	if err != nil {
		t.Fatal(err)
	}
	err = client.Check(context.Background(), "myapp", manifest)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing field accepted: %v", err)
	}
}

func TestSocketMustBeUnix(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newClientAtSocket(file); err == nil {
		t.Fatal("accepted non-socket")
	}
	if _, err := newClientAtSocket("relative.sock"); err == nil {
		t.Fatal("accepted relative socket")
	}
}

func TestProxySocketPath(t *testing.T) {
    got, err := ProxySocketPath("myapp")
    if err != nil {
        t.Fatal(err)
    }
    want := "/run/bluegreen/myapp/openbao.sock"
    if got != want {
        t.Fatalf("socket path = %q, want %q", got, want)
    }
    for _, invalid := range []string{"", "../other", "MyApp", "myapp/other", "myapp.sock"} {
        if _, err := ProxySocketPath(invalid); err == nil {
            t.Errorf("accepted invalid project %q", invalid)
        }
    }
}
