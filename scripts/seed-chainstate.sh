#!/usr/bin/env bash
#
# Seed a node from the Hiro archive (https://archive.hiro.so): one chainstate archive plus one
# API postgres dump taken on the same date.
#
# Usage: sudo ./scripts/seed-chainstate.sh [-y|--yes] [--dry-run]
#   -y, --yes    don't ask before deleting the existing chainstate and postgres data
#   --dry-run    only select the snapshot and print the plan (no root needed, nothing is changed)
#
# Snapshot selection: the newest date on which both a chainstate archive and an API dump exist and
# are compatible with the pinned versions in .env (same major version and not newer than
# STACKS_BLOCKCHAIN_VERSION / STACKS_BLOCKCHAIN_API_VERSION, dump postgres major not newer than
# POSTGRES_VERSION). Dates are never mixed and `-latest` aliases are never used.
#
# Order of operations: check nothing is running -> select snapshot -> confirm -> download and verify
# both files -> only then empty the existing data dirs (contents only, never the dirs themselves,
# so dedicated mounts keep working) -> extract and restore.
#
# SEED_DOWNLOAD_DIR (default: repo root) sets where the archives are downloaded. A file that is
# already there under the archive's own name is verified and reused; partial files are resumed.
# Only files this run downloaded are deleted at the end.
set -eo pipefail
set -Eo functrace
shopt -s expand_aliases
export PROFILE="stacks-blockchain"
ABS_PATH="$( cd -- "$(dirname "${0}")/.." >/dev/null 2>&1 ; pwd -P )"
export SCRIPTPATH="${ABS_PATH}"
export CONTAINER="postgres_import"
ARCHIVE_URL="https://archive.hiro.so"
ASSUME_YES=false
DRY_RUN=false
IMPORT_STARTED=false
DOWNLOADED=()
ENV_FILE="${SCRIPTPATH}/.env"
# If no .env file exists, copy the sample env and export the vars
if [ ! -f "${ENV_FILE}" ];then
	cp -a "${SCRIPTPATH}/sample.env" "${ENV_FILE}"
fi
source "${ENV_FILE}"
VERBOSE=${VERBOSE:-false}
DOWNLOAD_DIR="${SEED_DOWNLOAD_DIR:-${SCRIPTPATH}}"

# The user who invoked sudo; `who am i` is empty without a tty
CURRENT_USER=${SUDO_USER:-$(who am i | awk '{print $1}')}
# some ttys don't return the expected value here (looking at you, tmux)
if [ ! -n "${CURRENT_USER}" ];then
  CURRENT_USER=$(whoami | awk '{print $1}')
fi
export $CURRENT_USER

COLRED=$'\033[31m' # Red
COLGREEN=$'\033[32m' # Green
COLYELLOW=$'\033[33m' # Yellow
COLBLUE=$'\033[34m' # Blue
COLMAGENTA=$'\033[35m' # Magenta
COLCYAN=$'\033[36m' # Cyan
COLBRRED=$'\033[91m' # Bright Red
COLBOLD=$'\033[1m' # Bold Text
COLRESET=$'\033[0m' # reset color
ERROR="${COLRED}[ Error ]${COLRESET} "
WARN="${COLYELLOW}[ Warn ]${COLRESET} "
INFO="${COLGREEN}[ Success ]${COLRESET} "
EXIT_MSG="${COLRED}[ Exit Error ]${COLRESET} "

alias log="logger"
alias log_error='logger "${ERROR}"'
alias log_warn='logger "${WARN}"'
alias log_info='logger "${INFO}"'
alias log_exit='exit_error "${EXIT_MSG}"'
if ${VERBOSE}; then
	alias log='logger  "$(date "+%D %H:%m:%S")" "Func:${FUNCNAME:-main}" "Line:${LINENO:-null}"'
	alias log_info='logger  "$(date "+%D %H:%m:%S")" "Func:${FUNCNAME:-main}" "Line:${LINENO:-null}" "${INFO}"'
	alias log_warn='logger  "$(date "+%D %H:%m:%S")" "Func:${FUNCNAME:-main}" "Line:${LINENO:-null}" "${WARN}"'
	alias log_error='logger  "$(date "+%D %H:%m:%S")" "Func:${FUNCNAME:-main}" "Line:${LINENO:-null}" "${ERROR}"'
	alias log_exit='exit_error  "$(date "+%D %H:%m:%S")" "Func:${FUNCNAME:-main}" "Line:${LINENO:-null}" "${EXIT_MSG}"'
fi

logger() {
    if ${VERBOSE}; then
        printf "%s %-25s %-10s %-10s %-25s %s\\n" "${1}" "${2}" "${3}" "${DEBUG}" "${4}" "${5}"
    else
        printf "%-25s %s\\n" "${1}" "${2}"
    fi
}

exit_error() {
    if ${VERBOSE}; then
        printf "%s %-25s %-10s %-10s %-25s %s\\n\\n" "${1}" "${2}" "${DEBUG}" "${3}" "${4}" "${5}"
	else
        printf "%-25s %s\\n\\n" "${1}" "${2}"
    fi
    # Only stop the import container if this run started it (never one from another run)
    if ${IMPORT_STARTED}; then
        log "Stopping container ${CONTAINER} before exit"
        eval "docker stop ${CONTAINER} > /dev/null  2>&1"
    fi
    exit 1
}

usage() {
    echo "Usage: sudo ${0} [-y|--yes] [--dry-run]"
    echo "  -y, --yes    don't ask before deleting the existing chainstate and postgres data"
    echo "  --dry-run    only select the snapshot and print the plan (no root needed, nothing is changed)"
    exit 0
}

while [ ${#} -gt 0 ]; do
    case ${1} in
        -y|--yes) ASSUME_YES=true ;;
        --dry-run) DRY_RUN=true ;;
        -h|--help) usage ;;
        *) echo "Unknown argument: ${1}"; usage ;;
    esac
    shift
done

if [[ "$EUID" != 0 ]] && ! ${DRY_RUN}; then
    exit_error "${COLRED}Error${COLRESET} - Script needs to run as root or with sudo"
fi

require_tools() {
    local missing=()
    for tool in curl sha256sum numfmt tar zstd docker; do
        command -v "${tool}" > /dev/null 2>&1 || missing+=("${tool}")
    done
    if [ ${#missing[@]} -gt 0 ]; then
        exit_error "${COLRED}Error${COLRESET} - Missing required tools: ${missing[*]}"
    fi
}

# Refuse to run while the stack (or an import from an earlier run) is up: nothing has been changed yet
check_network() {
	local running=""
	${VERBOSE} && log "Checking if default services are running"
	running=$(docker compose -f "${SCRIPTPATH}/compose-files/common.yaml" --profile ${PROFILE} ps -q 2>/dev/null) || true
	for name in stacks-blockchain stacks-blockchain-api postgres "${CONTAINER}"; do
		running+=$(docker ps -q -f "name=^${name}$" 2>/dev/null) || true
	done
	if [[ -n "${running}" ]]; then
		${VERBOSE} && log "Docker services have a pid"
        log "Stacks Blockchain services (or a previous seed import) are currently running."
        log "  Stop services with: ./manage.sh -n ${NETWORK} -a stop"
        exit_error "  Exiting. No data was changed"
	fi
	${VERBOSE} && log "Docker services have no pid"
	return 0
}

major() { echo "${1%%.*}"; }
# true if version ${1} <= version ${2}
ver_le() { [ "$(printf '%s\n%s\n' "${1}" "${2}" | sort -V | head -1)" = "${1}" ]; }
# true if archive version ${1} can be used with pinned version ${2}: same major, not newer
compatible() { [ "$(major "${1}")" = "$(major "${2}")" ] && ver_le "${1}" "${2}"; }

# Size from a 1-byte ranged GET; archive.hiro.so answers HEAD with 403 (pre-signed redirects)
remote_size() {
    curl -sfL -r 0-0 -D - -o /dev/null "${1}" 2>/dev/null | awk -F/ 'tolower($0) ~ /^content-range:/ { gsub(/\r/, "", $2); print $2 }' | tail -1
}

# select_snapshot: sets SEL_DATE, SEL_CORE, SEL_API, SEL_PG (or exits with the reason)
select_snapshot() {
    local chains dumps dates d f v rest pg api
    local best_core best_api best_pg latest=""
    local pin_pg
    pin_pg=$(major "${POSTGRES_VERSION}")
    log "Reading the archive listing for ${NETWORK}"
    chains=$(curl -fsSL "${ARCHIVE_URL}/${NETWORK}/stacks-blockchain/" | grep -oE "${NETWORK}-stacks-blockchain-[0-9][0-9.]*-[0-9]{8}\.tar\.zst" | sort -u) || true
    dumps=$(curl -fsSL "${ARCHIVE_URL}/${NETWORK}/stacks-blockchain-api-pg/" | grep -oE "stacks-blockchain-api-pg-[0-9]+-[0-9][0-9.]*-[0-9]{8}\.dump" | sort -u) || true
    if [[ -z "${chains}" || -z "${dumps}" ]]; then
        exit_error "${COLRED}Error${COLRESET} - Unable to read the snapshot listing at ${ARCHIVE_URL}/${NETWORK}/"
    fi
    dates=$(grep -oE '[0-9]{8}\.tar\.zst$' <<< "${chains}" | cut -c1-8 | sort -ru)

    SEL_DATE=""
    for d in ${dates}; do
        best_core=""; best_api=""; best_pg=""
        local any_core="" any_api="" any_pg=""
        for f in $(grep -E -- "-${d}\.tar\.zst$" <<< "${chains}"); do
            v=${f#"${NETWORK}-stacks-blockchain-"}; v=${v%"-${d}.tar.zst"}
            { [ -z "${any_core}" ] || ver_le "${any_core}" "${v}"; } && any_core=${v}
            if compatible "${v}" "${STACKS_BLOCKCHAIN_VERSION}" && { [ -z "${best_core}" ] || ver_le "${best_core}" "${v}"; }; then
                best_core=${v}
            fi
        done
        for f in $(grep -E -- "-${d}\.dump$" <<< "${dumps}"); do
            rest=${f#stacks-blockchain-api-pg-}; rest=${rest%"-${d}.dump"}
            pg=${rest%%-*}; api=${rest#*-}
            { [ -z "${any_api}" ] || ver_le "${any_api}" "${api}"; } && { any_api=${api}; any_pg=${pg}; }
            if compatible "${api}" "${STACKS_BLOCKCHAIN_API_VERSION}" && [ "${pg}" -le "${pin_pg}" ] \
                && { [ -z "${best_api}" ] || ver_le "${best_api}" "${api}"; }; then
                best_api=${api}; best_pg=${pg}
            fi
        done
        # Newest date that has both files at all, whatever the versions (for messages)
        if [[ -z "${latest}" && -n "${any_core}" && -n "${any_api}" ]]; then
            latest="${d}: core ${any_core}, API ${any_api}, pg${any_pg}"
            LATEST_CORE=${any_core}; LATEST_API=${any_api}; LATEST_PG=${any_pg}
        fi
        if [[ -n "${best_core}" && -n "${best_api}" ]]; then
            SEL_DATE=${d}; SEL_CORE=${best_core}; SEL_API=${best_api}; SEL_PG=${best_pg}
            break
        fi
    done

    if [[ -z "${SEL_DATE}" ]]; then
        local reason="no date in the archive window has both files at compatible versions"
        if [[ -n "${latest}" && "$(major "${LATEST_CORE}")" != "$(major "${STACKS_BLOCKCHAIN_VERSION}")" ]]; then
            reason="the archive is on core $(major "${LATEST_CORE}").x"
        elif [[ -n "${latest}" && "$(major "${LATEST_API}")" != "$(major "${STACKS_BLOCKCHAIN_API_VERSION}")" ]]; then
            reason="the archive is on API $(major "${LATEST_API}").x"
        elif [[ -n "${latest}" && "${LATEST_PG}" -gt "${pin_pg}" ]]; then
            reason="the dumps need postgres ${LATEST_PG}, POSTGRES_VERSION is ${POSTGRES_VERSION}"
        elif [[ -n "${latest}" ]] && ! ver_le "${LATEST_CORE}" "${STACKS_BLOCKCHAIN_VERSION}"; then
            reason="the snapshots are newer than core ${STACKS_BLOCKCHAIN_VERSION}"
        elif [[ -n "${latest}" ]] && ! ver_le "${LATEST_API}" "${STACKS_BLOCKCHAIN_API_VERSION}"; then
            reason="the snapshots are newer than API ${STACKS_BLOCKCHAIN_API_VERSION}"
        fi
        log_error "No compatible ${NETWORK} snapshot for core ${STACKS_BLOCKCHAIN_VERSION} / API ${STACKS_BLOCKCHAIN_API_VERSION} / pg${pin_pg} (${reason})."
        [[ -n "${latest}" ]] && log "  Latest snapshot ${latest}."
        exit_error "  Update the versions in .env to match a snapshot, or sync from genesis"
    fi

    log "  Selected snapshot ${SEL_DATE}: core ${SEL_CORE}, API ${SEL_API}, pg${SEL_PG}"
    # Newer snapshots are never used, but the pins may simply be old: say so
    if [[ "${latest}" != "${SEL_DATE}:"* || "${LATEST_CORE}" != "${SEL_CORE}" || "${LATEST_API}" != "${SEL_API}" ]]; then
        log_warn "A newer snapshot exists (${latest}) but doesn't match the versions pinned in .env, so it is not used."
        log "  To use it, update STACKS_BLOCKCHAIN_VERSION / STACKS_BLOCKCHAIN_API_VERSION / POSTGRES_VERSION in .env"
    fi
    if [[ "${SEL_CORE}" != "${STACKS_BLOCKCHAIN_VERSION}" || "${SEL_API}" != "${STACKS_BLOCKCHAIN_API_VERSION}" ]]; then
        log_warn "The snapshot is older than the pinned versions (core ${STACKS_BLOCKCHAIN_VERSION}, API ${STACKS_BLOCKCHAIN_API_VERSION})."
        log "  The node and API migrate the data on first start"
    fi
    return 0
}

confirm() {
    ${ASSUME_YES} && return 0
    if [[ ! -t 0 ]]; then
        exit_error "${COLRED}Error${COLRESET} - Not running interactively. Re-run with --yes to confirm. No data was changed"
    fi
    local answer
    read -r -p "${1} [y/N] " answer
    [[ "${answer}" =~ ^[Yy]([Ee][Ss])?$ ]]
}

# checksum_ok <file> <sha256 url>: compare against the published .sha256 (always fetched fresh)
checksum_ok() {
    local local_file=${1}
    local checksum_url=${2}
    local expected actual
    expected=$(curl -fsSL "${checksum_url}" | awk '{print $1}') || exit_error "${COLRED}Error${COLRESET} downloading checksum file ${checksum_url}"
    [[ -n "${expected}" ]] || exit_error "${COLRED}Error${COLRESET} empty checksum file ${checksum_url}"
    log "  Generating sha256 for $(basename "${local_file}") and comparing to: ${expected}"
    actual=$(sha256sum "${local_file}" | awk '{print $1}')
    if [ "${expected}" != "${actual}" ]; then
        log "  published sha256:  ${expected}"
        log "  calculated sha256: ${actual}"
        return 1
    fi
    log "  SHA256 matched for: $(basename "${local_file}")"
    return 0
}

# fetch <url> <dest>: reuse a verified local copy, resume a partial one, else download; then verify
fetch() {
    local url=${1}
    local dest=${2}
    local sha_url="${url%.*}.sha256"
    [[ "${url}" == *.tar.zst ]] && sha_url="${url%.tar.zst}.sha256"
    local size local_size
    size=$(remote_size "${url}")
    log
    log "Checking for ${dest}"
    log "  File download size: $(numfmt --to iec --format "%8.4f" "${size:-0}")"
    if [[ -f "${dest}" ]]; then
        local_size=$(stat -c %s "${dest}")
        if [[ -n "${size}" && "${local_size}" -ge "${size}" ]]; then
            log "  File exists locally at full size. Verifying"
            checksum_ok "${dest}" "${sha_url}" && return 0
            exit_error "${COLRED}Error${COLRESET} ${dest} does not match its published sha256. Delete it and re-run"
        fi
        log "  Partial file exists locally ($(numfmt --to iec "${local_size}")). Resuming"
    fi
    DOWNLOADED+=("${dest}")
    log "  Retrieving: ${url}"
    if command -v aria2c > /dev/null 2>&1; then
        log "  Using aria2c for faster download"
        aria2c \
            -x 10 \
            -s 10 \
            --continue=true \
            --summary-interval=0 \
            --max-tries=10 \
            --retry-wait=5 \
            --dir="$(dirname "${dest}")" \
            --out="$(basename "${dest}")" \
            "${url}" || exit_error "${COLRED}Error${COLRESET} downloading ${url} to ${dest}. No data was changed"
    else
        # --fail: never save an HTTP error body as the archive (D52)
        curl -fL -# \
            -C - \
            --retry 10 \
            --retry-delay 5 \
            "${url}" -o "${dest}" || exit_error "${COLRED}Error${COLRESET} downloading ${url} to ${dest}. No data was changed"
    fi
    checksum_ok "${dest}" "${sha_url}" || exit_error "${COLRED}Error${COLRESET} sha256 mismatch for ${dest}. No data was changed"
    return 0
}

# Remove a directory's contents but keep the directory: it may be a mount point (D48, #152)
empty_dir() {
    mkdir -p "${1}"
    find "${1}" -mindepth 1 -delete || exit_error "${COLRED}Error${COLRESET} emptying ${1}"
}

log "-- seed-chainstate.sh --"
log "  Starting at $(date "+%D %H:%m:%S")"
log "  Using files/methods from https://docs.hiro.so/hiro-archive"
require_tools
check_network
select_snapshot

DATA_DIR="${SCRIPTPATH}/persistent-data/${NETWORK}"
PGDUMP_FILE="stacks-blockchain-api-pg-${SEL_PG}-${SEL_API}-${SEL_DATE}.dump"
PGDUMP_URL="${ARCHIVE_URL}/${NETWORK}/stacks-blockchain-api-pg/${PGDUMP_FILE}"
PGDUMP_DEST="${DOWNLOAD_DIR}/${PGDUMP_FILE}"
CHAINDATA_FILE="${NETWORK}-stacks-blockchain-${SEL_CORE}-${SEL_DATE}.tar.zst"
CHAINDATA_URL="${ARCHIVE_URL}/${NETWORK}/stacks-blockchain/${CHAINDATA_FILE}"
CHAINDATA_DEST="${DOWNLOAD_DIR}/${CHAINDATA_FILE}"

${VERBOSE} && log "  PGDUMP_URL:  ${PGDUMP_URL}"
${VERBOSE} && log "  PGDUMP_DEST:  ${PGDUMP_DEST}"
${VERBOSE} && log "  CHAINDATA_URL: ${CHAINDATA_URL}"
${VERBOSE} && log "  CHAINDATA_DEST: ${CHAINDATA_DEST}"
${VERBOSE} && log "**************************************"

CHAINDATA_SIZE=$(remote_size "${CHAINDATA_URL}")
PGDUMP_SIZE=$(remote_size "${PGDUMP_URL}")
mkdir -p "${DOWNLOAD_DIR}" 2>/dev/null || true
DOWNLOAD_FREE=$(df -B1 --output=avail "${DOWNLOAD_DIR}" 2>/dev/null | tail -1 | tr -d ' ')
log
log "Plan:"
log "  Download to ${DOWNLOAD_DIR} ($(numfmt --to iec "${DOWNLOAD_FREE:-0}") free):"
log "    ${CHAINDATA_URL} ($(numfmt --to iec "${CHAINDATA_SIZE:-0}"))"
log "    ${PGDUMP_URL} ($(numfmt --to iec "${PGDUMP_SIZE:-0}"))"
if [[ -n "${DOWNLOAD_FREE}" && -n "${CHAINDATA_SIZE}" && -n "${PGDUMP_SIZE}" ]] \
    && (( DOWNLOAD_FREE < CHAINDATA_SIZE + PGDUMP_SIZE )); then
    log_warn "Not enough free space in ${DOWNLOAD_DIR} for both files. Set SEED_DOWNLOAD_DIR to a larger volume"
fi
log "  Then DELETE the contents of:"
log "    ${DATA_DIR}/stacks-blockchain"
log "    ${DATA_DIR}/postgres"
log "  and restore the snapshot into them"
if ${DRY_RUN}; then
    log
    log "Dry run: nothing was changed"
    exit 0
fi
if ! confirm "Continue?"; then
    log "Aborted. No data was changed"
    exit 1
fi

fetch "${PGDUMP_URL}" "${PGDUMP_DEST}"
fetch "${CHAINDATA_URL}" "${CHAINDATA_DEST}"

# Both files are downloaded and verified: only now replace the existing data (D01)
log
log "Deleting existing data in ${DATA_DIR}/stacks-blockchain and ${DATA_DIR}/postgres"
empty_dir "${DATA_DIR}/stacks-blockchain"
empty_dir "${DATA_DIR}/postgres"

log
log "Extracting stacks-blockchain chainstate data to: ${DATA_DIR}/stacks-blockchain"
tar -xvf "${CHAINDATA_DEST}" -C "${DATA_DIR}/stacks-blockchain/" || exit_error "${COLRED}Error${COLRESET} extracting stacks-blockchain chainstate data"

log
log "  Chowning data to ${CURRENT_USER}"
chown -R ${CURRENT_USER} "${DATA_DIR}" || exit_error "${COLRED}Error${COLRESET} setting file permissions"

log
log "Importing postgres data"
log "  Starting postgres container: ${CONTAINER}"

IMPORT_STARTED=true
eval "docker run -d --rm --name ${CONTAINER} --shm-size=${PG_SHMSIZE:-256MB} -e POSTGRES_PASSWORD=${PG_PASSWORD} -v ${PGDUMP_DEST}:/tmp/stacks_node_postgres.dump -v ${DATA_DIR}/postgres:/var/lib/postgresql/data postgres:${POSTGRES_VERSION}-alpine > /dev/null  2>&1" || exit_error "${COLRED}Error${COLRESET} starting postgres container"
log "  Sleeping for 15s to give time for Postgres to start"
sleep 15

log
log "Restoring postgres data from ${PGDUMP_DEST}"
# --no-owner/--no-privileges: the dumps carry the archive's roles (e.g. hirosystems), which don't exist here
echo "docker exec ${CONTAINER} sh -c \"pg_restore --username ${PG_USER} --verbose --no-owner --no-privileges --create --dbname postgres /tmp/stacks_node_postgres.dump\"" || exit_error "${COLRED}Error${COLRESET} restoring postgres data"
eval "docker exec ${CONTAINER} sh -c \"pg_restore --username ${PG_USER} --verbose --no-owner --no-privileges --create --dbname postgres /tmp/stacks_node_postgres.dump\"" || exit_error "${COLRED}Error${COLRESET} restoring postgres data"
log "Setting postgres user password from .env for ${PG_USER}"
echo "docker exec -i ${CONTAINER} sh -c \"psql -U ${PG_USER} -c \\\"ALTER USER ${PG_USER} PASSWORD '${PG_PASSWORD}';\\\"\" " || exit_error "${COLRED}Error${COLRESET} setting postgres password for ${PG_USER}"
eval "docker exec -i ${CONTAINER} sh -c \"psql -U ${PG_USER} -c \\\"ALTER USER ${PG_USER} PASSWORD '${PG_PASSWORD}';\\\"\" " || exit_error "${COLRED}Error${COLRESET} setting postgres password for ${PG_USER}"

if [[ ${PG_DATABASE} != "stacks_blockchain_api" && ${PG_SCHEMA} != "stacks_blockchain_api" ]];then
    log "dropping restored schema stacks_blockchain_api.public"
    echo "docker exec -i ${CONTAINER} sh -c \"psql -U postgres -d stacks_blockchain_api -c \\\"drop SCHEMA if exists public;\\\"\" " || exit_error "${COLRED}Error${COLRESET} dropping schema public"
    eval "docker exec -i ${CONTAINER} sh -c \"psql -U postgres -d stacks_blockchain_api -c \\\"drop SCHEMA if exists public;\\\"\" " || exit_error "${COLRED}Error${COLRESET} dropping schema public"

    log "altering restored schema stacks_blockchain_api -> ${PG_SCHEMA:-public}"
    echo "docker exec -i ${CONTAINER} sh -c \"psql -U postgres -d stacks_blockchain_api -c \\\"ALTER SCHEMA stacks_blockchain_api RENAME TO ${PG_SCHEMA:-public};\\\"\" " || exit_error "${COLRED}Error${COLRESET} altering schema stacks_blockchain_api -> ${PG_SCHEMA:-public}"
    eval "docker exec -i ${CONTAINER} sh -c \"psql -U postgres -d stacks_blockchain_api -c \\\"ALTER SCHEMA stacks_blockchain_api RENAME TO ${PG_SCHEMA:-public};\\\"\" " || exit_error "${COLRED}Error${COLRESET} altering schema stacks_blockchain_api -> ${PG_SCHEMA:-public}"
fi
if [[ ${PG_DATABASE} == "postgres" ]];then
    log "dropping db ${PG_DATABASE}"
    echo "docker exec -i ${CONTAINER} sh -c \"psql -U postgres -d template1 -c \\\"DROP database ${PG_DATABASE};\\\"\" " || exit_error "${COLRED}Error${COLRESET} dropping db ${PG_DATABASE}"
    eval "docker exec -i ${CONTAINER} sh -c \"psql -U postgres -d template1 -c \\\"DROP database ${PG_DATABASE};\\\"\" " || exit_error "${COLRED}Error${COLRESET} dropping db ${PG_DATABASE}"
fi
if [[ ${PG_DATABASE} != "stacks_blockchain_api" ]]; then
    log "renaming db stacks_blockchain_api to ${PG_DATABASE}"
    echo "docker exec -i ${CONTAINER} sh -c \"psql -U postgres -d template1 -c \\\"ALTER DATABASE stacks_blockchain_api RENAME TO ${PG_DATABASE};\\\"\" "|| exit_error "${COLRED}Error${COLRESET} renaming db stacks_blockchain_api to ${PG_DATABASE}"
    eval "docker exec -i ${CONTAINER} sh -c \"psql -U postgres -d template1 -c \\\"ALTER DATABASE stacks_blockchain_api RENAME TO ${PG_DATABASE};\\\"\" "|| exit_error "${COLRED}Error${COLRESET} renaming db stacks_blockchain_api to ${PG_DATABASE}"
fi
log "Stopping postgres container"
eval "docker stop ${CONTAINER} > /dev/null  2>&1" || exit_error "${COLRED}Error${COLRESET} stopping postgres container ${CONTAINER}"
IMPORT_STARTED=false

# Only delete what this run downloaded; archives placed in SEED_DOWNLOAD_DIR by the user are kept
log "Deleting downloaded archive files"
for f in "${DOWNLOADED[@]}"; do
    rm -f "${f}" || exit_error "${COLRED}Error${COLRESET} deleting ${f}"
done

log "Exiting successfully at $(date "+%D %H:%m:%S")"
exit 0
