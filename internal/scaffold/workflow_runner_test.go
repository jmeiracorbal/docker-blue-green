package scaffold

import (
    "encoding/json"
    "os"
    "path/filepath"
    "testing"

    "gopkg.in/yaml.v3"
)

// Every reusable job must honor the caller-selected runner. Keep the existing
// hosted-runner behavior explicit for callers that do not supply this input.
func TestReusableWorkflowsAllowSelectingRunnerLabels(t *testing.T) {
    workflows := map[string][]string{
        "publish-images.yml": {"discover", "publish"},
        "deploy-remote.yml": {"plan", "deploy"},
        "validate-consumer.yml": {"validate"},
    }
    for name, expectedJobs := range workflows {
        t.Run(name, func(t *testing.T) {
            content, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
            if err != nil { t.Fatal(err) }
            var workflow struct {
                On struct {
                    WorkflowCall struct {
                        Inputs map[string]struct {
                            Type string `yaml:"type"`
                            Default string `yaml:"default"`
                        } `yaml:"inputs"`
                    } `yaml:"workflow_call"`
                } `yaml:"on"`
                Jobs map[string]struct { RunsOn string `yaml:"runs-on"` } `yaml:"jobs"`
            }
            if err := yaml.Unmarshal(content, &workflow); err != nil { t.Fatal(err) }
            runner, ok := workflow.On.WorkflowCall.Inputs["runner_labels_json"]
            if !ok || runner.Type != "string" { t.Fatal("missing string runner_labels_json input") }
            var labels []string
            if err := json.Unmarshal([]byte(runner.Default), &labels); err != nil {
                t.Fatalf("invalid runner default: %v", err)
            }
            if len(labels) != 1 || labels[0] != "ubuntu-latest" {
                t.Fatalf("default runner changed: %v", labels)
            }
            if len(workflow.Jobs) != len(expectedJobs) {
                t.Fatalf("unexpected jobs: %v", workflow.Jobs)
            }
            for _, name := range expectedJobs {
                job, ok := workflow.Jobs[name]
                if !ok { t.Fatalf("missing reusable job %q", name) }
                if job.RunsOn != "${{ fromJSON(inputs.runner_labels_json) }}" {
                    t.Fatalf("job %q ignores runner selection: %q", name, job.RunsOn)
                }
            }
        })
    }
}
