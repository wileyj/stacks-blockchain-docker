#!/usr/bin/env bash
# Keep the network templates in step with upstream stacks-core `sample/conf/` (refactor/11 Q27).
#
#   tools/sync-upstream-conf.sh [ref]           sync: vendor the upstream follower samples into conf/upstream/
#                                               (with their commit in conf/upstream/SOURCE), regenerate
#                                               conf/<network>/Config.toml.sample and validate them with
#                                               check-config. Review the diff, commit it and open the PR yourself.
#   tools/sync-upstream-conf.sh --check [ref]   read-only: exit 1 when upstream differs from conf/upstream/ or a
#                                               sample is stale. Writes nothing in the repo (used by CI).
#
# ref: an upstream branch or a full commit SHA (default: main). Needs git, curl and docker.
# Tier 0 by design: nothing here pushes, opens PRs or needs a token (refactor/HANDOFF.md).
set -euo pipefail

REPO="$(cd -- "$(dirname "${0}")/.." >/dev/null 2>&1 ; pwd -P)"
UPSTREAM="https://github.com/stacks-network/stacks-core"
RAW="https://raw.githubusercontent.com/stacks-network/stacks-core"
NETWORKS=(mainnet testnet)
IMAGE="stacks-blockchain-docker-config:sync"

CHECK=false
if [ "${1:-}" = "--check" ]; then
	CHECK=true
	shift
fi
REF="${1:-main}"
if [[ "${REF}" =~ ^[0-9a-f]{40}$ ]]; then
	SHA="${REF}"
else
	SHA="$(git ls-remote "${UPSTREAM}" "refs/heads/${REF}" | cut -f1)"
	[ -n "${SHA}" ] || { echo "sync: no upstream branch ${REF}" >&2; exit 2; }
fi
echo "upstream: ${UPSTREAM} ${REF} = ${SHA}"

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT
# Work on a copy of conf/; the repo is only written at the end of a sync
mkdir -p "${TMP}/conf/upstream"
for net in "${NETWORKS[@]}"; do
	mkdir -p "${TMP}/conf/${net}"
	cp "${REPO}/conf/${net}/Config.deployment.toml" "${TMP}/conf/${net}/"
done
[ -f "${REPO}/conf/upstream/SOURCE" ] && cp "${REPO}/conf/upstream/SOURCE" "${TMP}/conf/upstream/"

changed=false
for net in "${NETWORKS[@]}"; do
	f="${net}-follower-conf.toml"
	curl -fsSL "${RAW}/${SHA}/sample/conf/${f}" -o "${TMP}/conf/upstream/${f}"
	cmp -s "${TMP}/conf/upstream/${f}" "${REPO}/conf/upstream/${f}" 2>/dev/null || changed=true
done
# Record the upstream commit only when a vendored file changes, so an unchanged upstream is no diff
if ${changed} || [ ! -f "${TMP}/conf/upstream/SOURCE" ]; then
	printf 'repository=%s\ncommit=%s\nfiles=sample/conf/mainnet-follower-conf.toml sample/conf/testnet-follower-conf.toml\n' \
		"${UPSTREAM}" "${SHA}" > "${TMP}/conf/upstream/SOURCE"
fi
SOURCE_SHA="$(sed -n 's/^commit=//p' "${TMP}/conf/upstream/SOURCE")"

# Generate the samples with the config tool (built from this repo; its build runs the unit tests)
docker build -q -f "${REPO}/tools/config/Dockerfile" -t "${IMAGE}" "${REPO}" >/dev/null
for net in "${NETWORKS[@]}"; do
	docker run --rm -v "${TMP}/conf:/conf" "${IMAGE}" template \
		-upstream "/conf/upstream/${net}-follower-conf.toml" \
		-deployment "/conf/${net}/Config.deployment.toml" \
		-source "stacks-core@${SOURCE_SHA} sample/conf/${net}-follower-conf.toml" \
		-out "/conf/${net}/Config.toml.sample"
done

# Everything this script owns, as generated vs as committed
OWNED=(upstream/SOURCE)
for net in "${NETWORKS[@]}"; do
	OWNED+=("upstream/${net}-follower-conf.toml" "${net}/Config.toml.sample")
done

if ${CHECK}; then
	drift=0
	for p in "${OWNED[@]}"; do
		if ! diff -u "${REPO}/conf/${p}" "${TMP}/conf/${p}" > "${TMP}/diff" 2>&1; then
			echo "DRIFT conf/${p}"
			sed 's/^/    /' "${TMP}/diff"
			drift=1
		fi
	done
	if [ "${drift}" = 0 ]; then
		echo "in sync with upstream ${SHA}"
		exit 0
	fi
	echo "conf/ differs from upstream ${REF} (${SHA}). Run tools/sync-upstream-conf.sh, review, commit and open a PR."
	exit 1
fi

mkdir -p "${REPO}/conf/upstream"
for p in "${OWNED[@]}"; do
	cp "${TMP}/conf/${p}" "${REPO}/conf/${p}"
done
echo "== validating the rendered configs with the pinned images"
"${REPO}/.github/ci/check-config.sh"
echo
git -C "${REPO}" status --short -- conf
echo "Review the diff (git diff conf/), then commit and open a PR."
