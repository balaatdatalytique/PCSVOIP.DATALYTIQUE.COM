#!/usr/bin/env bash
# healthcheck.sh — verify the full pcsvoip.datalytique.com production stack
set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
RESET='\033[0m'

FAIL_COUNT=0

pass() { printf "${GREEN}PASS${RESET}  %s\n" "$1"; }
fail() { printf "${RED}FAIL${RESET}  %s\n" "$1"; FAIL_COUNT=$((FAIL_COUNT + 1)); }

# --- Container checks ---

check_running() {
    local container="$1" label="$2"
    if docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null | grep -q true; then
        pass "$label is running"
    else
        fail "$label is NOT running"
    fi
}

check_healthy() {
    local container="$1" label="$2"
    local status
    status=$(docker inspect -f '{{.State.Health.Status}}' "$container" 2>/dev/null || echo "none")
    if [ "$status" = "healthy" ]; then
        pass "$label is healthy"
    else
        fail "$label health status: $status"
    fi
}

check_network() {
    local container="$1" network="$2" label="$3"
    if docker inspect "$container" --format '{{json .NetworkSettings.Networks}}' 2>/dev/null | grep -q "\"$network\""; then
        pass "$label is on $network"
    else
        fail "$label is NOT on $network"
    fi
}

check_running  pcsvoip-web   "pcsvoip-web container"
check_healthy  pcsvoip-web   "pcsvoip-web container"
check_running  pcsvoip-voice "pcsvoip-voice container"
check_healthy  pcsvoip-voice "pcsvoip-voice container"
check_running  nginx-proxy   "nginx-proxy container"

check_network  pcsvoip-web   smartcrmpcsvoipcom_default "pcsvoip-web"
check_network  pcsvoip-voice smartcrmpcsvoipcom_default "pcsvoip-voice"

# --- HTTP / API checks ---

http_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 https://pcsvoip.datalytique.com/ || echo "000")
if [ "$http_code" = "200" ]; then
    pass "HTTPS site returns 200"
else
    fail "HTTPS site returned $http_code (expected 200)"
fi

chat_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 \
    -X POST https://pcsvoip.datalytique.com/api/chat \
    -H "Content-Type: application/json" \
    -d '{"message":"health check"}' || echo "000")
if [ "$chat_code" = "200" ]; then
    pass "Text chat API returns 200"
else
    fail "Text chat API returned $chat_code (expected 200)"
fi

ws_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 \
    -H "Upgrade: websocket" -H "Connection: Upgrade" \
    https://pcsvoip.datalytique.com/ws/voice || echo "000")
if [ "$ws_code" = "400" ]; then
    pass "Voice WebSocket endpoint reachable (400 = proxy reaches voice service)"
else
    fail "Voice WebSocket returned $ws_code (expected 400)"
fi

# --- Summary ---

echo ""
if [ "$FAIL_COUNT" -eq 0 ]; then
    printf "${GREEN}All checks passed.${RESET}\n"
    exit 0
else
    printf "${RED}%d check(s) failed.${RESET}\n" "$FAIL_COUNT"
    exit 1
fi
