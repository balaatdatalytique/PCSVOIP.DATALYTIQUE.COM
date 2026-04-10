#!/usr/bin/env bash
# ────────────────────────────────────────────────────────────────────────
# deploy.sh — Safe deployment procedure for pcsvoip.datalytique.com
#
# Rebuilds and redeploys the pcsvoip services with minimal downtime.
# Images are built BEFORE any container is stopped, and only the
# specified service is recreated (--no-deps) so unrelated containers
# keep running throughout.
#
# Prerequisites:
#   - smartcrm.pcsvoip.com stack must already be running (provides the
#     nginx-proxy container and the smartcrmpcsvoipcom_default network).
#   - healthcheck.sh should exist in the same directory for final
#     verification.
#
# Usage:
#   ./deploy.sh          # rebuild and deploy all services
#   ./deploy.sh web      # rebuild and deploy pcsvoip-web only
#   ./deploy.sh voice    # rebuild and deploy voice-proxy only
#   ./deploy.sh all      # same as no argument
# ────────────────────────────────────────────────────────────────────────
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

TARGET="${1:-all}"

# ── Validate argument ──────────────────────────────────────────────────
case "$TARGET" in
  web|voice|all) ;;
  *)
    echo "ERROR: Invalid argument '$TARGET'. Use 'web', 'voice', or 'all'."
    exit 1
    ;;
esac

# Map friendly names to docker-compose service names
WEB_SERVICE="pcsvoip-web"
VOICE_SERVICE="voice-proxy"

# Map friendly names to container names (for health checks)
WEB_CONTAINER="pcsvoip-web"
VOICE_CONTAINER="pcsvoip-voice"

# ── 1. Verify the nginx-proxy network exists ──────────────────────────
echo "==> Checking smartcrmpcsvoipcom_default network..."
if ! docker network inspect smartcrmpcsvoipcom_default >/dev/null 2>&1; then
  echo "ERROR: Network 'smartcrmpcsvoipcom_default' does not exist."
  echo "       Start smartcrm.pcsvoip.com first (nginx-proxy must be running)."
  exit 1
fi
echo "    Network exists."

# ── 2. Build new image(s) before stopping anything ────────────────────
echo "==> Building image(s) (target: $TARGET)..."
case "$TARGET" in
  web)   docker compose build "$WEB_SERVICE" ;;
  voice) docker compose build "$VOICE_SERVICE" ;;
  all)   docker compose build ;;
esac
echo "    Build complete."

# ── 3. Recreate only the specified service(s) ─────────────────────────
echo "==> Recreating container(s)..."
case "$TARGET" in
  web)
    docker compose up -d --force-recreate --no-deps "$WEB_SERVICE"
    CONTAINERS="$WEB_CONTAINER"
    ;;
  voice)
    docker compose up -d --force-recreate --no-deps "$VOICE_SERVICE"
    CONTAINERS="$VOICE_CONTAINER"
    ;;
  all)
    docker compose up -d --force-recreate --no-deps "$WEB_SERVICE" "$VOICE_SERVICE"
    CONTAINERS="$WEB_CONTAINER $VOICE_CONTAINER"
    ;;
esac
echo "    Containers recreated."

# ── 4. Wait for health checks to pass (up to 60 seconds) ─────────────
echo "==> Waiting for health checks (timeout: 60s)..."
for CONTAINER in $CONTAINERS; do
  echo "    Waiting for $CONTAINER..."
  SECONDS_WAITED=0
  while [ $SECONDS_WAITED -lt 60 ]; do
    STATUS=$(docker inspect --format='{{.State.Health.Status}}' "$CONTAINER" 2>/dev/null || echo "unknown")
    if [ "$STATUS" = "healthy" ]; then
      echo "    $CONTAINER is healthy."
      break
    fi
    sleep 2
    SECONDS_WAITED=$((SECONDS_WAITED + 2))
  done
  if [ "$STATUS" != "healthy" ]; then
    echo "WARNING: $CONTAINER did not become healthy within 60s (status: $STATUS)."
  fi
done

# ── 5. Reload nginx to pick up any DNS changes ───────────────────────
echo "==> Reloading nginx-proxy..."
if docker exec nginx-proxy nginx -s reload 2>&1; then
  echo "    nginx reloaded."
else
  echo "WARNING: nginx reload failed."
fi

# ── 6. Run healthcheck.sh for final verification ─────────────────────
echo "==> Running healthcheck.sh..."
if [ -x "$SCRIPT_DIR/healthcheck.sh" ]; then
  if "$SCRIPT_DIR/healthcheck.sh"; then
    echo "    Health check passed."
  else
    echo "WARNING: healthcheck.sh reported failures. Investigate manually."
    echo "         Automatic rollback is NOT performed."
  fi
else
  echo "    healthcheck.sh not found or not executable — skipping."
fi

echo "==> Deploy complete (target: $TARGET)."
