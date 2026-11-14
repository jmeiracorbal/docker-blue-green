package deploy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

    "github.com/jmeiracorbal/docker-blue-green/internal/openbao"
)

// TestLiveBlueGreen runs on a Docker-enabled CI runner. It uses isolated networks,
// temporary volumes and public test images; it needs no real deployment secrets.
func TestLiveBlueGreen(t *testing.T) {
	if os.Getenv("BLUEGREEN_DOCKER_INTEGRATION") != "1" {
		t.Skip("Docker integration not requested")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "info").CombinedOutput(); err != nil {
		t.Fatalf("Docker unavailable: %s: %v", out, err)
	}
	for _, img := range []string{"nginx:1.27-alpine", "caddy:2.10-alpine"} {
		if out, err := exec.CommandContext(ctx, "docker", "pull", img).CombinedOutput(); err != nil {
			t.Fatalf("pull %s: %s: %v", img, out, err)
		}
	}
	dig, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", "nginx:1.27-alpine").Output()
	if err != nil {
		t.Fatal(err)
	}
	appImage := strings.TrimSpace(string(dig))
	if !digestPattern.MatchString(appImage) {
		t.Fatalf("expected a pinned image digest: %s", appImage)
	}
	root := t.TempDir()
	id := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(filepath.Base(root), "TestLiveBlueGreen"), "-", ""))
	if len(id) > 8 {
		id = id[:8]
	}
	name := "it" + id
	listen, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpsPort := listen.Addr().(*net.TCPAddr).Port
	listen.Close()
	listen, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpPort := listen.Addr().(*net.TCPAddr).Port
	listen.Close()
	t.Setenv("DEPLOY_NETWORK", name+"-edge")
	t.Setenv("CADDY_IMAGE", "caddy:2.10-alpine")
	t.Setenv("CADDY_ACME_EMAIL", "test@example.invalid")
	t.Setenv("HTTP_PORT", fmt.Sprint(httpPort))
	t.Setenv("HTTPS_PORT", fmt.Sprint(httpsPort))
	t.Setenv("APPLICATION_PORT", "80")
	paths := map[string]string{
		"deploy/compose.yml": `name: "${PROJECT_NAME}-${DEPLOYMENT_SLOT}"
services:
  app:
    image: "${APPLICATION_IMAGE}"
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O /dev/null http://127.0.0.1:80/"]
      interval: 2s
      retries: 15
      start_period: 5s
    networks:
      edge:
        aliases:
          - "${PROJECT_NAME}-${DEPLOYMENT_SLOT}-app"
networks:
  edge:
    external: true
    name: "${DEPLOY_NETWORK}"
`,
		"deploy/infrastructure/compose.yml": `name: "${PROJECT_NAME}-infrastructure"
services:
  caddy:
    image: "${CADDY_IMAGE}"
    environment:
      CADDY_ACME_EMAIL: "${CADDY_ACME_EMAIL}"
    ports:
      - "${HTTP_PORT}:80/tcp"
      - "${HTTPS_PORT}:443/tcp"
    volumes:
      - ./caddy/Caddyfile:/etc/caddy/Caddyfile:ro
      - ./caddy/routes:/etc/caddy/routes:ro
      - caddy_data:/data
      - caddy_config:/config
    networks: [edge]
networks:
  edge:
    name: "${DEPLOY_NETWORK}"
volumes:
  caddy_data: {}
  caddy_config: {}
`,
		"deploy/infrastructure/caddy/Caddyfile": "{\n    email {$CADDY_ACME_EMAIL}\n}\nimport /etc/caddy/routes/*.caddy\n",
	}
	for p, s := range paths {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PROJECT_NAME", name)
    configYAML:=fmt.Sprintf("version: 1\nproject: %s\nroutes:\n  - host: app.localhost\n    service: app\n    port: 80\nopenbao:\n  mount: kv\n  secrets:\n    - service: app\n      name: database_password\n      path: db\n      field: password\n",name)
    // Exercise both lifecycle phases against a real, digest-pinned application
    // container. nginx -v and nginx -t are harmless framework-neutral probes.
    configYAML += "lifecycle:\n  before_start:\n    - service: app\n      entrypoint: nginx\n      args: [-v]\n      timeout_seconds: 30\n  before_switch:\n    - service: app\n      entrypoint: nginx\n      args: [-t]\n      timeout_seconds: 30\n"
    if err:=os.WriteFile(filepath.Join(root,"deploy","bluegreen.yml"),[]byte(configYAML),0644);err!=nil{t.Fatal(err)}
	secretVersion := 1
	c := Controller{Root: root, WaitTimeout: 90 * time.Second}
	c.SecretResolver = func(_ context.Context, _ string, m openbao.Manifest) ([]openbao.Resolved,error) {
		return []openbao.Resolved{{Reference:m.Secrets[0], Version:secretVersion, Data:[]byte(fmt.Sprintf("password-v%d",secretVersion))}},nil
	}
	release := Release{Project: name, Version: "v1", Images: map[string]string{"app": appImage}, Routes: []Route{{Host: "app.localhost", Service: "app", Port: 80}}}
	t.Cleanup(func() {
		for _, slot := range []string{"blue", "green"} {
			env := c.environment(release, slot)
			_ = ExecRunner{}.Run(context.Background(), root, env, "docker", "compose", "-f", filepath.Join(root, "deploy/compose.yml"), "down", "--remove-orphans")
		}
		_ = ExecRunner{}.Run(context.Background(), root, c.environment(release, "blue"), "docker", "compose", "-f", filepath.Join(root, "deploy/infrastructure/compose.yml"), "down", "--volumes", "--remove-orphans")
	})
	if err := c.Deploy(ctx, release); err != nil {
		t.Fatalf("deploy v1: %v", err)
	}
	probe := func() {
		t.Helper()
		client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{ServerName: "app.localhost", InsecureSkipVerify: true}}}
		defer client.CloseIdleConnections()
		url := fmt.Sprintf("https://127.0.0.1:%d/", httpsPort)
		deadline := time.Now().Add(30 * time.Second)
		var lastErr error
		for time.Now().Before(deadline) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			req.Host = "app.localhost"
			response, err := client.Do(req)
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return
				}
				lastErr = fmt.Errorf("status %d", response.StatusCode)
			} else {
				lastErr = err
			}
			time.Sleep(700 * time.Millisecond)
		}
		t.Fatalf("HTTPS route never became ready: %v", lastErr)
	}
	probe()
	checkSlotSecret := func(slot, want string) {
		t.Helper()
		result, err := exec.CommandContext(ctx, "docker", "ps", "-q",
			"--filter", "label=com.docker.compose.project="+name+"-"+slot,
			"--filter", "label=com.docker.compose.service=app").Output()
		if err != nil { t.Fatal(err) }
		id := strings.TrimSpace(string(result))
		if id == "" { t.Fatalf("missing %s container", slot) }
		value, err := exec.CommandContext(ctx, "docker", "exec", id, "cat", "/run/secrets/database_password").Output()
		if err != nil || string(value)!=want { t.Fatalf("%s mounted secret: %q: %v", slot, value, err) }
	}
	checkSlotSecret("blue", "password-v1")
	secretVersion=2
	release.Version = "v2"
	if err := c.Deploy(ctx, release); err != nil {
		t.Fatalf("deploy v2: %v", err)
	}
	probe()
	checkSlotSecret("green", "password-v2")
	secretVersion=3
	if err := c.ReconcileSecrets(ctx, name); err != nil {
		t.Fatalf("reconcile changed credentials: %v", err)
	}
	probe()
	checkSlotSecret("blue", "password-v3")
	if err := c.Rollback(ctx, name); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	probe()
	checkSlotSecret("green", "password-v2")
}
