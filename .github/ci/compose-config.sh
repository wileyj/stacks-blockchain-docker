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
	for profiles in node api node,api node,api,signer node,api,signer,proxy signer node,api,monitoring bns seed '*'; do
		export COMPOSE_PROFILES="${profiles}"
		check "root ${n} ${profiles}" -f "${REPO}/compose.yaml"
	done
	COMPOSE_PROFILES=node,api check "root ${n} node,api +postgres port" -f "${REPO}/compose.yaml" -f "${REPO}/compose.postgres-port.yaml"
done
unset COMPOSE_PROFILES

# refactor/12 acceptance 5: no secret from .env reaches a monitoring container. postgres-exporter needs
# the database credentials and Grafana its own admin password; nothing else may carry a secret.
total=$((total + 1))
secrets_env="$(mktemp)"
{ cat "${TMP}/.env"; echo "SIGNER_PRIVATE_KEY=sbd-secret-signer"; echo "TESTNET_SIGNER_PRIVATE_KEY=sbd-secret-tsigner"
  echo "AUTH_TOKEN=sbd-secret-auth"; echo "BTC_RPC_PASS=sbd-secret-btc"; echo "GRAFANA_ADMIN_PASSWORD=sbd-secret-grafana"; } > "${secrets_env}"
leaks=$(NETWORK=mainnet COMPOSE_PROFILES='*' docker compose --env-file "${secrets_env}" -f "${REPO}/compose.yaml" config --format json \
	| python3 -I -c '
import sys, json
svc = json.load(sys.stdin)["services"]
allowed = {"grafana": {"sbd-secret-grafana"}}
for name in ("prometheus", "grafana", "cadvisor", "node-exporter", "postgres-exporter"):
    env = svc.get(name, {}).get("environment") or {}
    for k, v in env.items():
        if v and "sbd-secret-" in v and v not in allowed.get(name, set()):
            print(f"{name}: {k}")
')
rm -f "${secrets_env}"
if [ -z "${leaks}" ]; then
	echo "ok   monitoring containers carry no .env secrets"
else
	echo "FAIL monitoring containers carry .env secrets:"; echo "${leaks}" | sed 's/^/     /'
	fail=$((fail + 1))
fi

echo "${total} combinations, ${fail} failed"
[ "${fail}" -eq 0 ]
