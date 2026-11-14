# Remote end-to-end deployment fixture

A disposable, framework-independent consumer project for exercising the *real*
release workflows, OCI registry, SSH transport, Docker Compose, Caddy, lifecycle
hooks, and explicit rollback. This directory is a standalone project template:
copy its contents to the root of a **separate, disposable GitHub repository**.
Do not deploy it into an existing application or production Docker host.

## Application contract

- `app/version.txt` is embedded in the OCI image at build time. Begin with `v1`;
  commit `v2` to the same file for the second release. `/version` returns the
  **image-baked** version and the active slot, so an old image cannot masquerade
  as a new release merely because a Compose environment variable changed.
- `GET /healthz` and the OCI image's `health` command support Docker healthchecks.
- `/e2e before-start` and `/e2e before-switch` are executed from the **same
  digest-pinned image** as the application. Both normally exit successfully.
- Set `E2E_FAIL_BEFORE_START=1`, `E2E_FAIL_BEFORE_SWITCH=1`, or
  `E2E_FAIL_HEALTH=1` in the server-local `.env` to exercise each failure
  boundary. Reset the flag before retrying; keep `deploy/bluegreen.yml` unchanged
  for a given release. Never use these flags in a production project.
- The application slot exposes no host port. Only the separate Caddy service
  listens on host ports, bound to loopback by the example `.env`.

## Isolated SSH destination

Use a **fresh, dedicated Linux/amd64 VM**, not a shared server or a container
with a mounted host Docker socket. Install Docker Engine and the Compose v2
plugin, then create a dedicated `bluegreen-e2e` SSH account with permission to
run Docker. Docker group access is effectively root: the VM must be disposable
and contain no other workloads or secrets. Do not reuse any production keys.

The runner executing the reusable `deploy-remote.yml` workflow must be able to
reach the VM's SSH port, either from a controlled CI network or a dedicated
runner connected to the test network. The application ports can stay
loopback-only; do not expose them to the internet. Provision the `.env` under
`~bluegreen-e2e/.local/share/docker-blue-green/bge2e/.env` from `.env.example`
**on the VM**. The workflow deliberately never sends or overwrites this file.
Keep the example's default failure flags set to zero.

In the disposable consumer repository, replace `registry.repository` and
`registry.username` in `deploy/bluegreen.yml` with the registry account
actually authorized to publish. Then configure the dedicated SSH target and
its **independently verified** public host key with `bluegreen configure`:

```bash
bluegreen configure --deploy-host <test-vm-host> \
  --deploy-user bluegreen-e2e --deploy-port 22 \
  --deploy-known-hosts '<verified-known_hosts-line>'
bluegreen config check
```

The angle-bracket fields above are **instructions**, not valid configuration
values. Do not commit a private SSH key, registry token, runtime `.env`, a
fictitious host key, or a production hostname. Set `OCI_REGISTRY_TOKEN` and
`SERVER_SSH_PRIVATE_KEY` only as secrets in the disposable consumer repository;
set the optional read-only OCI pull credential pair only when the published
images are private. The `deployment` block is deliberately absent from the
committed template: publishing cannot deploy until SSH is explicitly configured.

## Validation boundary

The included release workflow publishes image digests from the immutable tag
and then invokes the existing reusable remote-deployment workflow. Publish a
GitHub Release for the commit containing `v1` before creating the `v2` commit
and Release. These are two distinct OCI images, not renamed tags for one image.
The remote CLI receives digest-pinned references from the workflow.

From the dedicated VM, after a successful cutover, check the Caddy route:

```bash
curl --insecure --resolve e2e.localhost:18443:127.0.0.1 \
  https://e2e.localhost:18443/version
```

A successful response identifies both the embedded image version and current
slot. Inspect `~/.local/share/docker-blue-green/bge2e/.bluegreen/state.json`
for active and previous releases. Verify failed hooks and rollback against
that state and the HTTP response, not solely against a successful process exit.
TLS uses Caddy's local testing certificate; `--insecure` is limited to this
loopback-only fixture.

**Scope:** This first stage provides a testable fixture and a target contract.
It does not provision the VM, set credentials, publish OCI releases, or assert
that the full remote end-to-end path has run. Those execution checks belong to
the next stage of PR #4.
