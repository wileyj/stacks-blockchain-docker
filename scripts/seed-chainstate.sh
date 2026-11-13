#!/usr/bin/env bash
#
# Seed a node from the Hiro archive (https://archive.hiro.so): one chainstate archive plus one API
# postgres dump taken on the same date.
#
# This is a wrapper around the seed job, kept so the script's name keeps working (owner, 2026-11-13).
# The job is the repo's compose service `seed` (tools/seed, built locally by compose); it selects the
# snapshot from the versions pinned in .env, verifies the downloads, and restores in a container, so
# this needs neither root nor host tools beyond Docker Compose. The same thing without this script:
#
#   docker compose run --rm seed [--dry-run] [--yes] [--keep-downloads] [--chainstate F --pgdump F]
#
# Usage: ./scripts/seed-chainstate.sh [-y|--yes] [--dry-run] [other seed job options]
#   -y, --yes    don't ask before replacing the existing chainstate and API database
#   --dry-run    select the snapshot and print the plan; change nothing
#
# The stack must be stopped first (docker compose --profile '*' down, or sbd stop); the job refuses
# otherwise. Settings: SEED_DOWNLOAD_DIR, SEED_RESTORE_JOBS, SEED_DEFER_INDEXES ... in .env
# (docs/usage.md#seed-from-the-hiro-archive).
set -euo pipefail

REPO="$(cd -- "$(dirname "${0}")/.." >/dev/null 2>&1 ; pwd -P)"
cd "${REPO}"

if ! docker compose version >/dev/null 2>&1; then
	echo "seed-chainstate.sh: Docker Compose v2 is required (docker compose version)" >&2
	exit 1
fi
if [ ! -f .env ]; then
	echo "seed-chainstate.sh: no .env in ${REPO}: cp sample.env .env and set NETWORK first" >&2
	exit 1
fi

args=()
for a in "$@"; do
	case "${a}" in
		-y) args+=("--yes") ;;
		*) args+=("${a}") ;;
	esac
done

exec docker compose run --rm seed "${args[@]}"
