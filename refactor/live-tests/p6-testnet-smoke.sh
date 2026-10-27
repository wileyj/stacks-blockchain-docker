#!/usr/bin/env bash
# P6: testnet smoke test with the Q27 config source (upstream stacks-core main sample/conf).
# Pass = check-config OK, the node stays up BOOT_SECS with no panic/FATAL and no restarts.
# It deliberately does NOT require RPC-up/sync: Hiro's regtest BTC peer is often slow (see 15, P4 attempt).
# Usage: refactor/live-tests/p6-testnet-smoke.sh [core_version] [boot_secs]   (defaults: 4.0.4, 180)
set -euo pipefail
V=${1:-4.0.4}; BOOT_SECS=${2:-180}; P=sbd-p6
URL=https://raw.githubusercontent.com/stacks-network/stacks-core/main/sample/conf/testnet-follower-conf.toml
W=$(mktemp -d); trap 'docker compose -p $P -f "$W/compose.yaml" down -v >/dev/null 2>&1 || true' EXIT
curl -fsSL "$URL" | grep -v '^\s*#' | perl -pe 's#^\[node\]$#[node]\nworking_dir = "/root/stacks-blockchain/data"#' > "$W/Config.toml"
{ printf 'name: %s\nservices:\n  node:\n    image: ghcr.io/stacks-network/stacks-core:%s\n' "$P" "$V"
  printf '    command: ["/bin/stacks-node","start","--config","/etc/stacks/Config.toml"]\n'
  printf '    configs: [{source: cfg, target: /etc/stacks/Config.toml}]\n    volumes: [chain:/root/stacks-blockchain/data]\n'
  printf 'configs:\n  cfg:\n    content: |\n'; sed 's/^/      /' "$W/Config.toml"
  printf 'volumes: {chain: {}}\n'; } > "$W/compose.yaml"
dc() { docker compose -p $P -f "$W/compose.yaml" "$@"; }
dc up --no-start --quiet-pull >/dev/null 2>&1
echo "== check-config ($V)"; dc run --rm --no-deps --entrypoint /bin/stacks-node node check-config --config /etc/stacks/Config.toml 2>&1 | tail -1
dc up -d >/dev/null 2>&1; sleep "$BOOT_SECS"
cid=$(dc ps -q node); restarts=$(docker inspect -f '{{.RestartCount}}' "$cid"); state=$(docker inspect -f '{{.State.Status}}' "$cid")
panics=$(dc logs --no-log-prefix node 2>&1 | grep -cE 'panicked|FATAL' || true)
dc logs --no-log-prefix node 2>&1 | grep -E 'Syncing Bitcoin headers' | tail -1 | cut -c1-160 || true
echo "state=$state restarts=$restarts panics=$panics"
[ "$state" = running ] && [ "$restarts" = 0 ] && [ "$panics" = 0 ] && echo "P6 PASS" || { echo "P6 FAIL"; dc logs --no-log-prefix node 2>&1 | grep -E 'panicked|FATAL' | head -3; exit 1; }
