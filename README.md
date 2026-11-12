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

Set `OCI_IMAGE_PREFIX` to the complete image destination, including the OCI registry hostname and repository prefix (for example, `registry.example.com/team/project` or `ghcr.io/team/project`). No registry is selected automatically. Set `OCI_PUSH_USERNAME` for the publishing account and `GIT_REGISTRY_TOKEN` as a secret with image-push permissions.

The generated workflow calls the shared publisher at a fixed commit for reproducibility. Publishing does not require deployment credentials. Once remote deployment is enabled and configured, the release workflow can deploy the published image digests.

## Deploying

Define the public hostnames, application service names and container ports in `deploy/routes.json`. Every application container must have a working Docker healthcheck. Keep stateful services in `deploy/infrastructure/compose.yml`; migrations must remain compatible with the previously running application during a cutover.

The CLI can deploy a release manifest on a Docker host:

```bash
bluegreen deploy --dir . --release release.json --env-file .env
bluegreen rollback --dir . --project myapp --env-file .env
```

The reusable remote workflow transfers the deployment bundle, a pinned Linux CLI binary, the image-digest manifest and the runtime environment. It uses `DEPLOY_PROJECT`, `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_PORT` and `DEPLOY_SSH_KNOWN_HOSTS`; set `DEPLOY_ENABLED=true` to enable remote deployment. The deployment determines the registry from the immutable image references in the release manifest, without separate registry destination variables.

Provide `DEPLOY_SSH_PRIVATE_KEY` and `DEPLOY_ENV` as secrets. If the published images are private, also configure `OCI_PULL_USERNAME` and `OCI_PULL_TOKEN` as secrets with read-only registry access; public images require neither. `DEPLOY_ENV` contains the environment consumed by the remote Compose files and is stored with restricted permissions. No cloud-provider-specific identifiers are required.

The remote workflow runs over verified SSH. Shared infrastructure remains up while the inactive application slot is prepared, then Caddy is reloaded only after the new containers pass their healthchecks. The previous slot is preserved for rollback. A full server-level end-to-end test must be performed before using the workflow for production traffic.
