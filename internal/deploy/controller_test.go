package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls     []string
	failOn    string
	unhealthy bool
}

func (f *fakeRunner) Run(_ context.Context, _ string, _ []string, bin string, args ...string) error {
	c := bin + " " + strings.Join(args, " ")
	f.calls = append(f.calls, c)
	if f.failOn != "" && strings.Contains(c, f.failOn) {
		return errors.New("simulated command failure")
	}
	return nil
}
func (f *fakeRunner) Output(_ context.Context, _ string, _ []string, bin string, args ...string) (string, error) {
	c := bin + " " + strings.Join(args, " ")
	f.calls = append(f.calls, c)
	if f.failOn != "" && strings.Contains(c, f.failOn) {
		return "", errors.New("simulated command failure")
	}
	if strings.Contains(c, "ps -q ") {
		return "fake-container", nil
	}
	if strings.Contains(c, "inspect --format") {
		if f.unhealthy {
			return "unhealthy", nil
		}
		return "healthy", nil
	}
	return "", nil
}
func testRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"deploy/compose.yml", "deploy/infrastructure/compose.yml", "deploy/infrastructure/caddy/Caddyfile"} {
		target := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("services: {}\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func release(version string) Release {
	return Release{
		Project: "demo", Version: version,
		Images: map[string]string{"api": "ghcr.io/example/api@sha256:" + strings.Repeat("a", 64)},
		Routes: []Route{{Host: "api.example.com", Service: "api", Port: 8000}},
	}
}
func TestDeployAndRollback(t *testing.T) {
	root := testRoot(t)
	runner := &fakeRunner{}
	c := Controller{Root: root, Runner: runner}
	ctx := context.Background()
	if err := c.Deploy(ctx, release("v1")); err != nil {
		t.Fatal(err)
	}
	s, err := readState(filepath.Join(root, ".bluegreen"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ActiveSlot != "blue" || s.ActiveVersion != "v1" || s.PreviousSlot != "" {
		t.Fatalf("first state: %+v", s)
	}
	contents, err := os.ReadFile(routeFile(root, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "demo-blue-api:8000") {
		t.Fatal(string(contents))
	}
	count := len(runner.calls)
	if err := c.Deploy(ctx, release("v1")); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != count {
		t.Fatal("re-deployed same image digest")
	}
	second := release("v2")
	second.Images["api"] = "ghcr.io/example/api@sha256:" + strings.Repeat("b", 64)
	if err := c.Deploy(ctx, second); err != nil {
		t.Fatal(err)
	}
	s, err = readState(filepath.Join(root, ".bluegreen"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ActiveSlot != "green" || s.PreviousVersion != "v1" || s.PreviousImages["api"] != release("v1").Images["api"] {
		t.Fatalf("second state: %+v", s)
	}
	if err := c.Rollback(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(routeFile(root, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "demo-blue-api:8000") {
		t.Fatal(string(contents))
	}
	s, err = readState(filepath.Join(root, ".bluegreen"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ActiveVersion != "v1" || s.PreviousVersion != "v2" {
		t.Fatalf("rollback state: %+v", s)
	}
}
func TestUnhealthyDoesNotSwitch(t *testing.T) {
	root := testRoot(t)
	runner := &fakeRunner{}
	c := Controller{Root: root, Runner: runner}
	if err := c.Deploy(context.Background(), release("v1")); err != nil {
		t.Fatal(err)
	}
	runner.unhealthy = true
	if err := c.Deploy(context.Background(), release("v2")); err == nil || !strings.Contains(err.Error(), "not healthy") {
		t.Fatalf("error: %v", err)
	}
	b, err := os.ReadFile(routeFile(root, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "demo-blue-api") {
		t.Fatal(string(b))
	}
	runner.unhealthy = false
	if err := c.Deploy(context.Background(), release("v2")); err != nil {
		t.Fatal(err)
	}
	s, err := readState(filepath.Join(root, ".bluegreen"))
	if err != nil {
		t.Fatal(err)
	}
	if s.ActiveVersion != "v2" || s.Phase != "" {
		t.Fatalf("recovery failed: %+v", s)
	}
}
func TestCaddyReloadFailureRestoresRoute(t *testing.T) {
	root := testRoot(t)
	runner := &fakeRunner{}
	c := Controller{Root: root, Runner: runner}
	if err := c.Deploy(context.Background(), release("v1")); err != nil {
		t.Fatal(err)
	}
	runner.failOn = "caddy reload"
	if err := c.Deploy(context.Background(), release("v2")); err == nil {
		t.Fatal("expected failure")
	}
	b, err := os.ReadFile(routeFile(root, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "demo-blue-api") {
		t.Fatal(string(b))
	}
	runner.failOn = ""
	if err := c.Deploy(context.Background(), release("v2")); err != nil {
		t.Fatal(err)
	}
}
func TestInvalidManifest(t *testing.T) {
	for _, r := range []Release{
		{Project: "UPPER", Version: "v1", Images: release("v1").Images, Routes: release("v1").Routes},
		{Project: "demo", Version: "v1", Images: map[string]string{"api": "foo:latest"}, Routes: release("v1").Routes},
		{Project: "demo", Version: "v1", Images: release("v1").Images, Routes: []Route{{Host: "foo {invalid}", Service: "api", Port: 80}}},
	} {
		if err := Validate(r); err == nil {
			t.Fatalf("expected invalid release: %+v", r)
		}
	}
}
func TestReadManifestTrailingData(t *testing.T) {
	p := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(p, []byte(`{"project":"demo","version":"v1"} {}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRelease(p); err == nil {
		t.Fatal("accepted trailing data")
	}
}
