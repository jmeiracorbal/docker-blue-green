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
├── routes.json
└── infrastructure/
    ├── compose.yml
    └── caddy/
        ├── Caddyfile
        └── routes/
.github/workflows/release.yml
.env.example
```

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

Define the public hostnames, application service names and container ports in `deploy/routes.json`. Every application container must have a working Docker healthcheck. Keep stateful services in `deploy/infrastructure/compose.yml`; migrations must remain compatible with the previously running application during a cutover.

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
| `DEPLOY_SSH_PRIVATE_KEY` | Secret | *(SSH private key)* | Private key used to authenticate to the deployment server. |
| `DEPLOY_ENV` | Secret | `PROJECT_NAME=myapp` | Runtime environment contents required by the generated Compose files; may contain multiple newline-separated entries. |
| `OCI_REGISTRY_PULL_USERNAME` | Secret (optional) | `ci-reader` | Account authorized to download private OCI images. |
| `OCI_REGISTRY_PULL_TOKEN` | Secret (optional) | *(read-only registry token)* | Credential for downloading private OCI images. |

The two `OCI_REGISTRY_PULL_*` secrets are required **only for private images**. For public images, leave both unset. The publisher's `OCI_REGISTRY_TOKEN` is not forwarded to the deployment server.

The deployment derives the registry hostname and exact image references from the release manifest; it does not need a second set of registry destination variables. No cloud-provider-specific identifiers are required.

The remote workflow runs over verified SSH. Shared infrastructure remains up while the inactive application slot is prepared, then Caddy is reloaded only after the new containers pass their healthchecks. The previous slot is preserved for rollback. A full server-level end-to-end test must be performed before using the workflow for production traffic.
