package deploycli

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This test uses a disposable, genuine OpenBao TLS dev server. It never
// connects to a production server or relies on an existing OpenBao session.
func TestRealOpenBaoDotenvImport(t *testing.T) {
	if os.Getenv("BLUEGREEN_REAL_OPENBAO") != "1" {
		t.Skip("real OpenBao integration is opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	root := t.TempDir()
	certDir := filepath.Join(root, "certs")
	if err := os.MkdirAll(certDir, 0700); err != nil { t.Fatal(err) }
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	port := portListener.Addr().(*net.TCPAddr).Port
	portListener.Close()
	address := fmt.Sprintf("https://127.0.0.1:%d", port)
	const token = "ci-local-only-openbao-token"
	container := fmt.Sprintf("bg-env-import-%d", os.Getpid())
	run := func(env []string, name string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = append(os.Environ(), env...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("%s failed: %w", name, err)
		}
		return strings.TrimSpace(string(output)), nil
	}
	must := func(env []string, name string, args ...string) string {
		t.Helper()
		value, err := run(env, name, args...)
		if err != nil { t.Fatal(err) }
		return value
	}
	must(nil, "docker", "pull", "ghcr.io/openbao/openbao:2.7.1")
	must(nil, "docker", "run", "--detach", "--rm",
		"--name", container, "--network", "host",
		"-e", "BAO_DEV_ROOT_TOKEN_ID="+token,
		"ghcr.io/openbao/openbao:2.7.1", "server",
		"-dev-tls", "-dev-no-store-token",
		"-dev-tls-cert-dir=/tmp/bluegreen-tls",
		"-dev-listen-address=127.0.0.1:"+fmt.Sprint(port))
	t.Cleanup(func() {
		stop, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		_, _ = exec.CommandContext(stop, "docker", "stop", container).CombinedOutput()
	})
	bao := filepath.Join(root, "bao")
	must(nil, "docker", "cp", container+":/usr/bin/bao", bao)
	if err := os.Chmod(bao, 0700); err != nil { t.Fatal(err) }
	var caPath, lastTLSStatus, lastCopyError string
	ready := false
	for i := 0; i < 60; i++ {
		if _, copyErr := run(nil, "docker", "cp", container+":/tmp/bluegreen-tls/.", certDir); copyErr != nil {
			lastCopyError = copyErr.Error()
		}
		caPath = findGeneratedOpenBaoCA(certDir)
		if caPath != "" {
			adminEnv := []string{"BAO_ADDR="+address, "BAO_TOKEN="+token, "BAO_CACERT="+caPath}
			statusCmd := exec.CommandContext(ctx, bao, "status")
			statusCmd.Env = append(os.Environ(), adminEnv...)
			output, statusErr := statusCmd.CombinedOutput()
			if statusErr == nil {
				ready = true
				break
			}
			lastTLSStatus = strings.ReplaceAll(string(output), token, "[redacted]")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		state, _ := run(nil, "docker", "inspect", "--format", "{{.State.Status}}/{{.State.ExitCode}}", container)
		t.Fatalf("OpenBao TLS test endpoint unavailable: container=%s, CA_found=%t, copy=%s, TLS_status=%s",
			state, caPath != "", lastCopyError, strings.TrimSpace(lastTLSStatus))
	}
	adminEnv := []string{"BAO_ADDR="+address, "BAO_TOKEN="+token, "BAO_CACERT="+caPath}
	must(adminEnv, bao, "secrets", "enable", "-path=kv", "-version=2", "kv")

	policyPath := filepath.Join(root, "writer.hcl")
	policy := `path "kv/data/projects/myapi/*" {
  capabilities = ["create", "read", "update"]
}`
	if err := os.WriteFile(policyPath, []byte(policy), 0600); err != nil { t.Fatal(err) }
	must(adminEnv, bao, "policy", "write", "import-writer", policyPath)
	limitedToken := must(adminEnv, bao, "token", "create", "-policy=import-writer", "-field=token")

	project := prepareImportFixture(t)
	envFile := filepath.Join(project, "selected.env")
	if err := os.WriteFile(envFile, []byte("DATABASE_PASSWORD=import-test-one\n"), 0600); err != nil { t.Fatal(err) }
	t.Setenv("BAO_ADDR", address)
	t.Setenv("BAO_CACERT", caPath)
	t.Setenv("BAO_TOKEN", limitedToken)
	args := []string{"--dir", project, "--service", "api", "--mount", "kv", "--env-file", envFile}
	var out bytes.Buffer
	for i := 0; i < 2; i++ {
		out.Reset()
		if err := importSecrets(args, strings.NewReader("yes\n"), &out); err != nil { t.Fatal(err) }
		if strings.Contains(out.String(), "import-test-one") { t.Fatal("CLI output exposed an imported value") }
	}
	secret := "projects/myapi/env/api"
	metadata := func() int {
		t.Helper()
		raw := must(adminEnv, bao, "kv", "get", "-format=json", "-mount=kv", secret)
		var response struct {
			Data struct {
				Metadata struct { Version int `json:"version"` } `json:"metadata"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(raw), &response); err != nil { t.Fatal(err) }
		return response.Data.Metadata.Version
	}
	if version := metadata(); version != 1 { t.Fatalf("idempotent import created %d KV versions", version) }
	if err := os.WriteFile(envFile, []byte("DATABASE_PASSWORD=import-test-two\n"), 0600); err != nil { t.Fatal(err) }
	if err := importSecrets(args, strings.NewReader("yes\n"), &out); err == nil {
		t.Fatal("accepted overwrite without --update")
	}
	if err := importSecrets(append(append([]string{}, args...), "--update"),
		strings.NewReader("yes\n"), &out); err != nil { t.Fatal(err) }
	if version := metadata(); version != 2 { t.Fatalf("update failed to create a new KV v2 version: %d", version) }

	readPolicyPath := filepath.Join(root, "reader.hcl")
	readPolicy := `path "kv/data/projects/myapi/*" {
  capabilities = ["read"]
}`
	if err := os.WriteFile(readPolicyPath, []byte(readPolicy), 0600); err != nil { t.Fatal(err) }
	must(adminEnv, bao, "policy", "write", "import-reader", readPolicyPath)
	readerToken := must(adminEnv, bao, "token", "create", "-policy=import-reader", "-field=token")
	readerEnv := []string{"BAO_ADDR="+address, "BAO_TOKEN="+readerToken, "BAO_CACERT="+caPath}
	if got := must(readerEnv, bao, "kv", "get", "-field=DATABASE_PASSWORD", "-mount=kv", secret); got != "import-test-two" {
		t.Fatal("project read-only identity could not retrieve the imported field")
	}
	if _, err := run(readerEnv, bao, "kv", "put", "-mount=kv", secret, "DATABASE_PASSWORD=should-not-write"); err == nil {
		t.Fatal("read-only identity was granted write access")
	}
}

func findGeneratedOpenBaoCA(root string) string {
	var result string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() { return nil }
		data, err := os.ReadFile(path)
		if err != nil { return nil }
		for len(data) > 0 {
			block, remaining := pem.Decode(data)
			if block == nil { break }
			data = remaining
			if block.Type != "CERTIFICATE" { continue }
			cert, err := x509.ParseCertificate(block.Bytes)
			if err == nil && cert.IsCA { result = path; return errors.New("found") }
		}
		return nil
	})
	return result
}
