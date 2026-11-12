package deploycli

import (
    "bytes"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestSecretCheckUsesManagedProxyEndpoint(t *testing.T) {
    root := t.TempDir()
    if err := os.MkdirAll(filepath.Join(root, "deploy"), 0700); err != nil {
        t.Fatal(err)
    }
    doc := `{"mount":"kv","secrets":[{"service":"api","name":"database_password","path":"database","field":"password"}]}`
    if err := os.WriteFile(filepath.Join(root, "deploy", "secrets.json"), []byte(doc), 0600); err != nil {
        t.Fatal(err)
    }
    var output bytes.Buffer
    const project = "bluegreenclitest001"
    err := checkSecrets([]string{"check", "--dir", root, "--project", project}, &output)
    if err == nil || !strings.Contains(err.Error(), "access OpenBao Proxy socket") {
        t.Fatalf("expected unprovisioned proxy socket error, got %v", err)
    }
    if output.Len() != 0 {
        t.Fatalf("unexpected secret check output: %s", output.String())
    }
}

func TestSecretCheckRejectsSocketFlag(t *testing.T) {
    err := checkSecrets([]string{"check", "--project", "myapp", "--socket", "/tmp/proxy.sock"}, &bytes.Buffer{})
    if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
        t.Fatalf("socket override should be rejected: %v", err)
    }
}
