package scaffold

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func find(files []File, path string) string {
	for _, f := range files {
		if f.Path == path {
			return string(f.Data)
		}
	}
	return ""
}

func TestInitializeExistingCompose(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "compose.yml")
	doc := `{
 "name":"legacy", "services":{
 "api":{"build":{"context":"./backend"},"image":"legacy-api:dev","ports":[{"target":8000,"published":"8000","protocol":"tcp"}],"depends_on":{"db":{"condition":"service_healthy"}},"env_file":["./backend/.env"],"volumes":[{"type":"bind","source":"./backend/media","target":"/app/media"}]},
 "db":{"image":"postgres:16","networks":["private"],"volumes":["dbdata:/var/lib/postgresql/data"]}
 }, "networks":{"private":{"internal":true}}, "volumes":{"dbdata":{}}
}`
	files, err := Generate(Options{Root: root, SourcePath: source, ComposeJSON: []byte(doc), AppServices: []string{"api"}, InfrastructureServices: []string{"db"}})
	if err != nil {
		t.Fatal(err)
	}
	app := find(files, "deploy/compose.yml")
	infra := find(files, "deploy/infrastructure/compose.yml")
	if !strings.Contains(app, `"${API_IMAGE}"`) {
		t.Fatal("app image not parameterized", app)
	}
	for _, bad := range []string{"depends_on", "published:", "container_name:"} {
		if strings.Contains(app, bad) {
			t.Errorf("app contains %s", bad)
		}
	}
	for _, expected := range []string{`"8000"`, `"../backend"`, `"../backend/.env"`, `"../backend/media"`, `"${DEPLOY_NETWORK}"`, `"${PROJECT_NAME}-${DEPLOYMENT_SLOT}-api"`} {
		if !strings.Contains(app, expected) {
			t.Errorf("missing in app: %s", expected)
		}
	}
	for _, expected := range []string{"db:", "caddy:", "caddy_data:", "dbdata:", `"${PROJECT_NAME}-infrastructure"`, `"${DEPLOY_NETWORK}"`, `"./caddy/Caddyfile:/etc/caddy/Caddyfile:ro"`} {
		if !strings.Contains(infra, expected) {
			t.Errorf("missing in infra: %s", expected)
		}
	}
	if strings.Contains(infra, "api:") {
		t.Fatal("app leaked into infrastructure")
	}
	env := find(files, ".env.example")
	for _, required := range []string{"PROJECT_NAME=", "DEPLOYMENT_SLOT=", "API_IMAGE=", "DEPLOY_NETWORK=", "CADDY_IMAGE="} {
		if !strings.Contains(env, required) {
			t.Errorf("missing key %s", required)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(env), "\n") {
		if !strings.HasSuffix(line, "=") {
			t.Errorf("example has a value: %s", line)
		}
	}
	if err := Write(root, files); err != nil {
		t.Fatal(err)
	}
	if err := Write(root, files); err != nil {
		t.Fatalf("not idempotent: %v", err)
	}
}

func TestNoSourceGeneratesBasicScaffold(t *testing.T) {
	files, err := Generate(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 7 {
		t.Fatalf("got %d files", len(files))
	}
	if !strings.Contains(find(files, "deploy/compose.yml"), "${APPLICATION_IMAGE}") {
		t.Fatal("missing default app")
	}
	if !strings.Contains(find(files, "deploy/bluegreen.yml"), "routes: []") { t.Fatal("missing route manifest") }
	if !strings.Contains(find(files, "deploy/infrastructure/compose.yml"), "caddy:") {
		t.Fatal("missing caddy")
	}
}

func TestClassificationRequiresAllServices(t *testing.T) {
	_, err := Generate(Options{ComposeJSON: []byte(`{"services":{"api":{},"db":{}}}`), AppServices: []string{"api"}, Input: strings.NewReader("")})
	if err == nil || !strings.Contains(err.Error(), "db") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInteractiveClassification(t *testing.T) {
	var prompts bytes.Buffer
	classes, err := classify([]string{"api", "db"}, nil, nil, strings.NewReader("a\ni\n"), &prompts)
	if err != nil {
		t.Fatal(err)
	}
	if classes["api"] != "app" || classes["db"] != "infra" {
		t.Fatal(classes)
	}
	if !strings.Contains(prompts.String(), "Service api") {
		t.Fatal("missing prompt")
	}
}

func TestSharedVolumeRejected(t *testing.T) {
	doc := []byte(`{"services":{"api":{"volumes":["data:/app"]},"db":{"volumes":["data:/data"]}},"volumes":{"data":{}}}`)
	_, err := Generate(Options{ComposeJSON: doc, AppServices: []string{"api"}, InfrastructureServices: []string{"db"}})
	if err == nil || !strings.Contains(err.Error(), "volume") {
		t.Fatalf("expected shared volume error, got %v", err)
	}
}

func TestNoOverwrite(t *testing.T) {
	root := t.TempDir()
	files := emptyScaffold()
	if err := os.MkdirAll(filepath.Join(root, "deploy"), 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "deploy", "compose.yml")
	if err := os.WriteFile(target, []byte("custom"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Write(root, files); err == nil {
		t.Fatal("must reject divergent file")
	}
	if _, err := os.Stat(filepath.Join(root, ".env.example")); !os.IsNotExist(err) {
		t.Fatal("partial write occurred")
	}
}

func TestDefaultsRejected(t *testing.T) {
	doc := []byte(`{"services":{"api":{"image":"${IMAGE:-latest}"}}}`)
	_, err := Generate(Options{ComposeJSON: doc, AppServices: []string{"api"}})
	if err == nil || !strings.Contains(err.Error(), "interpolation") {
		t.Fatalf("expected default detection, got %v", err)
	}
}

func TestHostNetworkRejected(t *testing.T) {
	doc := []byte(`{"services":{"api":{"network_mode":"host"}}}`)
	_, err := Generate(Options{ComposeJSON: doc, AppServices: []string{"api"}})
	if err == nil || !strings.Contains(err.Error(), "network_mode") {
		t.Fatalf("expected network error, got %v", err)
	}
}

func TestFindSourceAmbiguity(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"compose.yml", "docker-compose.yml"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("services: {}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := FindSource(root, ""); err == nil {
		t.Fatal("expected ambiguity error")
	}
}

func TestPublishedWorkflowUsesProjectContract(t *testing.T) {
    files,err:=Generate(Options{})
    if err!=nil{t.Fatal(err)}
    workflow:=find(files,".github/workflows/release.yml")
    for _,expected:=range []string{
        "on:", "types: [published]",
        "secrets.OCI_REGISTRY_TOKEN","secrets.SERVER_SSH_PRIVATE_KEY",
        "secrets.OCI_REGISTRY_PULL_TOKEN","secrets.OCI_REGISTRY_PULL_USERNAME",
        "github.event.release.tag_name",
        "docker-blue-green/.github/workflows/publish-images.yml@",
        "docker-blue-green/.github/workflows/deploy-remote.yml@",
        "engine_ref:",
    } {
        if !strings.Contains(workflow,expected){t.Errorf("missing %q in workflow",expected)}
    }
    for _,obsolete:=range []string{
        "vars.","oci_registry_host:","oci_registry_repository:","oci_registry_username:",
        "deploy_host:","deploy_user:","deploy_port:","deploy_ssh_known_hosts:",
        "DEPLOY_ENV","DEPLOY_SSH_PRIVATE_KEY",
    } {
        if strings.Contains(workflow,obsolete){t.Errorf("workflow still duplicates %q",obsolete)}
    }
    if strings.Contains(workflow,"@main"){t.Fatal("reusable workflows must use immutable commits")}
    engine:=regexp.MustCompile(`engine_ref: ([0-9a-f]{40})`)
    refs:=engine.FindAllStringSubmatch(workflow,-1)
    if len(refs)!=2 || refs[0][1]!=refs[1][1]{
        t.Fatalf("publishing and deployment must use the same immutable engine: %v",refs)
    }
    pinned:=strings.Count(workflow,"@"+refs[0][1])
    if pinned!=2{t.Fatalf("reusable workflows must use the same pinned engine; got %d",pinned)}
}
