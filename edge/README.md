# Shared Edge

The sole Caddy instance on a Docker host is managed by the bluegreen engine.

The engine installs and reconciles Edge under the sibling directory `edge` of the per-project deployment directories. The Caddy configuration imports `apps/*.caddy` without modifying the central Caddyfile for each new application. Each project owns one `<project>.caddy` file, written by the engine only after health checks.

The host-wide lock serializes provisioning and route publication across independent projects. Existing project names and hostnames are checked before updates. Divergent host-wide settings are rejected rather than overwriting other projects' configuration. Deploying a new project never upgrades the shared Caddy version.

The shared Edge environment uses: `CADDY_IMAGE`, `CADDY_ACME_EMAIL`, `DEPLOY_NETWORK`, `HTTP_PORT` and `HTTPS_PORT`. The first project establishes these settings; later projects must agree. Changes to shared configuration or Caddy versions are separate, explicit infrastructure operations.

Application infrastructure Compose files must not contain their own `caddy` service. An application service that should be reachable through Edge must declare its routing in the project deployment contract and connect to the shared Docker network. The generated route targets the selected application's Blue/Green network alias.
