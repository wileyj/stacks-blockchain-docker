#!/usr/bin/env bash
# Compose-only boot test (refactor/14 §2.1 R7): no manage.sh, no CLI. In a copy of the repo:
#   cp sample.env .env, set NETWORK/COMPOSE_PROFILES, `docker compose up -d`, hold BOOT_SECS,
#   then `docker compose --profile '*' down` and check the node stopped cleanly.
# Pass: the config jobs succeed, postgres and the API become healthy (when enabled), every service
# stays up with no restarts and the node logs no panic. Sync is not required (slow testnet BTC peer).
#
# Runs as compose project `sbd-compose`; an override renames the containers and network and drops the
# host ports, so it never collides with a real stack on the same host.
#
# Usage: .github/ci/compose-boot.sh [network] [profiles] [boot_secs]   (defaults: testnet node,api 180)
#   BOOT_ENV="KEY=value ..." appends to .env; KEEP_STACK=1 leaves the stack running for debugging.
set -euo pipefail

REPO="$(cd -- "$(dirname "${0}")/../.." >/dev/null 2>&1 ; pwd -P)"
NETWORK="${1:-testnet}"
PROFILES="${2:-node,api}"
BOOT_SECS="${3:-180}"
P=sbd-compose
TMP="$(mktemp -d)"
source "${REPO}/.github/ci/lib.sh"
WORK="${TMP}/repo"

copy_repo "${WORK}"
cp "${WORK}/sample.env" "${WORK}/.env"
{
	echo "NETWORK=${NETWORK}"
	echo "COMPOSE_PROFILES=${PROFILES}"
	echo "SIGNER_PRIVATE_KEY=${TEST_SIGNER_KEY}"
	echo "TESTNET_SIGNER_PRIVATE_KEY=${TEST_SIGNER_KEY}"
	echo "DOCKER_NETWORK=${P}"
	# BOOT_ENV="KEY=value ..." adds settings, e.g. a faster BTC peer
	for kv in ${BOOT_ENV:-}; do echo "${kv}"; done
} >> "${WORK}/.env"

cat > "${TMP}/override.yaml" <<EOF
services:
  postgres: {container_name: ${P}-postgres}
  stacks-blockchain-api: {container_name: ${P}-stacks-blockchain-api, ports: !reset []}
  stacks-blockchain: {container_name: ${P}-stacks-blockchain, ports: !reset []}
  stacks-signer: {container_name: ${P}-stacks-signer, ports: !reset []}
  nginx: {container_name: ${P}-nginx, ports: !reset []}
EOF
# Plain compose from the repo root; only -p and the rename override differ from a user's `up -d`
dc() { (cd "${WORK}" && docker compose -p "${P}" -f compose.yaml -f "${TMP}/override.yaml" "$@"); }
cleanup() {
	# KEEP_STACK=1 leaves the stack and its temp dir for debugging (remove with the printed command)
	if [ "${KEEP_STACK:-0}" = 1 ]; then
		echo "kept: (cd ${WORK} && docker compose -p ${P} -f compose.yaml -f ${TMP}/override.yaml --profile '*' down -v)"
		return
	fi
	dc --profile '*' down -v -t 30 >/dev/null 2>&1 || true
	docker run --rm -v "${WORK}:/w" alpine:3.20 rm -rf /w/persistent-data >/dev/null 2>&1 || true
	rm -rf "${TMP}"
}
trap cleanup EXIT

echo "== ${NETWORK} ${PROFILES}: docker compose up -d"
# `up` fails by itself when a config job fails; the per-job checks below say which one
dc up -d --quiet-pull > "${TMP}/up.log" 2>&1 || echo "docker compose up failed: $(grep -iE 'error|did not complete|didn.t complete' "${TMP}/up.log" | tail -1)"
dc logs --no-log-prefix config 2>&1 | sed 's/^/  /'

fail=0
for job in config config-check signer-config-check; do
	cid=$(dc ps -aq "${job}" 2>/dev/null || true)
	[ -n "${cid}" ] || continue
	code=$(docker inspect -f '{{.State.ExitCode}}' "${cid}")
	echo "${job}: exit=${code}"
	[ "${code}" = 0 ] || { fail=1; dc logs --no-log-prefix "${job}" 2>&1 | tail -5 | cut -c1-300 | sed 's/^/  /'; }
done
[ "${fail}" = 0 ] || { echo "COMPOSE BOOT FAIL (config)"; exit 1; }

echo "== hold ${BOOT_SECS}s"
sleep "${BOOT_SECS}"
for svc in postgres stacks-blockchain-api stacks-blockchain stacks-signer nginx; do
	cid=$(dc ps -aq "${svc}" 2>/dev/null || true)
	[ -n "${cid}" ] || continue
	read -r state restarts health < <(docker inspect -f '{{.State.Status}} {{.RestartCount}} {{if .State.Health}}{{.State.Health.Status}}{{else}}-{{end}}' "${cid}")
	echo "${svc}: state=${state} restarts=${restarts} health=${health}"
	[ "${state}" = running ] && [ "${restarts}" = 0 ] || fail=1
	# postgres must be healthy by now. The node, and the API's HTTP side (which waits for the node's
	# RPC), may still be in their start period during the initial Bitcoin header sync
	[ "${svc}" = postgres ] && [ "${health}" != healthy ] && fail=1
done
if [ -n "$(dc ps -aq stacks-blockchain 2>/dev/null)" ]; then
	panics=$(dc logs --no-log-prefix stacks-blockchain 2>&1 | grep -cE 'panicked|FATAL' || true)
	echo "node panics=${panics}"
	[ "${panics}" = 0 ] || fail=1
fi

[ "${KEEP_STACK:-0}" = 1 ] && { echo "KEEP_STACK=1: not stopping"; exit "${fail}"; }
echo "== docker compose --profile '*' down"
start=$(date +%s)
dc --profile '*' down 2>&1 | grep -E 'Error|error' || true
echo "down took $(( $(date +%s) - start ))s"

if [ "${fail}" = 0 ]; then
	echo "COMPOSE BOOT PASS (${NETWORK} ${PROFILES}, ${BOOT_SECS}s)"
else
	echo "COMPOSE BOOT FAIL (${NETWORK} ${PROFILES})"
	exit 1
fi
