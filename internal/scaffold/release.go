package scaffold

import (
    "bytes"
    "fmt"
    "strings"
)

// publishWorkflowFile installs a thin, version-pinned caller for the shared
// registry publishing workflow. No registry credentials are stored in the repository.
func publishWorkflowFile() File {
	return File{
		Path: ".github/workflows/release.yml",
		Data: []byte(`name: Publish OCI images

on:
  release:
    types: [published]

permissions:
  contents: read
  packages: write

jobs:
  validate:
    uses: jmeiracorbal/docker-blue-green/.github/workflows/validate-consumer.yml@e82b74c860c0bf6db5bd116f26a411617f17afdf
    with:
      engine_ref: e82b74c860c0bf6db5bd116f26a411617f17afdf
      validation_env_file: .github/blue-green.env

  publish:
    needs: validate
    uses: jmeiracorbal/docker-blue-green/.github/workflows/publish-images.yml@e82b74c860c0bf6db5bd116f26a411617f17afdf
    with:
      version: ${{ github.event.release.tag_name }}
      source_ref: ${{ github.event.release.tag_name }}
      engine_ref: e82b74c860c0bf6db5bd116f26a411617f17afdf
    secrets:
      OCI_REGISTRY_TOKEN: ${{ secrets.GITHUB_TOKEN }}

  deploy:
    needs: publish
    uses: jmeiracorbal/docker-blue-green/.github/workflows/deploy-remote.yml@e82b74c860c0bf6db5bd116f26a411617f17afdf
    with:
      version: ${{ github.event.release.tag_name }}
      source_ref: ${{ github.event.release.tag_name }}
      engine_ref: e82b74c860c0bf6db5bd116f26a411617f17afdf
    secrets:
      SERVER_SSH_PRIVATE_KEY: ${{ secrets.SERVER_SSH_PRIVATE_KEY }}
      OCI_REGISTRY_PULL_USERNAME: ${{ secrets.OCI_REGISTRY_PULL_USERNAME }}
      OCI_REGISTRY_PULL_TOKEN: ${{ secrets.OCI_REGISTRY_PULL_TOKEN }}
`),
	}
}

// validationWorkflowFile creates the stable check name consumed by repository
// rulesets. A failing or skipped reusable validation fails the final gate.
func validationWorkflowFile() File {
    return File{Path:".github/workflows/blue-green.yml",Data:[]byte(`name: blue-green

on:
  pull_request:
  push:
    branches: [main, master]
  merge_group:

permissions:
  contents: read

jobs:
  validate:
    uses: jmeiracorbal/docker-blue-green/.github/workflows/validate-consumer.yml@e82b74c860c0bf6db5bd116f26a411617f17afdf
    with:
      engine_ref: e82b74c860c0bf6db5bd116f26a411617f17afdf
      validation_env_file: .github/blue-green.env

  blue-green:
    name: blue-green
    needs: [validate]
    if: ${{ always() }}
    runs-on: ubuntu-latest
    steps:
      - name: Require successful Blue-Green validation
        env:
          VALIDATION_RESULT: ${{ needs.validate.result }}
        shell: bash
        run: |
          set -euo pipefail
          [[ "$VALIDATION_RESULT" == "success" ]] || {
            echo "Required Blue-Green validation did not pass: $VALIDATION_RESULT" >&2
            exit 1
          }
`)}
}

func validationEnvironmentFile(example []byte) File {
    defaults:=map[string]string{
        "PROJECT_NAME":"bluegreen-ci",
        "DEPLOYMENT_SLOT":"blue",
        "DEPLOY_NETWORK":"bluegreen-ci-edge",
        "CADDY_IMAGE":"caddy:2.10.2-alpine",
        "CADDY_ACME_EMAIL":"ci@example.invalid",
        "HTTP_PORT":"18080",
        "HTTPS_PORT":"18443",
        "APPLICATION_PORT":"8080",
    }
    var out bytes.Buffer
    fmt.Fprintln(&out,"# Non-secret placeholders for validating Compose in CI. Never store credentials here.")
    for _,line:=range strings.Split(strings.TrimSpace(string(example)),"\n"){
        key,_,ok:=strings.Cut(line,"=")
        if !ok || key=="" {continue}
        value,exists:=defaults[key]
        if !exists {
            switch{
            case strings.HasSuffix(key,"_IMAGE"):
                value="ghcr.io/example/bluegreen-ci:validation"
            case strings.HasSuffix(key,"_PORT"):
                value="8080"
            default:
                value="ci-placeholder"
            }
        }
        fmt.Fprintf(&out,"%s=%s\n",key,value)
    }
    return File{Path:".github/blue-green.env",Data:out.Bytes()}
}
