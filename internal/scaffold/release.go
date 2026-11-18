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
    uses: ./.github/workflows/blue-green.yml
    secrets: inherit

  publish:
    needs: validate
    uses: jmeiracorbal/docker-blue-green/.github/workflows/publish-images.yml@adfe87ec60f72310afbae0820065bdb6ed5f0072
    with:
      version: ${{ github.event.release.tag_name }}
      source_ref: ${{ github.event.release.tag_name }}
      engine_ref: adfe87ec60f72310afbae0820065bdb6ed5f0072
    secrets:
      OCI_REGISTRY_TOKEN: ${{ secrets.GITHUB_TOKEN }}

  deploy:
    needs: publish
    uses: jmeiracorbal/docker-blue-green/.github/workflows/deploy-remote.yml@adfe87ec60f72310afbae0820065bdb6ed5f0072
    with:
      version: ${{ github.event.release.tag_name }}
      source_ref: ${{ github.event.release.tag_name }}
      engine_ref: adfe87ec60f72310afbae0820065bdb6ed5f0072
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
  workflow_call:

permissions:
  contents: read

jobs:
  validate:
    uses: jmeiracorbal/docker-blue-green/.github/workflows/validate-consumer.yml@adfe87ec60f72310afbae0820065bdb6ed5f0072
    with:
      engine_ref: adfe87ec60f72310afbae0820065bdb6ed5f0072
      validation_env_file: .github/blue-green.env

  repository-secrets:
    name: repository-secrets
    runs-on: ubuntu-latest
    permissions:
      contents: read
    # GitHub resolves these expressions. Only booleans reach the runner.
    env:
      HAS_SERVER_SSH_PRIVATE_KEY: ${{ secrets.SERVER_SSH_PRIVATE_KEY != '' }}
      HAS_OCI_REGISTRY_PULL_USERNAME: ${{ secrets.OCI_REGISTRY_PULL_USERNAME != '' }}
      HAS_OCI_REGISTRY_PULL_TOKEN: ${{ secrets.OCI_REGISTRY_PULL_TOKEN != '' }}
    steps:
      - uses: actions/checkout@v4
        with:
          persist-credentials: false
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - name: Check required repository secrets
        env:
          ENGINE_REF: adfe87ec60f72310afbae0820065bdb6ed5f0072
        shell: bash
        run: |
          set -euo pipefail
          go install "github.com/jmeiracorbal/docker-blue-green/cmd/bluegreen@$ENGINE_REF"
          "$(go env GOPATH)/bin/bluegreen" secrets plan --dir . > "$RUNNER_TEMP/bluegreen-repository-secrets.json"
          missing=0
          while IFS= read -r secret_name; do
            case "$secret_name" in
              SERVER_SSH_PRIVATE_KEY)
                present="$HAS_SERVER_SSH_PRIVATE_KEY"
                ;;
              OCI_REGISTRY_PULL_USERNAME)
                present="$HAS_OCI_REGISTRY_PULL_USERNAME"
                ;;
              OCI_REGISTRY_PULL_TOKEN)
                present="$HAS_OCI_REGISTRY_PULL_TOKEN"
                ;;
              *)
                echo "::error::Unsupported required repository secret: $secret_name"
                missing=1
                continue
                ;;
            esac
            if [[ "$present" != "true" ]]; then
              echo "::error::Missing required repository secret: $secret_name"
              missing=1
            fi
          done < <(jq -r '.github_required[]' "$RUNNER_TEMP/bluegreen-repository-secrets.json")
          [[ "$missing" -eq 0 ]] || exit 1

  blue-green:
    name: blue-green
    needs: [validate, repository-secrets]
    if: ${{ always() }}
    runs-on: ubuntu-latest
    steps:
      - name: Require successful Blue-Green validation
        env:
          VALIDATION_RESULT: ${{ needs.validate.result }}
          REPOSITORY_SECRETS_RESULT: ${{ needs.repository-secrets.result }}
        shell: bash
        run: |
          set -euo pipefail
          [[ "$VALIDATION_RESULT" == "success" && "$REPOSITORY_SECRETS_RESULT" == "success" ]] || {
            echo "Blue-Green prerequisites failed: validation=$VALIDATION_RESULT, repository-secrets=$REPOSITORY_SECRETS_RESULT" >&2
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
