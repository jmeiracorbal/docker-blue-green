# Docker blue-green

A language-agnostic foundation for Blue-Green deployments with Docker Compose and Caddy. The intended delivery pipeline uses GitHub Actions to build container images, publish them to GitHub Container Registry (GHCR), and deploy immutable image references to a Docker host.

## Layout

```text
deploy/
├── compose.yml
└── infrastructure/
    ├── compose.yml
    └── caddy/
        ├── Caddyfile
        └── routes/
```

- `deploy/compose.yml` defines release-scoped application services. The same file can run in the `blue` and `green` slots without changing its contents.
- `deploy/infrastructure/compose.yml` defines long-lived, shared infrastructure. The initial implementation includes Caddy; projects can add their own persistent dependencies.
- `deploy/infrastructure/caddy/routes/` is reserved for active Caddy routing snippets. The deployment controller will manage those files and reload Caddy when switching traffic.
- `.env.example` lists configuration keys without sample values. Real values must be supplied for the target environment and must not be committed.

## Configuration contract

Both Compose files use required variable interpolation (`${VARIABLE:?message}`), with no default values.

| Variable | Purpose |
| --- | --- |
| `PROJECT_NAME` | Logical project identifier used for Compose project names and service aliases |
| `DEPLOYMENT_SLOT` | Application slot (`blue` or `green`), set by the deployment controller |
| `APPLICATION_IMAGE` | Fully qualified, immutable application image reference (prefer a digest) |
| `APPLICATION_PORT` | Port exposed by the application container |
| `DEPLOY_NETWORK` | Shared Docker network name created by the infrastructure Compose project |
| `CADDY_IMAGE` | Caddy container image reference |
| `CADDY_ACME_EMAIL` | ACME account contact email |
| `HTTP_PORT` | Host HTTP port |
| `HTTPS_PORT` | Host HTTPS port, TCP and UDP |

The application is accessible on the shared Docker network through the slot-specific alias `<project>-<slot>-app`. Keep persistent storage out of the application slots; shared databases and other stateful services belong in infrastructure. An application image must supply an appropriate Docker `HEALTHCHECK` for deployment health verification.

## Deployment model

1. Build and publish application images in CI.
2. Start the inactive application slot with a pinned image reference.
3. Validate its container health and application readiness.
4. Atomically update the Caddy route configuration and reload Caddy.
5. Verify the active route; retain the previous slot for rollback until it is safe to retire.

Application database migrations must remain compatible with both slots during the cutover. Existing WebSocket connections require a deliberate draining policy.

## Status

This commit establishes the repository layout and Compose configuration contract. The deployment controller, registry publishing workflows, slot switching, and rollback are not implemented yet; this scaffold alone is not a production deployment tool.
