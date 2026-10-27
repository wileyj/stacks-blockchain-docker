#!/usr/bin/env bash
# Render Config.toml / Signer.toml with the compose `config` job (in a temp copy of the repo), then
# validate each rendered file with `check-config` from the images pinned in sample.env, for every
# profile set that changes the output. Would have caught D50 (testnet config rejected by the node).
#
# Usage: .github/ci/check-config.sh [network ...]   (default: mainnet testnet)
set -euo pipefail

REPO="$(cd -- "$(dirname "${0}")/../.." >/dev/null 2>&1 ; pwd -P)"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT
source "${REPO}/.github/ci/lib.sh"
NETWORKS=("$@")
[ $# -eq 0 ] && NETWORKS=(mainnet testnet)

NODE_IMAGE="ghcr.io/stacks-network/stacks-core:$(pin STACKS_BLOCKCHAIN_VERSION)"
SIGNER_IMAGE="ghcr.io/stacks-network/stacks-signer:$(pin STACKS_SIGNER_VERSION)"
echo "node image:   ${NODE_IMAGE}"
echo "signer image: ${SIGNER_IMAGE}"

fail=0
check() {
	local label="${1}"; shift
	if out=$("$@" 2>&1); then
		echo "ok   ${label}: $(tail -1 <<<"${out}")"
	else
		echo "FAIL ${label}"
		echo "${out}" | tail -5 | sed 's/^/     /'
		fail=$((fail + 1))
	fi
}

for network in "${NETWORKS[@]}"; do
	for mode in node node,api node,api,signer; do
		work="${TMP}/${network}-${mode//,/-}"
		if ! out=$(render "${work}" "${network}" "${mode}" 2>&1); then
			echo "FAIL ${network} ${mode}: config job"; echo "${out}" | tail -5 | sed 's/^/     /'
			fail=$((fail + 1)); continue
		fi
		check "${network} ${mode} Config.toml" docker run --rm -v "${work}/conf/${network}:/conf:ro" \
			--entrypoint /bin/stacks-node "${NODE_IMAGE}" check-config --config /conf/Config.toml
		if [[ "${mode}" == *signer* ]]; then
			check "${network} ${mode} Signer.toml" docker run --rm -v "${work}/conf/${network}:/conf:ro" \
				--entrypoint /bin/stacks-signer "${SIGNER_IMAGE}" check-config --config /conf/Signer.toml
		fi
	done
done

if [ "${fail}" -ne 0 ]; then
	echo "${fail} failed"
	exit 1
fi
echo "all rendered configs valid"
