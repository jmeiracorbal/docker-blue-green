#!/usr/bin/env bash
set -euo pipefail

# End-to-end SSH and Docker test on a disposable GitHub-hosted Linux runner.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE="$ROOT/examples/remote-e2e"
TMP="$(mktemp -d)"
SSH_PORT=22222
REGISTRY_PORT=5005
REGISTRY_CONTAINER="bluegreen-e2e-registry-$$"
SSH_USER="$(id -un)"
SSH_TARGET="$SSH_USER@127.0.0.1"
REMOTE_REL=".local/share/docker-blue-green/bge2e"
REMOTE_DIR="$HOME/$REMOTE_REL"
APP_REPO="localhost:$REGISTRY_PORT/bge2e-app"

cleanup() {
  local status=$?
  trap - EXIT
  set +e
  if [[ -f "$REMOTE_DIR/.env" ]]; then
    for slot in blue green; do
      (cd "$REMOTE_DIR" && DEPLOYMENT_SLOT="$slot" \
        docker compose --env-file .env -f deploy/compose.yml down --remove-orphans) >/dev/null 2>&1
    done
    (cd "$REMOTE_DIR" && docker compose --env-file .env \
      -f deploy/infrastructure/compose.yml down --volumes --remove-orphans) >/dev/null 2>&1
  fi
  docker rm -f "$REGISTRY_CONTAINER" >/dev/null 2>&1
  if [[ -f "$TMP/sshd.pid" ]]; then
    sudo kill "$(cat "$TMP/sshd.pid")" >/dev/null 2>&1
  fi
  rm -rf "$REMOTE_DIR" "$TMP"
  exit "$status"
}
trap cleanup EXIT

ssh-keygen -q -t ed25519 -N "" -f "$TMP/client"
ssh-keygen -q -t ed25519 -N "" -f "$TMP/host"
cp "$TMP/client.pub" "$TMP/authorized_keys"
chmod 600 "$TMP/authorized_keys"
printf '[127.0.0.1]:%s %s\n' "$SSH_PORT" "$(cut -d' ' -f1,2 "$TMP/host.pub")" > "$TMP/known_hosts"

cat > "$TMP/sshd_config" <<EOF
Port $SSH_PORT
ListenAddress 127.0.0.1
HostKey $TMP/host
PidFile $TMP/sshd.pid
AuthorizedKeysFile $TMP/authorized_keys
StrictModes no
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
UsePAM no
AllowUsers $SSH_USER
Subsystem sftp /usr/lib/openssh/sftp-server
LogLevel ERROR
EOF
sudo install -d -m 755 /run/sshd
sudo /usr/sbin/sshd -t -f "$TMP/sshd_config"
sudo /usr/sbin/sshd -f "$TMP/sshd_config"

SSH_ARGS=(-i "$TMP/client" -p "$SSH_PORT" -o "UserKnownHostsFile=$TMP/known_hosts" -o StrictHostKeyChecking=yes -o BatchMode=yes -o ConnectTimeout=3)
SCP_ARGS=(-i "$TMP/client" -P "$SSH_PORT" -o "UserKnownHostsFile=$TMP/known_hosts" -o StrictHostKeyChecking=yes -o BatchMode=yes)
for _ in {1..15}; do
  if ssh "${SSH_ARGS[@]}" "$SSH_TARGET" true 2>/dev/null; then break; fi
  sleep 1
done
ssh "${SSH_ARGS[@]}" "$SSH_TARGET" 'docker info >/dev/null'

docker run -d --rm --name "$REGISTRY_CONTAINER" \
  -p "127.0.0.1:$REGISTRY_PORT:5000" registry:2 >/dev/null
for _ in {1..20}; do
  if curl -fsS "http://127.0.0.1:$REGISTRY_PORT/v2/" >/dev/null; then break; fi
  sleep 1
done
curl -fsS "http://127.0.0.1:$REGISTRY_PORT/v2/" >/dev/null

cp -a "$FIXTURE/app" "$TMP/app"
build_release() {
  local version="$1"
  printf '%s\n' "$version" > "$TMP/app/version.txt"
  docker build --quiet -t "$APP_REPO:$version" "$TMP/app" >/dev/null
  docker push "$APP_REPO:$version" >/dev/null
  local digest
  digest="$(docker image inspect --format '{{index .RepoDigests 0}}' "$APP_REPO:$version")"
  [[ "$digest" =~ @sha256:[a-f0-9]{64}$ ]] || { echo "Missing OCI digest: $digest" >&2; exit 1; }
  printf '%s\n' "$digest"
}

echo "Building and publishing immutable fixture releases..."
V1_IMAGE="$(build_release v1)"
V2_IMAGE="$(build_release v2)"
V3_IMAGE="$(build_release v3)"

(cd "$ROOT" && go build -o "$TMP/bluegreen" ./cmd/bluegreen)
tar -C "$FIXTURE" -czf "$TMP/deploy.tgz" deploy

ssh "${SSH_ARGS[@]}" "$SSH_TARGET" "install -d -m 700 '$REMOTE_REL/.incoming'"
scp "${SCP_ARGS[@]}" "$FIXTURE/.env.example" "$SSH_TARGET:$REMOTE_REL/.env"
scp "${SCP_ARGS[@]}" "$TMP/deploy.tgz" "$TMP/bluegreen" "$SSH_TARGET:$REMOTE_REL/.incoming/"
ssh "${SSH_ARGS[@]}" "$SSH_TARGET" \
  "cd '$REMOTE_REL' && tar -xzf .incoming/deploy.tgz && install -m 700 .incoming/bluegreen ./bluegreen"

deploy_release() {
  local version="$1" image="$2"
  jq -n --arg version "$version" --arg image "$image" \
    '{project:"bge2e", version:$version, images:{app:$image}, routes:[{host:"e2e.localhost",service:"app",port:8080}]}' \
    > "$TMP/release.json"
  scp "${SCP_ARGS[@]}" "$TMP/release.json" "$SSH_TARGET:$REMOTE_REL/.incoming/release.json"
  ssh "${SSH_ARGS[@]}" "$SSH_TARGET" \
    "cd '$REMOTE_REL' && install -m 600 .incoming/release.json ./release.json && ./bluegreen deploy --dir . --release release.json --env-file .env"
}

assert_route() {
  local expected_version="$1" expected_slot="$2" response actual
  for _ in {1..45}; do
    if response="$(curl -ksSf --max-time 3 \
       --resolve e2e.localhost:18443:127.0.0.1 \
       https://e2e.localhost:18443/version 2>/dev/null)"; then
      actual="$(jq -r '.version + " " + .slot' <<< "$response")"
      if [[ "$actual" == "$expected_version $expected_slot" ]]; then
        echo "Verified active route: $actual"
        return 0
      fi
    fi
    sleep 1
  done
  echo "Unexpected active route: ${response:-no response}; expected $expected_version $expected_slot" >&2
  return 1
}

set_failure() {
  local name="$1" value="$2"
  [[ "$name" == E2E_FAIL_BEFORE_START || "$name" == E2E_FAIL_BEFORE_SWITCH ]] || exit 1
  [[ "$value" == 0 || "$value" == 1 ]] || exit 1
  ssh "${SSH_ARGS[@]}" "$SSH_TARGET" \
    "sed -i 's/^$name=.*/$name=$value/' '$REMOTE_REL/.env'"
}

expect_failed_hook() {
  local stage="$1" version="$2" image="$3" output
  if output="$(deploy_release "$version" "$image" 2>&1)"; then
    echo "Deployment $version unexpectedly passed $stage" >&2
    return 1
  fi
  if ! grep -q "lifecycle $stage" <<< "$output"; then
    echo "Deployment failed for an unexpected reason: $output" >&2
    return 1
  fi
  echo "Verified $stage failure without a traffic switch"
}

echo "Verifying SSH deployment, failed hooks, recovery and rollback..."
deploy_release v1 "$V1_IMAGE"
assert_route v1 blue

set_failure E2E_FAIL_BEFORE_START 1
expect_failed_hook before_start v2 "$V2_IMAGE"
assert_route v1 blue
set_failure E2E_FAIL_BEFORE_START 0
deploy_release v2 "$V2_IMAGE"
assert_route v2 green

set_failure E2E_FAIL_BEFORE_SWITCH 1
expect_failed_hook before_switch v3 "$V3_IMAGE"
assert_route v2 green
set_failure E2E_FAIL_BEFORE_SWITCH 0
deploy_release v3 "$V3_IMAGE"
assert_route v3 blue

ssh "${SSH_ARGS[@]}" "$SSH_TARGET" \
  "cd '$REMOTE_REL' && ./bluegreen rollback --dir . --project bge2e --env-file .env"
assert_route v2 green

echo "SSH end-to-end fixture passed."
