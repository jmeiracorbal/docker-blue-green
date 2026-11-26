package scaffold

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jmeiracorbal/docker-blue-green/internal/config"
	"gopkg.in/yaml.v3"
)

// runnerCapableEngineRef is an immutable engine revision that understands both
// the CI project contract and the reusable runner_labels_json input.
const runnerCapableEngineRef = "55f051fc4f00f050e8dc43cd71995038b950f930"
const legacyEngineRef = "007a49463462a310c825c79671653f4879273059"

var gitCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type WorkflowUpdate struct {
	Path     string
	Original []byte
	Data     []byte
}

func validateRunnerSelection(ci *config.CI) error {
	if ci == nil {
		return nil
	}
	return (config.Document{Version: 1, CI: ci}).Validate(false)
}

// generatedWorkflows uses the same structured rewrite for newly scaffolded
// projects and existing consumer workflows. It never embeds a provider choice.
func generatedWorkflows(ci *config.CI) ([]File, error) {
	if err := validateRunnerSelection(ci); err != nil {
		return nil, err
	}
	files := []File{publishWorkflowFile(), validationWorkflowFile()}
	if ci == nil {
		return files, nil
	}
	for i, f := range files {
		revised, err := updateRunnerWorkflow(f.Path, f.Data, ci.RunnerLabels)
		if err != nil {
			return nil, fmt.Errorf("generate %s: %w", f.Path, err)
		}
		files[i].Data = revised
	}
	return files, nil
}

// PrepareWorkflowRunnerUpdate checks both managed files before any writes.
// It refuses missing, symlinked or unrelated files rather than replacing them.
func PrepareWorkflowRunnerUpdate(root string, ci *config.CI) ([]WorkflowUpdate, error) {
	if ci == nil {
		return nil, nil
	}
	if err := validateRunnerSelection(ci); err != nil {
		return nil, err
	}
	var updates []WorkflowUpdate
	for _, path := range []string{".github/workflows/release.yml", ".github/workflows/blue-green.yml"} {
		target := filepath.Join(root, path)
		info, err := os.Lstat(target)
		if err != nil {
			return nil, fmt.Errorf("managed workflow %s is required: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("managed workflow %s must be a regular file", path)
		}
		current, err := os.ReadFile(target)
		if err != nil {
			return nil, err
		}
		next, err := updateRunnerWorkflow(path, current, ci.RunnerLabels)
		if err != nil {
			return nil, fmt.Errorf("cannot reconcile %s: %w", path, err)
		}
		updates = append(updates, WorkflowUpdate{Path: path, Original: current, Data: next})
	}
	return updates, nil
}

// ApplyWorkflowRunnerUpdate stages every changed YAML file before replacing any
// source. Unchanged files are left untouched, preserving idempotence.
func ApplyWorkflowRunnerUpdate(root string, updates []WorkflowUpdate) error {
	type staged struct{ target, temp string }
	var staging []staged
	defer func() {
		for _, f := range staging {
			_ = os.Remove(f.temp)
		}
	}()
	for _, change := range updates {
		if change.Path != ".github/workflows/release.yml" && change.Path != ".github/workflows/blue-green.yml" {
			return fmt.Errorf("unmanaged workflow path %q", change.Path)
		}
		target := filepath.Join(root, change.Path)
		info, err := os.Lstat(target)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("managed workflow %s must be a regular file", change.Path)
		}
		current, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, change.Original) {
			return fmt.Errorf("workflow %s changed while configuring CI", change.Path)
		}
		if bytes.Equal(current, change.Data) {
			continue
		}
		f, err := os.CreateTemp(filepath.Dir(target), ".bluegreen-workflow-")
		if err != nil {
			return err
		}
		staging = append(staging, staged{target: target, temp: f.Name()})
		if err := f.Chmod(info.Mode().Perm()); err != nil {
			_ = f.Close()
			return err
		}
		if _, err := f.Write(change.Data); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	for _, f := range staging {
		if err := os.Rename(f.temp, f.target); err != nil {
			return err
		}
	}
	return nil
}

func updateRunnerWorkflow(path string, source []byte, labels []string) ([]byte, error) {
	encoded, err := json.Marshal(labels)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(source, &document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("workflow must contain one YAML mapping")
	}
	jobs, err := workflowEntry(document.Content[0], "jobs")
	if err != nil {
		return nil, err
	}
	if jobs.Kind != yaml.MappingNode {
		return nil, errors.New("workflow jobs must be a mapping")
	}
	switch path {
	case ".github/workflows/release.yml":
		if err := setRunnerInput(jobs, "publish", "publish-images.yml", string(encoded)); err != nil {
			return nil, err
		}
		if err := setRunnerInput(jobs, "deploy", "deploy-remote.yml", string(encoded)); err != nil {
			return nil, err
		}
	case ".github/workflows/blue-green.yml":
		if err := setRunnerInput(jobs, "validate", "validate-consumer.yml", string(encoded)); err != nil {
			return nil, err
		}
		for _, name := range []string{"repository-secrets", "blue-green"} {
			job, err := workflowEntry(jobs, name)
			if err != nil {
				return nil, err
			}
			runsOn, err := workflowEntry(job, "runs-on")
			if err != nil {
				return nil, err
			}
			runsOn.Kind = yaml.SequenceNode
			runsOn.Tag = "!!seq"
			runsOn.Value = ""
			runsOn.Content = nil
			for _, label := range labels {
				runsOn.Content = append(runsOn.Content, &yaml.Node{
					Kind: yaml.ScalarNode, Tag: "!!str", Value: label,
				})
			}
		}
		// repository-secrets installs the CLI separately. Keep its engine SHA
		// aligned with the executable used by reusable workflows.
		secrets, _ := workflowEntry(jobs, "repository-secrets")
		steps, err := workflowEntry(secrets, "steps")
		if err != nil || steps.Kind != yaml.SequenceNode {
			return nil, errors.New("repository-secrets must declare steps")
		}
		found := false
		for _, step := range steps.Content {
			env, err := workflowEntry(step, "env")
			if err != nil {
				continue
			}
			ref, err := workflowEntry(env, "ENGINE_REF")
			if err != nil {
				continue
			}
			if ref.Value == legacyEngineRef {
				ref.Value = runnerCapableEngineRef
			}
			if !gitCommitPattern.MatchString(ref.Value) {
				return nil, errors.New("repository-secrets ENGINE_REF must be immutable")
			}
			found = true
		}
		if !found {
			return nil, errors.New("repository-secrets must declare ENGINE_REF")
		}
	default:
		return nil, fmt.Errorf("unsupported managed workflow %s", path)
	}
	return yaml.Marshal(&document)
}

func setRunnerInput(jobs *yaml.Node, jobName, reusableName, encoded string) error {
	job, err := workflowEntry(jobs, jobName)
	if err != nil {
		return err
	}
	uses, err := workflowEntry(job, "uses")
	if err != nil {
		return err
	}
	prefix := "jmeiracorbal/docker-blue-green/.github/workflows/" + reusableName + "@"
	if !strings.HasPrefix(uses.Value, prefix) {
		return fmt.Errorf("%s must call managed reusable workflow %s", jobName, reusableName)
	}
	ref := strings.TrimPrefix(uses.Value, prefix)
	if !gitCommitPattern.MatchString(ref) {
		return fmt.Errorf("%s must pin an immutable engine commit", jobName)
	}
	with, err := workflowEntry(job, "with")
	if err != nil {
		return err
	}
	if with.Kind != yaml.MappingNode {
		return fmt.Errorf("%s with must be a mapping", jobName)
	}
	engine, err := workflowEntry(with, "engine_ref")
	if err != nil {
		return err
	}
	if engine.Value != ref {
		return fmt.Errorf("%s uses and engine_ref differ", jobName)
	}
	if ref == legacyEngineRef {
		ref = runnerCapableEngineRef
		uses.Value = prefix + ref
		engine.Value = ref
	}
	value, err := optionalWorkflowEntry(with, "runner_labels_json")
	if err != nil {
		return err
	}
	if value == nil {
		with.Content = append(with.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "runner_labels_json"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: encoded},
		)
	} else {
		value.Kind = yaml.ScalarNode
		value.Tag = "!!str"
		value.Value = encoded
		value.Content = nil
	}
	return nil
}

func workflowEntry(mapping *yaml.Node, key string) (*yaml.Node, error) {
	node, err := optionalWorkflowEntry(mapping, key)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, fmt.Errorf("missing managed workflow field %q", key)
	}
	return node, nil
}

func optionalWorkflowEntry(mapping *yaml.Node, key string) (*yaml.Node, error) {
	if mapping.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected YAML mapping for %s", key)
	}
	var value *yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			if value != nil {
				return nil, fmt.Errorf("duplicate workflow field %q", key)
			}
			value = mapping.Content[i+1]
		}
	}
	return value, nil
}
