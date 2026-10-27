#!/usr/bin/env bash
# Validate every network x profile x flag combination with `docker compose config -q`,
# using the same file list manage.sh builds. Runs against sample.env in a temp SCRIPTPATH,
# so the repo's own .env, conf/ and persistent-data/ are never read or written.
set -euo pipefail

REPO="$(cd -- "$(dirname "${0}")/../.." >/dev/null 2>&1 ; pwd -P)"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT
cp "${REPO}/sample.env" "${TMP}/.env"
ln -s "${REPO}/scripts" "${TMP}/scripts"
ln -s "${REPO}/conf" "${TMP}/conf"
export SCRIPTPATH="${TMP}"
USER_ID="$(id -u)"   # manage.sh exports this too
export USER_ID

CF="${REPO}/compose-files"
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

for network in "${CF}"/networks/*.yaml; do
	n=$(basename "${network%.*}")
	case ${n} in
		mainnet) export STACKS_CHAIN_ID="0x00000001" ;;
		testnet) export STACKS_CHAIN_ID="0x80000000" ;;
		*) export STACKS_CHAIN_ID="2147483648" ;;
	esac
	export NETWORK="${n}"
	base=(-f "${CF}/common.yaml" -f "${network}")
	check "${n} stacks-blockchain" "${base[@]}" --profile stacks-blockchain
	for flag in proxy signer postgres; do
		check "${n} stacks-blockchain +${flag}" "${base[@]}" -f "${CF}/extra-services/${flag}.yaml" --profile stacks-blockchain
	done
	check "${n} stacks-blockchain +proxy+signer+postgres" "${base[@]}" \
		-f "${CF}/extra-services/proxy.yaml" -f "${CF}/extra-services/signer.yaml" -f "${CF}/extra-services/postgres.yaml" \
		--profile stacks-blockchain
	check "${n} bns" "${base[@]}" -f "${CF}/extra-services/bns.yaml" --profile bns
	for action in import export; do
		check "${n} event-replay ${action}" "${base[@]}" -f "${CF}/event-replay/api-${action}-events.yaml" --profile event-replay
	done
done

# Root compose.yaml (plain `docker compose` path): every network x profile set, as COMPOSE_PROFILES
# in .env would select them; '*' is what `docker compose --profile '*' down` loads
for n in mainnet testnet; do
	export NETWORK="${n}"
	for profiles in node api node,api node,api,signer node,api,signer,proxy signer '*'; do
		export COMPOSE_PROFILES="${profiles}"
		check "root ${n} ${profiles}" -f "${REPO}/compose.yaml"
	done
done
unset COMPOSE_PROFILES

echo "${total} combinations, ${fail} failed"
[ "${fail}" -eq 0 ]
