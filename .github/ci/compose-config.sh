#!/usr/bin/env bash
# Validate compose.yaml for every network x profile set with `docker compose config -q`
# (plus the postgres port override). Uses sample.env, never the repo's own .env.
set -euo pipefail

REPO="$(cd -- "$(dirname "${0}")/../.." >/dev/null 2>&1 ; pwd -P)"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT
cp "${REPO}/sample.env" "${TMP}/.env"
USER_ID="$(id -u)"   # manage.sh exports this for the bns job
export USER_ID

fail=0
total=0
check() {
	local label="${1}"; shift
	total=$((total + 1))
	# An unset variable is only a warning to compose; treat it as a failure here.
	if out=$(docker compose --env-file "${TMP}/.env" "$@" config -q 2>&1) && ! grep -q 'variable is not set' <<<"${out}"; then
		echo "ok   ${label}"
	else
		echo "FAIL ${label}"
		echo "${out}" | sed 's/^/     /'
		fail=$((fail + 1))
	fi
}

# Every network x profile set, as COMPOSE_PROFILES in .env would select them; '*' is what `docker compose --profile '*' down` loads
for n in mainnet testnet; do
	export NETWORK="${n}"
	for profiles in node api node,api node,api,signer node,api,signer,proxy signer bns event-replay '*'; do
		export COMPOSE_PROFILES="${profiles}"
		check "root ${n} ${profiles}" -f "${REPO}/compose.yaml"
	done
	COMPOSE_PROFILES=node,api check "root ${n} node,api +postgres port" -f "${REPO}/compose.yaml" -f "${REPO}/compose.postgres-port.yaml"
done
unset COMPOSE_PROFILES

echo "${total} combinations, ${fail} failed"
[ "${fail}" -eq 0 ]
