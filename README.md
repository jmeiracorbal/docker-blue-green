# Docker blue-green

A reusable, framework-independent foundation for Blue-Green deployments using Docker Compose and Caddy.

## Features

- Initialize an existing Docker Compose project without rewriting its source files.
- Separate application services from long-lived infrastructure.
- Prepare isolated Blue/Green application project names and a shared Docker network.
- Preserve service configuration, named volumes and relative paths where safe.
- Generate `.env.example` with empty values, and avoid overwriting customized deployment files.
- Install a release workflow that builds and publishes versioned OCI images through a reusable, pinned workflow.

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

Select another input with `--compose path/to/file.yml`. If no Compose file exists, `init` creates a minimal application and Caddy configuration that can be customized.

The generated files are:

```text
deploy/
├── compose.yml
├── bluegreen.yml
└── infrastructure/
    ├── compose.yml
    └── caddy/
        ├── Caddyfile
        └── routes/
.github/workflows/release.yml
.env.example
```

Configure the single project-level contract after initialization:

```bash
bluegreen configure
bluegreen config check
```

The guided CLI prompts for the project identifier, application hostname, service and container port. Automation can pass the same values without prompts:

```bash
bluegreen configure --project myapp --host api.example.com --service api --port 8000
```

All projects use `deploy/bluegreen.yml`:

```yaml
version: 1
project: myapp
routes:
  - host: api.example.com
    service: api
    port: 8000
```

The file also contains optional OpenBao KV references. Docker Compose retains service definitions, persistent volumes and networking. The immutable release JSON remains a runtime input (image digests and version), and the engine rejects a release whose project or routes differ from the YAML contract. `bluegreen config export` emits only project and routes as JSON for reusable CI workflows; it never includes secret references or values.

Set the environment variables required by the generated Compose files. Application images use per-service variables such as `API_IMAGE` so that releases can provide immutable image references. Host-published application ports are converted into container-only exposed ports; shared infrastructure remains outside the Blue/Green slots.

The initializer does not start containers. It rejects configurations that cannot be separated safely, and does not overwrite customized deployment files.

## Publishing images

A published GitHub Release builds every application service with a `build` definition in `deploy/compose.yml`. The images are pushed to the configured OCI registry with the release tag. Each resulting image digest is uploaded as a workflow artifact for later deployment.

Configure these values under **Settings → Secrets and variables → Actions** in the consuming GitHub repository.

| Name | GitHub setting | Example | Description |
| --- | --- | --- | --- |
| `OCI_REGISTRY_HOST` | Variable | `ghcr.io` | OCI registry hostname, without `https://`. |
| `OCI_REGISTRY_REPOSITORY` | Variable | `company/project` | Repository path and image-name prefix within the registry. |
| `OCI_REGISTRY_USERNAME` | Variable | `ci-publisher` | Account that authenticates to the registry; it is not the Git commit author. |
| `OCI_REGISTRY_TOKEN` | Secret | *(registry access token)* | Credential authorized to push images. Never commit its value. |

For a Compose service named `backend` and a release tagged `v1.2.0`, the example above publishes `ghcr.io/company/project-backend:v1.2.0`. The workflow also records the immutable image digest used by deployments.

`ghcr.io` is an example, **not a default**. Supply the hostname and repository path of your chosen OCI-compatible registry.

The generated workflow calls the shared publisher at a fixed commit for reproducibility. Publishing does not require deployment credentials. Once remote deployment is enabled and configured, the release workflow can deploy the published image digests.

## Deploying

Define the public hostnames, application service names and container ports using `bluegreen configure`, which writes `deploy/bluegreen.yml`. Every application container must have a working Docker healthcheck. Keep stateful services in `deploy/infrastructure/compose.yml`; migrations must remain compatible with the previously running application during a cutover.

The CLI can deploy a release manifest on a Docker host:

```bash
bluegreen deploy --dir . --release release.json --env-file .env
bluegreen rollback --dir . --project myapp --env-file .env
```

The reusable remote workflow transfers the deployment bundle, a pinned Linux CLI binary, the image-digest manifest and the runtime environment. Configure the following values in **Settings → Secrets and variables → Actions** of the consuming repository:

| Name | GitHub setting | Example | Description |
| --- | --- | --- | --- |
| `DEPLOY_ENABLED` | Variable | `true` | Enables remote deployment when set to `true`. |
| `DEPLOY_PROJECT` | Variable | `myapp` | Unique deployment identifier on the server. |
| `DEPLOY_HOST` | Variable | `203.0.113.10` | Hostname or IP address of the Docker server. |
| `DEPLOY_PORT` | Variable | `22` | SSH port. |
| `DEPLOY_USER` | Variable | `deploy` | SSH account used to run Docker commands. |
| `DEPLOY_SSH_KNOWN_HOSTS` | Variable | `203.0.113.10 ssh-ed25519 AAAA…` | Verified SSH host-key entry in `known_hosts` format. The example is abbreviated, not a valid key. |
| `SERVER_SSH_PRIVATE_KEY` | Secret | *(SSH private key)* | Private key used to authenticate to the deployment server. |
| `OCI_REGISTRY_PULL_USERNAME` | Secret (optional) | `ci-reader` | Account authorized to download private OCI images. |
| `OCI_REGISTRY_PULL_TOKEN` | Secret (optional) | *(read-only registry token)* | Credential for downloading private OCI images. |

The two `OCI_REGISTRY_PULL_*` secrets are required **only for private images**. For public images, leave both unset. The publisher's `OCI_REGISTRY_TOKEN` is not forwarded to the deployment server.

The deployment server retains its own non-secret Compose `.env` file in the deployment working directory. GitHub Actions neither uploads nor overwrites it; `DEPLOY_ENV` is no longer a GitHub secret. Set network, Caddy and port variables in that server-side file. Application credentials belong in OpenBao.

The deployment derives the registry hostname and exact image references from the release manifest; it does not need a second set of registry destination variables. No cloud-provider-specific identifiers are required.

The remote workflow runs over verified SSH. Shared infrastructure remains up while the inactive application slot is prepared, then Caddy is reloaded only after the new containers pass their healthchecks. The previous slot is preserved for rollback. A full server-level end-to-end test must be performed before using the workflow for production traffic.

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
