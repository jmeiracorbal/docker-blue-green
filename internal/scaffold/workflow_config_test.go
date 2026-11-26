package scaffold

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmeiracorbal/docker-blue-green/internal/config"
	"gopkg.in/yaml.v3"
)

func runnerWorkflowNode(t *testing.T, content []byte, path ...string) *yaml.Node {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		t.Fatal(err)
	}
	node := root.Content[0]
	for _, key := range path {
		var err error
		node, err = workflowEntry(node, key)
		if err != nil {
			t.Fatal(err)
		}
	}
	return node
}

func mustRunnerLabels(t *testing.T, content []byte, job string, labels []string) {
	t.Helper()
	node := runnerWorkflowNode(t, content, "jobs", job, "runs-on")
	if node.Kind != yaml.SequenceNode || len(node.Content) != len(labels) {
		t.Fatalf("%s runs-on must be an explicit sequence: %+v", job, node)
	}
	for i, item := range node.Content {
		if item.Value != labels[i] {
			t.Fatalf("%s runner label %d = %q, want %q", job, i, item.Value, labels[i])
		}
	}
}

func mustReusableRunner(t *testing.T, content []byte, job string, labels string) {
	t.Helper()
	encoded := runnerWorkflowNode(t, content, "jobs", job, "with", "runner_labels_json")
	if encoded.Value != labels {
		t.Fatalf("%s runner input = %q, want %q", job, encoded.Value, labels)
	}
	uses := runnerWorkflowNode(t, content, "jobs", job, "uses")
	ref := runnerWorkflowNode(t, content, "jobs", job, "with", "engine_ref")
	if !strings.HasSuffix(uses.Value, "@"+runnerCapableEngineRef) || ref.Value != runnerCapableEngineRef {
		t.Fatalf("%s must pin same CI-capable engine in uses and engine_ref", job)
	}
}

func TestGenerateRunnersFromCanonicalCIContract(t *testing.T) {
	ci := &config.CI{RunnerLabels: []string{"self-hosted", "linux", "x64", "custom"}}
	files, err := Generate(Options{CI: ci})
	if err != nil {
		t.Fatal(err)
	}
	document, err := config.Parse([]byte(find(files, "deploy/bluegreen.yml")))
	if err != nil {
		t.Fatal(err)
	}
	if document.CI == nil || strings.Join(document.CI.RunnerLabels, ",") != "self-hosted,linux,x64,custom" {
		t.Fatalf("scaffold did not persist configured labels: %+v", document.CI)
	}
	release := []byte(find(files, ".github/workflows/release.yml"))
	gate := []byte(find(files, ".github/workflows/blue-green.yml"))
	for _, job := range []string{"publish", "deploy"} {
		mustReusableRunner(t, release, job, `["self-hosted","linux","x64","custom"]`)
	}
	mustReusableRunner(t, gate, "validate", `["self-hosted","linux","x64","custom"]`)
	for _, job := range []string{"repository-secrets", "blue-green"} {
		mustRunnerLabels(t, gate, job, ci.RunnerLabels)
	}
	steps := runnerWorkflowNode(t, gate, "jobs", "repository-secrets", "steps")
	found := false
	for _, step := range steps.Content {
		env, err := workflowEntry(step, "env")
		if err != nil {
			continue
		}
		ref, err := workflowEntry(env, "ENGINE_REF")
		if err == nil && ref.Value == runnerCapableEngineRef {
			found = true
		}
	}
	if !found {
		t.Fatal("locally installed CLI must use CI-capable engine")
	}

	original, err := Generate(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(find(original, ".github/workflows/release.yml")), publishWorkflowFile().Data) {
		t.Fatal("initializing without CI settings must retain the established workflow template")
	}
	if _, err := Generate(Options{CI: &config.CI{RunnerLabels: []string{"invalid label"}}}); err == nil {
		t.Fatal("invalid CI labels accepted by init")
	}
}

func writeManagedWorkflowFixtures(t *testing.T, root string) {
	t.Helper()
	for _, file := range []File{publishWorkflowFile(), validationWorkflowFile()} {
		target := filepath.Join(root, file.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, file.Data, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReconcileWorkflowRunnersPreservesJobsAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	writeManagedWorkflowFixtures(t, root)
	releasePath := filepath.Join(root, ".github/workflows/release.yml")
	before, err := os.ReadFile(releasePath)
	if err != nil {
		t.Fatal(err)
	}
	// A consumer-owned addition must survive the structured rewrite.
	const marker = "\n# organization CI rule; do not remove\n"
	if err := os.WriteFile(releasePath, append(before, []byte(marker)...), 0644); err != nil {
		t.Fatal(err)
	}
	ci := &config.CI{RunnerLabels: []string{"self-hosted", "linux", "x64", "shared"}}
	updates, err := PrepareWorkflowRunnerUpdate(root, ci)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 generated workflow updates, got %d", len(updates))
	}
	if err := ApplyWorkflowRunnerUpdate(root, updates); err != nil {
		t.Fatal(err)
	}
	release, err := os.ReadFile(releasePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(release), "organization CI rule; do not remove") {
		t.Fatal("consumer workflow comment was lost")
	}
	gatePath := filepath.Join(root, ".github/workflows/blue-green.yml")
	gate, err := os.ReadFile(gatePath)
	if err != nil {
		t.Fatal(err)
	}
	mustReusableRunner(t, release, "publish", `["self-hosted","linux","x64","shared"]`)
	mustReusableRunner(t, release, "deploy", `["self-hosted","linux","x64","shared"]`)
	mustReusableRunner(t, gate, "validate", `["self-hosted","linux","x64","shared"]`)
	for _, job := range []string{"repository-secrets", "blue-green"} {
		mustRunnerLabels(t, gate, job, ci.RunnerLabels)
	}
	again, err := PrepareWorkflowRunnerUpdate(root, ci)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range again {
		if !bytes.Equal(file.Original, file.Data) {
			t.Fatalf("second reconciliation rewrites %s", file.Path)
		}
	}
	if err := ApplyWorkflowRunnerUpdate(root, again); err != nil {
		t.Fatal(err)
	}
	snapshots, err := filepath.Glob(filepath.Join(root, ".github/workflows/.bluegreen-workflow-*"))
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("temporary files leaked after apply: %v, %v", snapshots, err)
	}
}

func TestReconcileWorkflowRunnersRejectsNonManagedOrModifiedFiles(t *testing.T) {
	root := t.TempDir()
	ci := &config.CI{RunnerLabels: []string{"self-hosted", "linux", "x64", "build"}}
	if _, err := PrepareWorkflowRunnerUpdate(root, ci); err == nil {
		t.Fatal("accepted missing managed workflows")
	}
	writeManagedWorkflowFixtures(t, root)
	releasePath := filepath.Join(root, ".github/workflows/release.yml")
	release, err := os.ReadFile(releasePath)
	if err != nil {
		t.Fatal(err)
	}
	unmanaged := bytes.Replace(release, []byte("docker-blue-green/.github/workflows/publish-images.yml"), []byte("someone/else/.github/workflows/publish-images.yml"), 1)
	if bytes.Equal(unmanaged, release) {
		t.Fatal("fixture did not alter caller ownership")
	}
	if err := os.WriteFile(releasePath, unmanaged, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareWorkflowRunnerUpdate(root, ci); err == nil {
		t.Fatal("accepted an unrelated publisher workflow")
	}
	if err := os.WriteFile(releasePath, release, 0644); err != nil {
		t.Fatal(err)
	}
	updates, err := PrepareWorkflowRunnerUpdate(root, ci)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releasePath, append(release, []byte("\n# modified concurrently\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyWorkflowRunnerUpdate(root, updates); err == nil {
		t.Fatal("overwrote workflow modified after preflight")
	}
	// A symlink at a generated path must never be accepted as a target.
	if err := os.Remove(releasePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".github/workflows/blue-green.yml"), releasePath); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareWorkflowRunnerUpdate(root, ci); err == nil {
		t.Fatal("followed symlink to managed workflow")
	}
}
