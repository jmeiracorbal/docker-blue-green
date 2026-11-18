# Shared Edge

The sole Caddy instance on a Docker host is managed by the bluegreen engine.

The engine installs and reconciles Edge under the sibling directory `edge` of the per-project deployment directories. The Caddy configuration imports `apps/*.caddy` without modifying the central Caddyfile for each new application. Each project owns one `<project>.caddy` file, written by the engine only after health checks.

The host-wide lock serializes provisioning and route publication across independent projects. Existing project names and hostnames are checked before updates. Divergent host-wide settings are rejected rather than overwriting other projects' configuration. Deploying a new project never upgrades the shared Caddy version.

The shared Edge environment uses: `CADDY_IMAGE`, `CADDY_ACME_EMAIL`, `DEPLOY_NETWORK`, `HTTP_PORT` and `HTTPS_PORT`. The first project establishes these settings; later projects must agree. Changes to shared configuration or Caddy versions are separate, explicit infrastructure operations.

Application infrastructure Compose files must not contain their own `caddy` service. An application service that should be reachable through Edge must declare its routing under `x-bluegreen.ingress` in `deploy/compose.yml` and connect to the shared Docker network. The generated route targets the selected application's Blue/Green network alias.

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

## Reusable migration validation

A consumer repository invokes
`.github/workflows/validate-consumer.yml` by an immutable Blue-Green commit
SHA. The reusable workflow installs the same immutable CLI, verifies the
generated deployment files, checks the single Compose ingress contract,
rejects obsolete route/secret manifests or a project-owned Caddy, validates
Compose with the caller's **non-secret** CI environment file, and exports
the public release contract. It never deploys infrastructure, reads live
OpenBao secret values, or connects to a production host.

The consumer must pass `engine_ref` equal to the pinned workflow revision
and `validation_env_file` as a repository-relative file path.
