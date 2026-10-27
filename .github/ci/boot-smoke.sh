#!/usr/bin/env bash
# Short boot test: render the config with manage.sh (docker stubbed), then start postgres + API +
# node from the repo's compose files with the pinned images, and require that for BOOT_SECS every
# service stays running with no restarts and the node logs no panic.
#
# It does not wait for RPC or sync: the testnet BTC peer is often slow (refactor/15, P4/P6).
# Runs as compose project `sbd-boot`; an override renames the containers and network and drops
# the host port bindings, so it never collides with a real stack on the same host.
#
# Usage: .github/ci/boot-smoke.sh [network] [boot_secs]   (defaults: testnet 180)
set -euo pipefail

REPO="$(cd -- "$(dirname "${0}")/../.." >/dev/null 2>&1 ; pwd -P)"
NETWORK="${1:-testnet}"
BOOT_SECS="${2:-180}"
P=sbd-boot
TMP="$(mktemp -d)"
source "${REPO}/.github/ci/lib.sh"
WORK="${TMP}/repo"

case ${NETWORK} in
	mainnet) STACKS_CHAIN_ID="0x00000001" ;;
	testnet) STACKS_CHAIN_ID="0x80000000" ;;
	*) echo "unsupported network: ${NETWORK}"; exit 2 ;;
esac
USER_ID="$(id -u)"
export SCRIPTPATH="${WORK}" NETWORK STACKS_CHAIN_ID USER_ID

cat > "${TMP}/override.yaml" <<EOF
services:
  postgres: {container_name: ${P}-postgres}
  stacks-blockchain-api: {container_name: ${P}-stacks-blockchain-api, ports: !reset []}
  stacks-blockchain: {container_name: ${P}-stacks-blockchain, ports: !reset []}
networks:
  default: {name: ${P}}
EOF
dc() {
	docker compose -p "${P}" --env-file "${WORK}/.env" -f "${WORK}/compose-files/common.yaml" \
		-f "${WORK}/compose-files/networks/${NETWORK}.yaml" -f "${TMP}/override.yaml" --profile stacks-blockchain "$@"
}
cleanup() {
	dc down -v -t 30 >/dev/null 2>&1 || true
	# persistent-data is written by root inside the containers
	docker run --rm -v "${TMP}:/w" alpine:3.20 rm -rf /w/repo/persistent-data >/dev/null 2>&1 || true
	rm -rf "${TMP}"
}
trap cleanup EXIT

echo "== render (${NETWORK})"
render "${WORK}" "${NETWORK}" >/dev/null
grep -E '^(bootstrap_node|peer_host|mode|timeout_ms)' "${WORK}/conf/${NETWORK}/Config.toml" || true

echo "== up, then hold ${BOOT_SECS}s"
dc up -d --quiet-pull 2>&1 | grep -vE '^ *(Container|Network|Volume) ' || true
sleep "${BOOT_SECS}"

fail=0
for svc in postgres stacks-blockchain-api stacks-blockchain; do
	cid=$(dc ps -aq "${svc}")
	state=$(docker inspect -f '{{.State.Status}}' "${cid}")
	restarts=$(docker inspect -f '{{.RestartCount}}' "${cid}")
	echo "${svc}: state=${state} restarts=${restarts}"
	[ "${state}" = running ] && [ "${restarts}" = 0 ] || fail=1
done
panics=$(dc logs --no-log-prefix stacks-blockchain 2>&1 | grep -cE 'panicked|FATAL' || true)
echo "node panics=${panics}"
dc logs --no-log-prefix stacks-blockchain 2>&1 | grep -E 'Syncing Bitcoin headers' | tail -1 | cut -c1-160 || true
[ "${panics}" = 0 ] || fail=1

if [ "${fail}" = 0 ]; then
	echo "BOOT PASS (${NETWORK}, ${BOOT_SECS}s)"
else
	echo "BOOT FAIL (${NETWORK})"
	dc logs --no-log-prefix --tail 10 stacks-blockchain 2>&1 | cut -c1-300 | sed 's/^/  node: /'
	exit 1
fi
