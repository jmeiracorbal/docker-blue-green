# Docker blue-green

[![Test CLI](https://github.com/jmeiracorbal/docker-blue-green/actions/workflows/test-cli.yml/badge.svg?branch=main)](https://github.com/jmeiracorbal/docker-blue-green/actions/workflows/test-cli.yml) [![Deployment engine](https://github.com/jmeiracorbal/docker-blue-green/actions/workflows/test-engine.yml/badge.svg?branch=main)](https://github.com/jmeiracorbal/docker-blue-green/actions/workflows/test-engine.yml) [![Compose validation](https://github.com/jmeiracorbal/docker-blue-green/actions/workflows/validate.yml/badge.svg?branch=main)](https://github.com/jmeiracorbal/docker-blue-green/actions/workflows/validate.yml) [![OpenBao integration](https://github.com/jmeiracorbal/docker-blue-green/actions/workflows/test-openbao.yml/badge.svg?branch=main)](https://github.com/jmeiracorbal/docker-blue-green/actions/workflows/test-openbao.yml)

**Documentation:** [Deployment lifecycle (English)](docs/README.md) · [Deployment lifecycle (Spanish)](docs/README.es.md) · [Shared Edge](edge/README.md)

A reusable, framework-independent foundation for Blue-Green deployments using Docker Compose and Caddy.

## Documentation

The complete path from an existing Compose project to a guarded pull request, OCI release, optional SSH deployment, shared Edge routing and rollback is documented in [English](docs/README.md) and [Spanish](docs/README.es.md). Both guides distinguish checks that run in GitHub Actions from production-host operations.

## Features

- Initialize an existing Docker Compose project without rewriting its source files.
- Separate application services from long-lived infrastructure.
- Prepare isolated Blue/Green application project names and a shared Docker network.
- Preserve service configuration, named volumes and relative paths where safe.
- Generate `.env.example` with empty values, and avoid overwriting customized deployment files.
- Install the release workflow and a mandatory Blue-Green validation workflow, both pinned to immutable engine commits.

Blue-Green deployment and rollback are available through the CLI. Production integration requires a configured Docker host, route definitions, registry access and compatible application healthchecks.

## Requirements

- Go 1.23 or later (to build the CLI).
- Docker Engine with Docker Compose v2 (to import an existing Compose project).
- Optional OpenBao integration: an existing HTTPS OpenBao server, a scoped KV v2 AppRole identity, and the `bao` binary with systemd on the Linux Docker host.

## Installation

From this repository:

```bash
go build -o bluegreen ./cmd/bluegreen
```

## Usage

Run inside an existing project:

```bash
bluegreen init
```

The CLI discovers `compose.yaml`, `compose.yml`, `docker-compose.yaml` or `docker-compose.yml` and asks whether each service is part of the application or shared infrastructure. For unattended setup, specify the classification explicitly:

```bash
bluegreen init --dir /path/to/project --app api,frontend --infrastructure postgres,redis
```

Select another input with `--compose path/to/file.yml`. If no Compose file exists, `init` creates a minimal application configuration that can be customized.

The generated files are:

```text
deploy/
├── compose.yml
├── bluegreen.yml
└── infrastructure/
    └── compose.yml  # only when stateful services are present
.github/workflows/release.yml
.github/workflows/blue-green.yml
.github/blue-green.env
.env.example
```

The engine itself owns the shared Edge directory and Caddy templates; **application scaffolds do not include a Caddy container**.

Configure the single project-level contract after initialization:

```bash
bluegreen configure
bluegreen config check
```

The guided CLI configures the project identifier, application hostname, container port, OCI registry and optional SSH deployment. Automation can pass the same non-secret values without prompts:

```bash
bluegreen configure --project myapp --host api.example.com --service api --port 8000 \
  --registry-host ghcr.io --registry-repository company/myapp --registry-username publisher
```

The project identity, registry, lifecycle, deployment target, and OpenBao
references belong to `deploy/bluegreen.yml`:

```yaml
version: 1
project: myapp
registry:
  host: ghcr.io
  repository: company/myapp
  username: publisher
```

Public routes are declared only in `deploy/compose.yml`, under the Compose
extension `x-bluegreen.ingress`. The interactive `bluegreen configure` command
updates this extension; it does not duplicate routes into `bluegreen.yml`:

```yaml
services:
  api:
    image: "${API_IMAGE}"
    expose:
      - "8000"
    networks:
      bluegreen:
        aliases: ["${PROJECT_NAME}-${DEPLOYMENT_SLOT}-api"]

networks:
  bluegreen:
    external: true
    name: "${DEPLOY_NETWORK}"

x-bluegreen:
  ingress:
    - host: api.example.com
      service: api
      port: 8000
```

The engine validates every ingress target against a versioned application
service, then produces `<project>.caddy` within the host-wide Edge installation.
It rejects the old `routes:` field in `deploy/bluegreen.yml` rather than
silently retaining two routing authorities.

To enable remote deployment, add the verified SSH target to the same configuration with `bluegreen configure --deploy-host ... --deploy-user ... --deploy-port ... --deploy-known-hosts ...`. Its `deployment` block is absent (and deployment disabled) until configured. The `known_hosts` value is the verified **public** SSH host key; never store the private SSH key here.

The file also contains optional OpenBao KV references. Legacy `deploy/routes.json` and `deploy/secrets.json` are rejected rather than silently ignored: move their settings into the project contract before deploying. Docker Compose retains service definitions, persistent volumes and networking. The immutable release JSON remains a runtime input (image digests and version), and the engine rejects a release whose project or routes differ from the Compose ingress and project contract. `bluegreen config export` validates the OCI registry configuration and emits project, routes, registry and optional deployment settings as JSON for the reusable workflows. It never includes OpenBao references, tokens or SSH private keys.

Set the environment variables required by the generated Compose files and the shared Edge installation. Edge is a single host-wide Caddy managed by bluegreen under the sibling `edge/` directory of the project deployment roots; each project owns exactly one `apps/<project>.caddy` route file. Shared Caddy and its network are provisioned/reconciled once, never created in an application's infrastructure Compose. All applications on a server must use identical Edge configuration (`DEPLOY_NETWORK`, `CADDY_IMAGE`, `CADDY_ACME_EMAIL`, `HTTP_PORT`, `HTTPS_PORT`); a mismatch fails explicitly. See [Edge](edge/README.md).

Set the environment variables required by the generated Compose files. Application images use per-service variables such as `API_IMAGE` so that releases can provide immutable image references. Host-published application ports are converted into container-only exposed ports; shared infrastructure remains outside the Blue/Green slots.

The initializer does not start containers. It rejects configurations that cannot be separated safely, and does not overwrite customized deployment files.


### Persistent service ingress

A persistent infrastructure service such as an S3-compatible object store may
explicitly opt into shared Edge ingress with `shared: true` in the application's
`x-bluegreen.ingress` contract. The engine routes it to
`<project>-infrastructure-<service>` on the shared Edge Docker network; unlike
application routes, its destination does not change between Blue and Green.
Only the explicitly declared infrastructure service must join the Edge network
with that alias. Keep databases, queues and private services on internal networks
unless there is a deliberate, justified ingress declaration. This does not
publish ports on the host or create per-application Caddy instances.

## Publishing images

A published GitHub Release builds every application service with a `build` definition in `deploy/compose.yml`. The images are pushed to the configured OCI registry with the release tag. Each resulting image digest is uploaded as a workflow artifact for later deployment.

Set the OCI destination and publishing username through `bluegreen configure`. The values are stored only in `deploy/bluegreen.yml`, shared with the deployment workflow. For GHCR publication, the generated release workflow passes GitHub's automatically provided `GITHUB_TOKEN` to the reusable OCI publisher. The consumer does not create an `OCI_REGISTRY_TOKEN` repository secret.

For a Compose service named `backend` and a release tagged `v1.2.0`, `registry.host: ghcr.io` and `registry.repository: company/myapp` publish `ghcr.io/company/myapp-backend:v1.2.0`. Immutable image digests are recorded for deployment.

`ghcr.io` is an example, not a default; the CLI requires an explicitly configured OCI destination.

The generated workflow calls the shared publisher at a fixed commit for reproducibility. The generated release workflow runs the full `blue-green` validation gate before publishing. With remote deployment enabled, this gate also requires the three GitHub repository secrets listed below, even if only publishing is intended. When deployment is disabled, those credentials are not required.

## Deploying

Define the public hostnames, application service names and container ports using `bluegreen configure`, which writes the `x-bluegreen.ingress` section of `deploy/compose.yml`. Every application container must have a working Docker healthcheck. Keep stateful services in `deploy/infrastructure/compose.yml`; migrations must remain compatible with the previously running application during a cutover.

The CLI can deploy a release manifest on a Docker host:

```bash
bluegreen deploy --dir . --release release.json --env-file .env
bluegreen rollback --dir . --project myapp --env-file .env
```

The reusable remote workflow transfers the deployment bundle, a pinned Linux CLI binary and the image-digest manifest. It reads the optional SSH target and verified public host key from `deploy/bluegreen.yml`. If no deployment target is configured, the deployment job is skipped after publishing.

The same GitHub Actions secret names are used in every consuming repository:

| Secret | Purpose |
| --- | --- |
| `SERVER_SSH_PRIVATE_KEY` | SSH private key, required by the generated gate when remote deployment is enabled. |
| `OCI_REGISTRY_PULL_USERNAME` | Registry reader identity, required by the generated gate when remote deployment is enabled. |
| `OCI_REGISTRY_PULL_TOKEN` | Registry read token, required by the generated gate when remote deployment is enabled. |

The generated gate currently requires all three when remote deployment is enabled, including for public images. The low-level transport can authenticate only when credentials are supplied, but CI intentionally enforces the current project contract. GitHub supplies `GITHUB_TOKEN` for publishing; that token is never forwarded to the server. There are no repository-specific GitHub Actions variables for the OCI destination, project name or deployment host.

The deployment server retains its own non-secret Compose `.env` file in the deployment working directory. GitHub Actions neither uploads nor overwrites it; `DEPLOY_ENV` is no longer a GitHub secret. Set network, Caddy and port variables in that server-side file. Application credentials belong in OpenBao.

The deployment derives the registry hostname and exact image references from the release manifest; it does not need a second set of registry destination variables. No cloud-provider-specific identifiers are required.

The remote workflow runs over verified SSH. Shared Edge and project infrastructure remain up while the inactive application slot is prepared, then the shared Caddy is reloaded only after the new containers pass their healthchecks. The previous slot is preserved for rollback. A full server-level end-to-end test must be performed before using the workflow for production traffic.

## Declarative deployment lifecycle

Every Compose project can declare one-off operations through `deploy/bluegreen.yml`.
The deployment engine executes commands using the **immutable image of the
declared application service**, without framework-specific integrations or
separate shell scripts on the deployment host.

```yaml
lifecycle:
  before_start:
    - service: app
      entrypoint: /app/migrate
      args: [--non-interactive]
      timeout_seconds: 300
  before_switch:
    - service: app
      entrypoint: /app/verify
      args: [--readiness]
      timeout_seconds: 60
```

The commands are examples: each consuming application supplies its own
executables inside its published OCI image. `service` must be declared in the
application Compose file and be present in the release's digest-pinned image
set. The CLI passes the configured executable and argument list directly
to Docker Compose, without invoking a shell on the deployment host.

Execution order:

1. Reconcile the server-wide Edge and project infrastructure, recover any interrupted deployment and
   prepare the inactive slot and OpenBao secret snapshot.
2. Pull the immutable application images. For new application releases,
   run `before_start` steps sequentially, each in an ephemeral container
   belonging to the inactive slot's Compose project.
3. Start the inactive application slot and require its Docker healthchecks
   to succeed.
4. Run `before_switch` steps sequentially. Switch Caddy only when **all**
   operations and healthchecks have succeeded.
5. Preserve the previous slot for an explicit application rollback.

Each step requires a Compose service, executable and a timeout between 1 and
3600 seconds; `args` is an optional list of literal arguments. The engine
uses `docker compose run --rm --no-deps -T --pull never --entrypoint`.
It rejects unknown YAML fields, invalid services and malformed operations.

A nonzero exit code or timeout aborts the deployment without changing the
active Caddy route. On retry, the engine reconciles the interrupted state.
Operations can run **more than once after a failure or interruption**;
database migrations must be idempotent and backward compatible with the
previous application version. No automatic database rollback is attempted.
An application rollback only restores container and routing state, not
database schema or data. Design schema changes using expand/migrate/contract.

Automatic OpenBao secret reconciliation skips lifecycle operations: a
credential rotation must not accidentally rerun database migrations. A
change to lifecycle instructions for the same already-active application
release is rejected; publish a new version instead.

Database-specific validators (including `schema-guard`) are **not bundled**
or required by this change. They may be evaluated as separate optional
validation integrations later.

## OpenBao integration

OpenBao is optional. The **OpenBao server** stores secrets centrally. An independent **OpenBao Proxy** runs as a Linux systemd service for each project, outside application containers. The CLI connects automatically through `/run/bluegreen/<project>/openbao.sock`; the socket, proxy credentials, and any supervisors are never mounted into the images.

### Configure the proxy once through the CLI

Prerequisites are `bao proxy`, systemd, a Linux deployment account, and an existing OpenBao AppRole identity with a read-only KV v2 policy limited to the project. Example OpenBao policy for project `myapp`:

```hcl
path "kv/data/projects/myapp/*" {
  capabilities = ["read"]
}

path "kv/metadata/projects/myapp" {
  capabilities = ["list"]
}

path "kv/metadata/projects/myapp/*" {
  capabilities = ["list"]
}
```

The OpenBao administrator creates the project-scoped AppRole identity and makes its identifiers available as protected files on the host. The one-time interactive setup needs only:

```bash
sudo bluegreen secrets setup --project myapp
```

The CLI prompts for the HTTPS URL of the OpenBao server, the existing Linux deployment account and the file locations of the AppRole RoleID and SecretID. **It does not request or print the credential values**, which remain in protected files. Systemd installation requires root privileges. This command runs on the deployment server, not on the developer's workstation.

The CLI installs project-scoped proxy configuration and credentials under `/etc/bluegreen/openbao/myapp/`, starts the proxy as a systemd service, and enables a per-project reconciliation timer. Re-running with identical settings is idempotent; divergent configuration is rejected. Creating the AppRole and its policy in OpenBao remains an administrator-side prerequisite.

### Discover and associate KV v2 secrets

After the proxy is running on the deployment server, discover the available secret paths **within the authorized project only**:

```bash
bluegreen secrets list --project myapp --mount kv
```

The output contains the permitted KV paths, for example `database` or `backend/api`, not their values. To inspect available field names without displaying credentials:

```bash
bluegreen secrets fields --project myapp --mount kv --path database
```

OpenBao requires `list` permission on project-scoped KV v2 metadata and `read` for field-name discovery. Metadata listings are not filtered per child key by OpenBao; restricting the operation to `projects/myapp` is therefore essential. The mount name is explicit on first use; once the project's `deploy/bluegreen.yml` contains an OpenBao mount, the CLI reads it automatically. These commands communicate with the local project socket and consequently run on the Docker host.

After `bluegreen init`, associate a secret with an application service using the guided CLI on the Docker host:

```bash
bluegreen secrets add --project myapp --mount kv --service api --name database_password
```

The CLI displays numbered authorized KV paths and then their available field names. The user selects one of each. The chosen names are saved as a reference; no values are copied to the repository. For an unattended workflow or a development machine without access to the server-local proxy, supply `--path database --field password` explicitly to the same command. The `--mount` flag is only needed for the first reference.

The same CLI updates the `openbao` section of `deploy/bluegreen.yml`, containing **references only**:

```yaml
openbao:
  mount: kv
  secrets:
    - service: api
      name: database_password
      path: database
      field: password
```

Verify the references against the running proxy without specifying a socket:

```bash
bluegreen secrets check --project myapp
```

The engine reads `kv/data/projects/myapp/database` through the project's OpenBao Proxy, without printing secret values.

### Delivery, rotation and rollback

During `bluegreen deploy`, the engine resolves all required secrets before changing Docker infrastructure. It writes a project-private, versioned host snapshot and a Compose override. The configured application services receive the values as read-only files at `/run/secrets/<name>`, using Docker Compose secrets. Applications must support reading these files; Compose cannot inject their contents into an arbitrary running process's environment.

KV v2 versions contribute to the release identity. On a subsequent deployment, changed secret versions cause the engine to prepare the inactive slot, check health and switch traffic; the previous slot keeps its earlier credentials for rollback.

Snapshots live under `.bluegreen/secrets/` on the Docker host, not in Git or GitHub Actions. They remain on host disk to support restarts and rollback. Docker Compose uses bind mounts for file-backed secrets and does not support UID remapping for those mounts, so the snapshot directories are private while file permissions allow arbitrary container UIDs to read their assigned file mounts. Use host-level disk encryption and restrictive access permissions. Obsolete snapshots are pruned after successful switches.

The CLI-provisioned systemd timer checks OpenBao every five minutes and invokes `bluegreen secrets reconcile` against the **currently active release**. It never deploys the latest published release automatically, nor does a secret-only rotation require registry pull credentials when its immutable image is already present locally. After an explicit rollback, the engine pauses automatic reconciliation until the next manually initiated successful deployment, avoiding automatic reapplication of the rolled-back secret version. A manual `bluegreen deploy` still checks secret versions immediately.

Automatic Ansible installation of Docker, Compose, and OpenBao host prerequisites is a separate concern and is not yet implemented.

### Reproducible real OpenBao integration test

The repository includes a minimal Docker Compose application in [`examples/openbao-demo/`](examples/openbao-demo/) and a separate [real OpenBao integration workflow](.github/workflows/test-openbao.yml).

On a disposable **Ubuntu/Linux** environment with Go 1.23+, Docker Engine/Compose v2 and permission to create a restricted directory under `/run/bluegreen` (the test uses passwordless `sudo` when not running as root), run from the repository root:

```bash
BLUEGREEN_REAL_OPENBAO=1 go test -v -timeout 10m ./internal/deploy -run '^TestRealOpenBaoBlueGreen$'
```

The test starts an ephemeral OpenBao **development-mode** server, provisions a KV v2 mount and a project-scoped AppRole, starts an authenticated Unix-socket proxy, and exercises the public CLI to list KV paths, discover field names, select and check a secret. It deploys the example application through Docker Compose and Caddy, verifies the mounted secret inside the Blue container, updates the KV value, reconciles the Green container, and verifies rollback restores the previous secret version. A forbidden cross-project KV read is also tested. The test removes its containers and temporary files.

**Testing scope:** OpenBao's development mode is insecure and must never be used in production. The integration test launches the proxy directly rather than installing the production systemd unit; `bluegreen secrets setup`, production TLS trust, and deployment against a separately provisioned OpenBao server require host-specific validation.

## Required Blue-Green validation

See the complete [integration and deployment lifecycle](docs/README.md) ([Spanish version](docs/README.es.md)) for the required GitHub ruleset, repository-secret checks, image publication and server transition.

`bluegreen init` generates `.github/workflows/blue-green.yml`. This workflow
runs on pull requests, pushes to main/master, and merge groups. It invokes a
revision-pinned, reusable validator and finishes with a dedicated job named
`blue-green`, which fails when the upstream validation fails or is skipped.

The reusable validation covers repository structure, the declared OpenBao and
GitHub secret **requirements** (via `bluegreen secrets plan`, without values
or access to secret stores), the public route/export contract, and the
Docker Compose manifests using `.github/blue-green.env`. The generated CI
environment contains only artificial, non-secret placeholders. CI does not
seed or read real OpenBao values.

To make the check truly **blocking for merges**, the repository owner must
make the exact `blue-green` job a required status check on the protected
branch or ruleset. Merely creating a GitHub Actions workflow cannot change
repository branch protection. Do not add path filters to the workflow.

The deployment secret readiness check is a separate trusted-server operation
(`bluegreen secrets check`) after OpenBao provisioning. It is not executed on
untrusted pull request code; secret values must never be copied into a PR,
CI fixtures, logs, or generated files.

### Checking configured GitHub repository secrets

The generated `repository-secrets` job runs in GitHub Actions, not on the Docker deployment host. GitHub evaluates only the presence of `SERVER_SSH_PRIVATE_KEY`, `OCI_REGISTRY_PULL_USERNAME`, and `OCI_REGISTRY_PULL_TOKEN` and passes three Boolean results to the runner. **Secret values never enter the validation process.**

When the project contract enables remote deployment, all three repository secrets are required. `bluegreen secrets plan` determines the requirement from the same declarative contract; disabled remote deployments do not require these credentials. A missing required secret fails `repository-secrets`, which in turn fails the final `blue-green` check. The generated release workflow calls the same local reusable workflow before publishing, so releases are blocked by missing credentials as well. Forked PRs cannot access repository secrets; when remote deployment requires them, these checks intentionally fail closed.

`GITHUB_TOKEN` is provisioned automatically by GitHub, not manually configured as a repository secret. OpenBao is independent: pull requests validate OpenBao references while the trusted deployment host verifies actual secret availability with `bluegreen secrets check`. Branch protection or a ruleset must still mark the `blue-green` status check as required; GitHub Actions cannot set branch rules.
