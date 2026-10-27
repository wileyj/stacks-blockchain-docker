#!/usr/bin/env bash
#
# Characterization harness for manage.sh
#
# Runs manage.sh inside a throwaway copy of the repo with `docker` and `uname` stubbed,
# and records, per scenario:
#   - stdout/stderr (ANSI stripped, temp paths normalized to <REPO>)
#   - exit code
#   - every docker invocation the script made
#   - filesystem side effects (new / modified files, diffs vs *.sample and vs pristine)
#
# Nothing in the real working tree is touched. No real docker commands are run.
#
# Usage:
#   refactor/harness/run.sh                 # run all scenarios, print to stdout
#   refactor/harness/run.sh <name-substr>   # run matching scenarios only
#   refactor/harness/run.sh --record        # (re)write refactor/harness/golden/*.txt
#   refactor/harness/run.sh --check         # diff current behavior against golden/
#
# Requirements: bash, GNU sed (as `sed` or `gsed`), perl (for timeouts), git, tar, diff.

set -uo pipefail
# Byte-order sorting everywhere: glibc's en_US.UTF-8 collation ignores leading dots, which
# reordered the side-effect listings vs the goldens (recorded with C order on macOS).
export LC_ALL=C

HARNESS_DIR="$(cd -- "$(dirname "${0}")" >/dev/null 2>&1 ; pwd -P)"
REPO_ROOT="$(cd -- "${HARNESS_DIR}/../.." >/dev/null 2>&1 ; pwd -P)"
GOLDEN_DIR="${HARNESS_DIR}/golden"
MODE="print"
FILTER=""
case "${1:-}" in
	--record) MODE="record" ;;
	--check) MODE="check" ;;
	"") ;;
	*) FILTER="${1}" ;;
esac

WORK="$(mktemp -d "${TMPDIR:-/tmp}/sbd-harness.XXXXXX")"
WORK="$(cd -- "${WORK}" && pwd -P)" # resolve symlinks (macOS /var -> /private/var) so path normalization matches pwd -P
trap 'rm -rf "${WORK}"' EXIT
PRISTINE="${WORK}/pristine"
BIN="${WORK}/bin"
mkdir -p "${PRISTINE}" "${BIN}"

# Copy the tracked files from the current working tree (not HEAD), excluding this folder
# Tracked and new (not ignored) files that exist in the working tree, so uncommitted work is tested
( cd "${REPO_ROOT}" && git ls-files --cached --others --exclude-standard | grep -v '^refactor/' \
	| while IFS= read -r f; do [ -e "${f}" ] && printf '%s\n' "${f}"; done | tar -cf - -T - ) | tar -xf - -C "${PRISTINE}"

# --- stubs -----------------------------------------------------------------------------
cat > "${BIN}/docker" <<'EOF'
#!/usr/bin/env bash
# docker stub: record the call, emulate just enough behavior for manage.sh
echo "docker $*" >> "${STUB_LOG}"
case "$*" in
	"compose version") echo "Docker Compose version v2.99.0-stub"; exit 0 ;;
esac
# Containers of the old compose-files/ project (label query) => running if STUB_LEGACY=1
if [[ "$*" == *"label=com.docker.compose.project=compose-files"* ]]; then
	[ "${STUB_LEGACY:-0}" = "1" ] && echo "c0ffee00000f"
	exit 0
fi
# `docker compose ... ps -q` and `docker ps -f name=... -q` => running if STUB_RUNNING=1
if [[ "$*" == *" ps -q"* || "$*" == "ps "* ]]; then
	[ "${STUB_RUNNING:-0}" = "1" ] && echo "c0ffee000001"
	exit 0
fi
# `docker logs event-replay` (the replay job's container) => canned lines selected by STUB_API_LOG keyword(s)
#   import-started | import-done | export-started | export-done   (comma separated)
if [[ "$1" == "logs" && "$2" == "event-replay" && -n "${STUB_API_LOG:-}" ]]; then
	[[ "${STUB_API_LOG}" == *import-started* ]] && echo "Importing raw event requests"
	[[ "${STUB_API_LOG}" == *export-started* ]] && echo "Export started"
	[[ "${STUB_API_LOG}" == *import-done* ]] && echo "Event import and playback successful"
	[[ "${STUB_API_LOG}" == *export-done* ]] && echo "Export successful"
	exit 0
fi
exit 0
EOF
cat > "${BIN}/uname" <<'EOF'
#!/bin/sh
if [ "$1" = "-m" ]; then echo "${STUB_ARCH:-x86_64}"; else /usr/bin/uname "$@"; fi
EOF
chmod +x "${BIN}/docker" "${BIN}/uname"

# manage.sh relies on GNU sed (s///m flag). Prefer gsed if `sed` is BSD.
if ! sed --version >/dev/null 2>&1; then
	if command -v gsed >/dev/null 2>&1; then
		ln -s "$(command -v gsed)" "${BIN}/sed"
	else
		echo "WARNING: GNU sed not found; start/restart/import/export scenarios will reflect BSD sed failure" >&2
	fi
fi

# --- scenarios -------------------------------------------------------------------------
# Format:  name | env (space separated K=V for the stub/run) | .env overrides (K=V ...) | pre-shell | stdin | args
SCENARIOS=(
	"env-autocreate-no-args||||| "
	"cli-no-args||||| "
	"cli-help||||| -h"
	"cli-bad-network||||| -n foo -a start"
	"cli-bad-action||||| -n mainnet -a bogus"
	"cli-missing-action||||| -n mainnet"
	"cli-missing-network-value||||| -a start -n"
	"cli-unknown-arg||||| -n mainnet -a start -x"
	"cli-malformed||||| -n mainnet -a start foo"
	"cli-case-insensitive||||| -n MainNet -a START"
	"start-mainnet||||| -n mainnet -a start"
	"start-mainnet-already-running|STUB_RUNNING=1|||| -n mainnet -a start"
	"start-mainnet-arm64-noninteractive|STUB_ARCH=arm64|||| -n mainnet -a start"
	"start-mainnet-arm64-confirm-yes|STUB_ARCH=arm64|||y| -n mainnet -a start"
	"start-mainnet-signer-nokey||||| -n mainnet -a start -f signer"
	"start-mainnet-signer-proxy||SIGNER_PRIVATE_KEY=deadbeef01||| -n mainnet -a start -f signer,proxy"
	"start-testnet-signer||TESTNET_SIGNER_PRIVATE_KEY=deadbeef02||| -n testnet -a start -f signer"
	"start-mocknet-env-network-mainnet||||| -n mocknet -a start"
	"start-mocknet-env-network-mocknet||NETWORK=mocknet||| -n mocknet -a start"
	"start-flags-unknown-and-case||||| -n mainnet -a start -f foo,PROXY"
	"start-flag-postgres||||| -n mainnet -a start -f postgres"
	"start-expose-postgres||EXPOSE_POSTGRES=true||| -n mainnet -a start"
	"start-flag-bns-rejected||||| -n mainnet -a start -f bns"
	"start-signer-then-plain||SIGNER_PRIVATE_KEY=deadbeef01|./manage.sh -n mainnet -a start -f signer||-n mainnet -a start"
	"stop-import-finished|STUB_RUNNING=1 STUB_API_LOG=import-started,import-done|||| -n mainnet -a stop"
	"stop-mainnet-running|STUB_RUNNING=1|||| -n mainnet -a stop"
	"stop-mocknet-running|STUB_RUNNING=1|||| -n mocknet -a stop"
	"stop-legacy-layout-running|STUB_RUNNING=1 STUB_LEGACY=1|||| -n mainnet -a stop"
	"stop-not-running||||| -n mainnet -a stop"
	"stop-import-in-progress|STUB_RUNNING=1 STUB_API_LOG=import-started|||| -n mainnet -a stop"
	"restart-not-running||||| -n mainnet -a restart -f proxy"
	"status-running|STUB_RUNNING=1|||| -n mainnet -a status"
	"status-stopped||||| -n mainnet -a status"
	"status-standalone||||| status"
	"logs-running|STUB_RUNNING=1|||| -n mainnet -a logs"
	"logs-not-running||||| -n mainnet -a logs"
	"logs-standalone|STUB_RUNNING=1|||| logs"
	"logs-export-running|STUB_RUNNING=1|||| -n mainnet -a logs export"
	"pull||||| -n mainnet -a pull"
	"upgrade-standalone||||| upgrade"
	"import-mainnet||||| -n mainnet -a import"
	"import-mainnet-running|STUB_RUNNING=1|||| -n mainnet -a import"
	"import-mainnet-with-file||| mkdir -p persistent-data/mainnet/event-replay && touch persistent-data/mainnet/event-replay/stacks-node-events.tsv|| -n mainnet -a import"
	"export-testnet||||| -n testnet -a export"
	"export-events-file-unset||STACKS_EXPORT_EVENTS_FILE=||| -n mainnet -a export"
	"bns-undefined||||| -n mainnet -a bns"
	"bns-defined||BNS_IMPORT_DIR=/bns-data||| -n mainnet -a bns"
	"bns-standalone-running|STUB_RUNNING=1|BNS_IMPORT_DIR=/bns-data||| bns"
	"reset-no-data||||| -n mainnet -a reset"
	"reset-with-data-confirm||| mkdir -p persistent-data/mainnet/postgres|y| -n mainnet -a reset"
	"reset-with-data-decline||| mkdir -p persistent-data/mainnet/postgres|n| -n mainnet -a reset"
	"reset-while-running|STUB_RUNNING=1|| mkdir -p persistent-data/mainnet/postgres|| -n mainnet -a reset"
)

trim() { local s="${1}"; s="${s#"${s%%[![:space:]]*}"}"; s="${s%"${s##*[![:space:]]}"}"; printf "%s" "${s}"; }

run_scenario() {
	local name="${1}" stub_env="${2}" env_over="${3}" pre="${4}" stdin_data="${5}" args="${6}"
	local case_dir="${WORK}/case/${name}"
	rm -rf "${case_dir}"; mkdir -p "${case_dir}"
	cp -a "${PRISTINE}/." "${case_dir}/"
	local stub_log="${WORK}/stub-${name}.log"; : > "${stub_log}"

	# .env is pre-created from sample.env (so .env mutations show up as diffs), except for
	# env-autocreate-* scenarios, which exercise manage.sh creating .env itself
	if [[ "${name}" != env-autocreate-* ]]; then
		cp "${case_dir}/sample.env" "${case_dir}/.env"
	fi
	if [ -n "$(trim "${env_over}")" ]; then
		local kv
		for kv in ${env_over}; do
			[[ "${kv}" == *=* ]] || continue
			local k="${kv%%=*}" v="${kv#*=}"
			if grep -qE "^#? ?${k}=" "${case_dir}/.env"; then
				perl -pi -e "s|^#? ?\Q${k}\E=.*|${k}=${v}|" "${case_dir}/.env"
			else
				echo "${k}=${v}" >> "${case_dir}/.env"
			fi
		done
	fi
	# optional setup step (may itself call manage.sh); its docker calls are discarded
	if [ -n "$(trim "${pre}")" ]; then
		( cd "${case_dir}" && env PATH="${BIN}:${PATH}" STUB_LOG="${stub_log}" ${stub_env} \
			perl -e 'alarm 15; exec @ARGV' bash -c "$(trim "${pre}")" </dev/null >/dev/null 2>&1 )
		: > "${stub_log}"
	fi
	# snapshot after setup (so diffs show only what the scenario's manage.sh call did)
	local before="${WORK}/before-${name}"
	rm -rf "${before}"; cp -a "${case_dir}" "${before}"

	local out rc
	out="$(
		cd "${case_dir}" && \
		printf "%s" "${stdin_data}" | \
		env PATH="${BIN}:${PATH}" STUB_LOG="${stub_log}" ${stub_env} \
			perl -e 'alarm 15; exec @ARGV' ./manage.sh ${args} 2>&1
	)"
	rc=$?
	[ "${rc}" -eq 142 ] && rc="142 (TIMEOUT after 15s - script hung)"

	normalize() { sed -e $'s/\x1b\\[[0-9;]*m//g' -e "s#${case_dir}#<REPO>#g" -e "s#${WORK}#<WORK>#g"; }
	{
		echo "=== scenario: ${name}"
		echo "--- args: ./manage.sh $(trim "${args}")"
		[ -n "$(trim "${stub_env}")" ] && echo "--- stub env: $(trim "${stub_env}")"
		[ -n "$(trim "${env_over}")" ] && echo "--- .env overrides: $(trim "${env_over}")"
		[ -n "$(trim "${pre}")" ] && echo "--- pre: $(trim "${pre}")"
		[ -n "${stdin_data}" ] && echo "--- stdin: ${stdin_data}"
		echo "--- exit code: ${rc}"
		echo "--- output (first 30 lines):"
		printf "%s\n" "${out}" | head -30 | normalize
		echo "--- docker calls:"
		grep -v '^docker compose version$' "${stub_log}" | normalize | sed 's/^/  /'
		echo "--- filesystem side effects:"
		( cd "${before}" && find . -type f -o -type d | sort ) > "${WORK}/b.lst"
		( cd "${case_dir}" && find . -type f -o -type d | sort ) > "${WORK}/a.lst"
		comm -13 "${WORK}/b.lst" "${WORK}/a.lst" | sed 's/^/  + /'
		comm -23 "${WORK}/b.lst" "${WORK}/a.lst" | sed 's/^/  - /'
		local f
		while read -r f; do
			[ -f "${case_dir}/${f}" ] || continue
			if [ -f "${before}/${f}" ] && ! cmp -s "${before}/${f}" "${case_dir}/${f}"; then
				echo "  ~ ${f} (modified):"
				diff -u "${before}/${f}" "${case_dir}/${f}" | tail -n +3 | sed 's/^/      /'
			elif [ ! -f "${before}/${f}" ] && [ -f "${case_dir}/${f}.sample" ]; then
				echo "  ~ ${f} (created from ${f}.sample) diff vs sample:"
				diff -u "${case_dir}/${f}.sample" "${case_dir}/${f}" | tail -n +3 | sed 's/^/      /'
			fi
		done < "${WORK}/a.lst"
		echo
	}
}

mkdir -p "${GOLDEN_DIR}"
fail=0
for s in "${SCENARIOS[@]}"; do
	IFS='|' read -r name stub_env env_over pre stdin_data args <<< "${s}"
	[ -n "${FILTER}" ] && [[ "${name}" != *"${FILTER}"* ]] && continue
	result="$(run_scenario "${name}" "${stub_env}" "${env_over}" "${pre}" "${stdin_data}" "${args}")"
	case "${MODE}" in
		print) printf "%s\n" "${result}" ;;
		record) printf "%s\n" "${result}" > "${GOLDEN_DIR}/${name}.txt"; echo "recorded ${name}" ;;
		check)
			if diff -u "${GOLDEN_DIR}/${name}.txt" <(printf "%s\n" "${result}") > "${WORK}/d.txt"; then
				echo "PASS ${name}"
			else
				echo "FAIL ${name}"; cat "${WORK}/d.txt"; fail=1
			fi
			;;
	esac
done
exit "${fail}"
