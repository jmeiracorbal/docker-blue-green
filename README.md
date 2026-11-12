# Docker blue-green

A reusable starting point for deploying containerized applications to a single Docker host with Docker Compose and Caddy.

The project aims to provide automated Blue-Green releases with GitHub Actions and GitHub Container Registry (GHCR), without requiring Kubernetes or a framework-specific deployment stack.

## Features

- Separate Blue and Green application environments.
- Shared infrastructure that remains running during application updates.
- Reverse proxy and TLS termination with Caddy.
- Application images supplied from a container registry.
- Configuration through environment variables.

Automated releases, traffic switching, health verification, and rollback are planned, **not yet implemented**.

## Requirements

- Docker Engine with the Docker Compose plugin.
- A container image for the application.
- A Docker host reachable through SSH.
- DNS configured for the application's public hostname when exposing services through Caddy.

## Getting started

1. Create a local `.env` file using `.env.example` and set the values for your project.
2. Adapt `deploy/compose.yml` to the services in your application.
3. Add any shared services to `deploy/infrastructure/compose.yml`.

The repository currently contains the initial Compose configuration only. It does not yet provide a complete deployment command or public routing automation.
