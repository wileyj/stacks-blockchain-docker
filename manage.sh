#!/usr/bin/env bash

set -eo pipefail
set -Eo functrace
shopt -s expand_aliases

# The following values can be overridden in the .env file or cmd line. adding some defaults here
export NETWORK="mainnet"
export ACTION=""
export PROFILE="stacks-blockchain"
SIGNER=false
STACKS_SHUTDOWN_TIMEOUT=1200 # default to 20 minutes, during sync it can take a long time to stop the runloop
LOG_TAIL="100"
FLAGS="proxy"
LOG_OPTS="-f --tail ${LOG_TAIL}"
VERBOSE=false

# # Base colors
# COLBLACK=$'\033[30m' # Black
COLRED=$'\033[31m' # Red
COLGREEN=$'\033[32m' # Green
COLYELLOW=$'\033[33m' # Yellow
COLBLUE=$'\033[34m' # Blue
COLMAGENTA=$'\033[35m' # Magenta
COLCYAN=$'\033[36m' # Cyan
# COLWHITE=$'\033[37m' # White

# # Bright colors
COLBRRED=$'\033[91m' # Bright Red
# COLBRGREEN=$'\033[92m' # Bright Green
# COLBRYELLOW=$'\033[93m' # Bright Yellow
# COLBRBLUE=$'\033[94m' # Bright Blue
# COLBRMAGENTA=$'\033[95m' # Bright Magenta
# COLBRCYAN=$'\033[96m' # Bright Cyan
# COLBRWHITE=$'\033[97m' # Bright White

# # Text formatting
# COLITALIC=$'\033[3m' # Italic
# COLUNDERLINE=$'\033[4m' # underline
# COLITALIC=$'\033[3m' # italic
# COLUNDERLINE=$'\033[4m' # underline
COLBOLD=$'\033[1m' # Bold Text

# # Text rest to default
COLRESET=$'\033[0m' # reset color


ERROR="${COLRED}[ Error ]${COLRESET} "
WARN="${COLYELLOW}[ Warn ]${COLRESET} "
INFO="${COLGREEN}[ Success ]${COLRESET} "
EXIT_MSG="${COLRED}[ Exit Error ]${COLRESET} "
DEBUG="[ DEBUG ] "

# Use .env in the local dir
#     - This var is also used in the docker compose yaml files
ABS_PATH="$( cd -- "$(dirname "${0}")" >/dev/null 2>&1 ; pwd -P )"
export SCRIPTPATH=${ABS_PATH}
ENV_FILE="${SCRIPTPATH}/.env"

# If no .env file exists, copy the sample env and export the vars
if [ ! -f "${ENV_FILE}" ];then
	cp -a "${SCRIPTPATH}/sample.env" "${ENV_FILE}"
fi
source "${ENV_FILE}"


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
    exit 1
}

# Populate hardcoded list of default services for shutdown order and log files export
${VERBOSE} && log "Creating list of default services"
DEFAULT_SERVICES=(
	stacks-blockchain
	stacks-blockchain-api
	postgres
)
${VERBOSE} && log "DEFAULT_SERVICES: ${DEFAULT_SERVICES[*]}"

# The stack is the root compose.yaml (plain `docker compose` works too); this script wraps it.
#     - flags map to compose profiles (signer, proxy, bns) or the postgres port override file
COMPOSE_FILE_MAIN="${SCRIPTPATH}/compose.yaml"
COMPOSE_FILE_PG_PORT="${SCRIPTPATH}/compose.postgres-port.yaml"
# Containers of a stack started from the old compose-files/ layout (a different compose project)
LEGACY_PROJECT="compose-files"
SUPPORTED_FLAGS=(signer proxy postgres bns)
${VERBOSE} && log "SUPPORTED_FLAGS: ${SUPPORTED_FLAGS[*]}"
SUPPORTED_NETWORKS=(mainnet testnet)
${VERBOSE} && log "SUPPORTED_NETWORKS: ${SUPPORTED_NETWORKS[*]}"

# Data locations, as in compose.yaml: DATA_ROOT or one path per component
DATA_ROOT="${DATA_ROOT:-${SCRIPTPATH}/persistent-data}"
[[ "${DATA_ROOT}" == /* ]] || DATA_ROOT="${SCRIPTPATH}/${DATA_ROOT#./}"

# Hardcoded list of supported actions this script accepts
${VERBOSE} && log "Defining hardcoded list of supported actions"
SUPPORTED_ACTIONS=(
	up
	start
	down
	stop
	restart
    log
	logs
	import
	export
	upgrade
	pull
	status
	reset
	bns
)
${VERBOSE} && log "SUPPORTED_ACTIONS: ${SUPPORTED_ACTIONS[*]}"


# Print usage with some examples
usage() {
	echo
	log "Usage:"
	log "    ${0} -n <network> -a <action> <optional args>"
	log "        -n|--network: [ mainnet | testnet ]"
	log "        -a|--action: [ start | stop | logs | reset | upgrade | import | export | bns ]"
	log "    optional args:"
	log "        -f|--flags: [ signer,proxy,postgres ]"
	log "        export: combined with 'logs' action, exports logs to a text file"
	log "    ex: ${COLCYAN}${0} -n mainnet -a start -f proxy${COLRESET}"
	log "    ex: ${COLCYAN}${0} -n mainnet -a start -f signer,proxy${COLRESET}"
	log "    ex: ${COLCYAN}${0} --network mainnet --action start --flags proxy${COLRESET}"
	log "    ex: ${COLCYAN}${0} -n mainnet -a logs export${COLRESET}"
	echo
	exit 0
}

# Function to ask for confirmation. Loop until valid input is received
confirm() {
	# y/n confirmation. loop until valid response is received
	while true; do
		read -r -n 1 -p "${1:-Continue?} [y/n]: " REPLY
		case ${REPLY} in
			[yY]) echo ; return 0 ;;
			[nN]) echo ; return 1 ;;
			*) printf "\\033[31m %s \\n\\033[0m" "invalid input"
		esac
	done
}

# Function to check for a valid flag (exists in provided arg of array)
#     - arrays are provided as args
check_flags() {
	local array="${1}"
	local element="${2}"
	${VERBOSE} && log "array: ${1}"
	${VERBOSE} && log "element: ${element}"
	for i in ${array}; do
		if [[ ${i} == "${element}" ]]; then
			return 0
		fi
	done
	return 1
}

# Check if we're on a Mac M1 - Docker IO is not ideal yet, and we're IO heavy
#     - Confirm if user really wants to run this on an M1
check_device() {
	# Check if we're on a M1 Mac - Disk IO is not ideal on this platform
	if [[ $(uname -m) == "arm64" ]]; then
		echo
		log_warn "⚠️  ${COLYELLOW}WARNING${COLRESET}"
		log_warn "⚠️  MacOS M1 CPU detected - NOT recommended for this repo"
		log_warn "⚠️  see README for details"
		log_warn "⚠️      https://github.com/stacks-network/stacks-blockchain-docker/blob/master/docs/requirements.md#macos-with-an-m1-processor-is-not-recommended-for-this-repo"
		confirm "Continue Anyway?" || exit_error "${COLRED}Exiting${COLRESET}"
	fi
}

# # Try to detect a breaking (major version change) in the API by comparing local version to .env definition
# # Return non-zero if a breaking change is detected (this logic is suspect, but should be ok)
# check_api(){
# 	# Try to detect if there is a breaking API change based on major version change
# 	${VERBOSE} && log "Checking API version for potential breaking change"
# 	if [ "${PROFILE}" != "event-replay" ]; then
# 		CURRENT_API_VERSION=$(docker images --format "{{.Tag}}" blockstack/stacks-blockchain-api  | cut -f 1 -d "." | head -1)
# 		CONFIGURED_API_VERSION=$( echo "${STACKS_BLOCKCHAIN_API_VERSION}" | cut -f 1 -d ".")
# 		${VERBOSE} && log "CURRENT_API_VERSION: ${CURRENT_API_VERSION}"
# 		${VERBOSE} && log "CONFIGURED_API_VERSION: ${CONFIGURED_API_VERSION}"
# 		if [ "${CURRENT_API_VERSION}" != "" ]; then
# 			if [ "${CURRENT_API_VERSION}" -lt "${CONFIGURED_API_VERSION}" ];then
# 				echo
# 				log_warn "${COLBOLD}stacks-blockchain-api contains a breaking schema change${COLRESET} ( Version: ${COLYELLOW}${STACKS_BLOCKCHAIN_API_VERSION}${COLRESET} )"
# 				return 0
# 			fi
# 		fi
# 	fi
# 	${VERBOSE} && log "No schema-breaking change detected"
# 	return 1
# }

# Containers still running from the old compose-files/ layout (another compose project)
legacy_containers() {
	docker ps -q --filter "label=com.docker.compose.project=${LEGACY_PROJECT}"
}

# Check if services are running
check_network() {
	${VERBOSE} && log "Checking if default services are running"
	# Determine if any service of the stack (any profile) or of the old layout is running
	if [[ $(docker compose -f "${COMPOSE_FILE_MAIN}" --profile '*' ps -q) || $(legacy_containers) ]]; then
		${VERBOSE} && log "Docker services have a pid"
		# Docker is running, return success
		return 0
	fi
	${VERBOSE} && log "Docker services have no pid"
	# Docker is not running, return fail
	return 1
}

# Check if there is an event-replay operation in progress
check_event_replay(){
	${VERBOSE} && log "Checking status of API event-replay"
	##
	## Check if import has started and save return code
	if [[ "${ACTION}" == "export" || "${ACTION}" == "import" ]]; then
		log "${ACTION} Checking for an active event-replay import"
	fi
	eval "docker logs event-replay 2>&1 | head -n20 | grep -q 'Importing raw event requests'" || test ${?} -eq 141
	check_import_started="${?}"
	${VERBOSE} && log "check_import_started: ${check_import_started}"
	##
	## Check if import has completed and save return code
	if [[ "${ACTION}" == "export" || "${ACTION}" == "import" ]]; then
		log "${ACTION} Checking for a completed event-replay import"
	fi
	eval "docker logs event-replay --tail 20 2>&1 | grep -q 'Event import and playback successful'" || test ${?} -eq 141
	check_import_finished="${?}"
	${VERBOSE} && log "check_import_finished: ${check_import_finished}"
	##
	## Check if export has started and save return code
	if [[ "${ACTION}" == "export" || "${ACTION}" == "import" ]]; then
		log "${ACTION} Checking for an active event-replay export"
	fi
	eval "docker logs event-replay 2>&1 | head -n20 | grep -q 'Export started'" || test ${?} -eq 141
	check_export_started="${?}"
	${VERBOSE} && log "check_export_started: ${check_export_started}"
	##
	## Check if export has completed and save return code
	if [[ "${ACTION}" == "export" || "${ACTION}" == "import" ]]; then
		log "${ACTION} Checking for a completed event-replay export"
	fi
	eval "docker logs event-replay --tail 20 2>&1 | grep -q 'Export successful'" || test ${?} -eq 141
	check_export_finished="${?}"
	${VERBOSE} && log "check_export_finished: ${check_export_finished}"

	if [ "${check_import_started}" -eq "0" ]; then
		# Import has started
		${VERBOSE} && log "import has started"
		if [ "${check_import_finished}" -eq "0" ]; then
			# Import has finished
			log "Event import and playback has finished"
			${VERBOSE} && log "import has finished, return 0"
			return 0
		fi
		# Import hasn't finished, return 1
		log_warn "Event import and playback is in progress"
		${VERBOSE} && log "import has not finished, return 1"
		return 1
	fi
	if [ "${check_export_started}" -eq "0" ]; then
		# Export has started
		${VERBOSE} && log "export has started"
		if [ "${check_export_finished}" -eq "0" ]; then
			# Export has finished
			log "Event export has finished"
			${VERBOSE} && log "export has finished, return 0"
			return 0
		fi
		# Export hasn't finished, return 1
		log_warn "Event export is in progress"
		${VERBOSE} && log "export has not finished, return 1"
		return 1
	fi
	${VERBOSE} && log "No event-replay in progress"
	# Default return success - event-replay is not running
	return 0
}

# Determine if a supplied container name is running
check_container() {
	local container="${1}"
	${VERBOSE} && log "Checking if container ${container} is running"
    if [ "$(docker ps -f name="^${container}"$ -q)" ]; then
        # Container is running, return success
		${VERBOSE} && log "${container} is running, return 0"
		return 0
	fi
    # Container is not running return fail
	${VERBOSE} && log "${container} is running, return 1"
	return 1
}

# # Check if BNS_IMPORT_DIR is defined, and if the directory exists/not empty
check_bns() {
	if [ "${BNS_IMPORT_DIR}" ]; then
		${VERBOSE} && log "Defined BNS_IMPORT_DIR var"
		local file_list=()
		if [ -d "${DATA_ROOT}${BNS_IMPORT_DIR}" ]; then
			${VERBOSE} && log "Found existing BNS_IMPORT_DIR directory"
			for file in "${DATA_ROOT}${BNS_IMPORT_DIR}"/*; do
				file_base=$(basename "${file%.*}")
				file_list+=("$file_base")
			done
			for item in "${BNS_FILES[@]}"; do
				if ! check_flags "${file_list[*]}" "$item"; then
					return 1
				fi
			done
		fi
	fi
	return 0
}

# Build the compose arguments for a manage.sh profile and flags (sets COMPOSE_ARGS)
#     - stacks-blockchain: profiles node + api, plus signer/proxy from the flags
#     - event-replay / bns: the job's own profile
#     - all: every profile, used to stop, follow logs and pull
#     - the postgres flag (or EXPOSE_POSTGRES=true) adds the port override file
compose_args() {
	local flags="${1}"
	local profile="${2}"
	local profiles=()
	case ${profile} in
		stacks-blockchain)
			profiles=(node api)
			for flag in ${flags}; do
				case ${flag} in
					signer|proxy) profiles+=("${flag}") ;;
				esac
			done
			;;
		all) profiles=("'*'") ;;
		*) profiles=("${profile}") ;;
	esac
	COMPOSE_ARGS="--env-file ${ENV_FILE} -f ${COMPOSE_FILE_MAIN}"
	if [[ " ${flags} " == *" postgres "* || "${EXPOSE_POSTGRES}" == "true" ]]; then
		COMPOSE_ARGS="${COMPOSE_ARGS} -f ${COMPOSE_FILE_PG_PORT}"
	fi
	for p in "${profiles[@]}"; do
		COMPOSE_ARGS="${COMPOSE_ARGS} --profile ${p}"
	done
	${VERBOSE} && log "COMPOSE_ARGS: ${COMPOSE_ARGS}"
	true
}

# Data dirs for NETWORK, resolved like compose.yaml does (sets DATA_DIRS)
data_dirs() {
	DATA_DIRS=(
		"${STACKS_DATA_DIR:-${DATA_ROOT}/${NETWORK}/stacks-blockchain}"
		"${POSTGRES_DATA_DIR:-${DATA_ROOT}/${NETWORK}/postgres}"
		"${SIGNER_DATA_DIR:-${DATA_ROOT}/${NETWORK}/stacks-signer}"
		"${EVENT_REPLAY_DIR:-${DATA_ROOT}/${NETWORK}/event-replay}"
	)
}

# Stop a stack started from the old compose-files/ layout: the node first (it may take up to
# STACKS_SHUTDOWN_TIMEOUT to finish its run loop), then its observers and the rest
legacy_stop() {
	[[ $(legacy_containers) ]] || return 0
	log "Stopping services started with the previous compose-files layout"
	for service in stacks-blockchain stacks-signer stacks-blockchain-api postgres nginx; do
		if check_container "${service}"; then
			log "${COLBOLD}Stopping ${service}${COLRESET}"
			local timeout=""
			[ "${service}" == "stacks-blockchain" ] && timeout="-t ${STACKS_SHUTDOWN_TIMEOUT}"
			eval "docker stop ${timeout} ${service}"
		fi
	done
	eval "docker rm \$(docker ps -aq --filter label=com.docker.compose.project=${LEGACY_PROJECT})" >/dev/null 2>&1 || true
	# The new project uses the same network name, which compose won't adopt from another project
	eval "docker network rm \$(docker network ls -q --filter label=com.docker.compose.project=${LEGACY_PROJECT})" >/dev/null 2>&1 || true
}

# Configure options to bring services up
docker_up() {
	if check_network "${PROFILE}"; then
		echo
		log_exit "Stacks Blockchain services are already running"
	fi
	if ! check_event_replay; then
		log_exit "Event-replay in progress. Refusing to start services"
	fi

	# Set signer env based on flag
	if [[ "${FLAGS_ARRAY[*]}" == *"signer"* ]]; then
		SIGNER=true
	fi
	# The config job also refuses an empty key; failing here writes nothing at all
	if ${SIGNER}; then
		local key="${SIGNER_PRIVATE_KEY}"
		[ "${NETWORK}" == "testnet" ] && key="${TESTNET_SIGNER_PRIVATE_KEY}"
		[ "${key}" ] || log_exit "Signer private key not set!"
	fi

	# Sanity checks before starting services
	local param="-d"
	if [ "${PROFILE}" == "bns" ]; then
		param=""
	fi

	# Create the data dirs as the current user (docker would create missing ones as root).
	#     Config.toml/Signer.toml are rendered by the compose `config` job on every start.
	data_dirs
	for dir in "${DATA_DIRS[@]}"; do
		if [[ ! -d "${dir}" ]]; then
			log "Creating data dir ${COLCYAN}${dir}${COLRESET}"
			mkdir -p "${dir}" >/dev/null 2>&1 || {
				log_exit "Unable to create required dir: ${COLCYAN}${dir}${COLRESET}"
			}
		fi
	done

	${VERBOSE} && log "Copying ${COLCYAN}${ENV_FILE}${COLRESET} -> ${COLCYAN}${ENV_FILE}.save${COLRESET}"
	$(cp -a "${ENV_FILE}" "${ENV_FILE}.save") >/dev/null 2>&1 || {
		log_exit "Unable to copy ${COLCYAN}${ENV_FILE}${COLRESET} -> ${COLCYAN}${ENV_FILE}.save${COLRESET}"
	}
	log "Starting all services for ${COLYELLOW}${PROFILE}${COLRESET}"
	${VERBOSE} && log "calling run_docker function: run_docker \"up\" \"${FLAGS_ARRAY[*]}\" \"${PROFILE}\" \"${param}\""
	run_docker "up" "${FLAGS_ARRAY[*]}" "${PROFILE}" "${param}"
}

# Configure options to bring services down
docker_down() {
	if ! check_network "${PROFILE}"; then
		if [ "${ACTION}" != "restart" ];then
			${VERBOSE} && log "calling status function"
			status
		fi
		return
	fi
	# sanity checks before stopping services
	if ! check_event_replay;then
		log_exit "Event-replay in progress. Refusing to stop services"
	fi
	# A stack started from the old compose-files/ layout is stopped container by container
	${VERBOSE} && log "calling legacy_stop function"
	legacy_stop
	# compose stops the node first (its observers and postgres depend on it outliving them) and
	# gives it stop_grace_period (STACKS_SHUTDOWN_TIMEOUT) to finish its run loop
	log "${COLBOLD}Stopping all services${COLRESET}"
	${VERBOSE} && log "calling run_docker function: run_docker \"down\" \"${FLAGS_ARRAY[*]}\" \"all\""
	run_docker "down" "${FLAGS_ARRAY[*]}" "all"
}

# Output the service logs
docker_logs(){
	# Tail docker logs for the last x lines via LOG_TAIL as 'param'
	local param="${1}"
	${VERBOSE} && log "param: ${param}"
	if ! check_network "${PROFILE}"; then
		log_error "No ${COLYELLOW}${NETWORK}${COLRESET} services running"
		usage
	fi
	${VERBOSE} && log "calling run_docker function: run_docker \"logs\" \"\" \"all\" \"${param}\""
	run_docker "logs" "" "all" "${param}"
}

# Export docker logs for the main services to files in ./exported-logs
logs_export(){
	if ! check_network "${PROFILE}"; then
		log_error "No ${COLYELLOW}${NETWORK}${COLRESET} services running"
		usage
	fi
	log "Exporting log data to text file"
	# create exported-logs if it doesn't exist
    if [[ ! -d "${SCRIPTPATH}/exported-logs" ]];then
        log "    - Creating log dir: ${COLCYAN}${SCRIPTPATH}/exported-logs${COLRESET}"
        mkdir -p "${SCRIPTPATH}/exported-logs" >/dev/null 2>&1 || {
			log_exit "Unable to create required dir: ${COLCYAN}${SCRIPTPATH}/exported-logs${COLRESET}"
		}
		${VERBOSE} && log "created logs dir: ${SCRIPTPATH}/exported-logs"
    fi
	${VERBOSE} && log "using existing logs dir: ${SCRIPTPATH}/exported-logs"
	# loop through main services, storing the logs as a text file
    for service in "${DEFAULT_SERVICES[@]}"; do
		if check_container "${service}"; then
			log "    - Exporting logs for ${COLCYAN}${service}${COLRESET} -> ${COLCYAN}${SCRIPTPATH}/exported-logs/${service}.log${COLRESET}"
    	    eval "docker logs ${service} > ${SCRIPTPATH}/exported-logs/${service}.log 2>&1"
		else
			log "    - Skipping export for non-running service ${COLYELLOW}${service}${COLRESET}"
		fi
    done
	log_info "Log export complete"
    exit 0
}

# Pull any updated images that may have been published
docker_pull() {
	${VERBOSE} && log "pulling new images for ${PROFILE}"
	# The config job's image is built locally (on every start), not pulled
	${VERBOSE} && log "calling run_docker function: run_docker \"pull\" \"\" \"all\" \"--ignore-buildable\""
	run_docker "pull" "" "all" "--ignore-buildable"
}

# Check if the services are running
status() {
	if check_network "${PROFILE}"; then
		echo
		log "${COLBOLD}Stacks Blockchain services are running${COLRESET}"
		echo
		${VERBOSE} && echo -e "$(docker compose -f "${COMPOSE_FILE_MAIN}" --profile '*' ps)"
		exit 0
	else
		echo
		log "${COLBOLD}Stacks Blockchain services are not running${COLRESET}"
		echo
		exit 1
	fi
}

# Delete persistent data for NETWORK
#     - empties each data dir but keeps the dir itself: it may be a mount point (D48, #152)
reset_data() {
	data_dirs
	local existing=()
	for dir in "${DATA_DIRS[@]}"; do
		[[ -d "${dir}" ]] && existing+=("${dir}")
	done
	if [ ${#existing[@]} -gt 0 ]; then
		${VERBOSE} && log "Found existing data: ${existing[*]}"
		if ! check_network "${PROFILE}"; then
			log "Data to delete for ${COLYELLOW}${NETWORK}${COLRESET}:"
			for dir in "${existing[@]}"; do
				log "    ${COLCYAN}${dir}${COLRESET}"
			done
			# Exit if operation isn't confirmed
			confirm "Delete Persistent data for ${COLYELLOW}${NETWORK}${COLRESET}?" || log_exit "Delete Cancelled"
			for dir in "${existing[@]}"; do
				${VERBOSE} && log "  Running: find ${dir} -mindepth 1 -delete"
				find "${dir}" -mindepth 1 -delete >/dev/null 2>&1 || {
					# Log error and exit if data wasn't deleted (permission denied etc)
					log_error "Failed to empty ${COLCYAN}${dir}${COLRESET}"
					log_exit "  Re-run the command with sudo: ${COLCYAN}sudo ${0} -n ${NETWORK} -a reset${COLRESET}"
				}
			done
			log_info "Persistent data deleted"
			echo
			exit 0
		else
			# Log error and exit if services are already running
			log_error "Can't reset while services are running"
			log_exit "  Try again after running: ${COLCYAN}${0} -n ${NETWORK} -a stop${COLRESET}"
		fi
	fi
	# No data exists, log error and move on
	log_error "No data exists for ${COLYELLOW}${NETWORK}${COLRESET}"
	${VERBOSE} && log "calling usage function"
	usage
}

# Download V1 BNS data to import via .env file BNS_IMPORT_DIR
download_bns_data() {
	if [ "${BNS_IMPORT_DIR}" ]; then
		${VERBOSE} && log "Using defined BNS_IMPORT_DIR: ${BNS_IMPORT_DIR}"
		if ! check_network "${PROFILE}"; then
			FLAGS_ARRAY=(bns)
			PROFILE="bns"
			${VERBOSE} && log "FLAGS_ARRAY: ${FLAGS_ARRAY[*]}"
			${VERBOSE} && log "PROFILE: ${PROFILE}"
			log "Downloading and extracting V1 bns-data"
			${VERBOSE} && log "calling docker_up function"
			docker_up
			${VERBOSE} && log "calling docker_down function"
			docker_down
			log_info "BNS Download Operation is complete"
			log "    Start the services with: ${COLCYAN}${0} -n ${NETWORK} -a start${COLRESET}"
			exit 0
		fi
		echo
		log_error "Refusing to download BNS data - ${COLBOLD}services need to be stopped first${COLRESET}"
		log_exit "    Stop the services with: ${COLCYAN}${0} -n ${NETWORK} -a stop${COLRESET}"
	fi
	echo
	log_error "Undefined or commented ${COLYELLOW}BNS_IMPORT_DIR${COLRESET} variable in ${COLCYAN}${ENV_FILE}${COLRESET}"
	exit 0
}

# Perform the Hiro API event-replay
event_replay(){
	if [ "${STACKS_BLOCKCHAIN_API_VERSION}" == "5.0.1" ]; then
	 	echo
		log "${COLYELLOW}${COLBOLD}There is an open issue running event-replay with this version (${STACKS_BLOCKCHAIN_API_VERSION}) of the API${COLRESET}"
		log "    https://github.com/hirosystems/stacks-blockchain-api/issues/1336"
		log "For now, use prior version of the API: ${COLBOLD}4.2.1${COLRESET}"
		log "Or sync from genesis using API: ${COLBOLD}5.0.1${COLRESET}"
		echo
		log_exit "${1} not supported for this version of the API"
	fi
	if [ "${STACKS_EXPORT_EVENTS_FILE}" != "" ]; then
		${VERBOSE} && log "Using defined STACKS_EXPORT_EVENTS_FILE: ${STACKS_EXPORT_EVENTS_FILE}"
		local action="${1}"
		# The events file lives in this network's event-replay dir (D09: it was always mainnet's)
		data_dirs
		local tsv_file
		tsv_file="${DATA_DIRS[3]}"/$(basename "${STACKS_EXPORT_EVENTS_FILE}")
		# An import needs the file; an export creates it
		if [[ "${action}" == "import" && ! -f "${tsv_file}" ]]; then
			log_exit "Missing event-replay file: ${COLCYAN}${tsv_file}${COLRESET}"
		fi
		${VERBOSE} && log "Using local event-replay file: ${tsv_file}"
		if check_network "${PROFILE}"; then
			${VERBOSE} && log "calling docker_down function"
			docker_down
		fi
		PROFILE="event-replay"
		FLAGS_ARRAY=()
		# Read by the event-replay service in compose.yaml
		export EVENT_REPLAY_ACTION="${action}"
		${VERBOSE} && log "PROFILE: ${PROFILE}"
		${VERBOSE} && log "EVENT_REPLAY_ACTION: ${EVENT_REPLAY_ACTION}"
		${VERBOSE} && log "calling docker_up function"
		docker_up
		echo
		log "${COLBRRED}${COLBOLD}This operation can take a long while${COLRESET}"
		log "Check logs for completion: ${COLCYAN}${0} -n ${NETWORK} -a logs${COLRESET}"
		if [ "${action}" == "export" ]; then
			log "    - Look for a export log entry: ${COLYELLOW}\"Export successful.\"${COLRESET}"
		fi
		if [ "${action}" == "import" ]; then
			log "    - Look for a import log entry: ${COLYELLOW}\"Event import and playback successful.\"${COLRESET}"
		fi
		log "${COLBOLD}Once the operation is complete${COLRESET}, restart the service with: ${COLCYAN}${0} -n ${NETWORK} -a restart${COLRESET}"
		echo
		exit 0
	fi
	echo
	log_error "Undefined or commented ${COLYELLOW}STACKS_EXPORT_EVENTS_FILE${COLRESET} variable in ${COLCYAN}${ENV_FILE}${COLRESET}"
	exit 0
}

# Execute the docker compose command using provided args
run_docker() {
	local action="${1}"
	local flags="${2}"
	local profile="${3}"
	local param="${4}"
	# compose file(s) and profiles for this profile and flags
	compose_args "${flags}" "${profile}"
	cmd="docker compose ${COMPOSE_ARGS} ${action} ${param}"
	# Log the command we'll be running for verbosity
	${VERBOSE} && log "action: ${action}"
	${VERBOSE} && log "profile: ${profile}"
	${VERBOSE} && log "param: ${param}"
	${VERBOSE} && log "Running: eval ${cmd}"
	# The BNS files are needed by the API on a normal start, not by the bns job that downloads them
	if [ "${action}" == "up" ] && [ "${profile}" == "stacks-blockchain" ]; then
		${VERBOSE} && log "Checking if BNS_IMPORT_DIR is defined and has data"
		if ! check_bns; then
			log "Missing some BNS files"
			log "  run: ${COLCYAN}${0} bns"
			log " -or -"
			log "  comment BNS_IMPORT_DIR in ${COLYELLOW}${ENV_FILE}${COLRESET}"
			exit_error "Exiting"
		fi
	fi
	eval "${cmd}"
	local ret="${?}"
	${VERBOSE} && log "cmd returned: ${ret}"
	# If return is not zero, it should be apparent. if it worked, print how to see the logs
	if [[ "$ret" -eq 0 && "${action}" == "up" && "${profile}" != "bns" ]]; then
		log_info "Brought up ${COLYELLOW}${NETWORK}${COLRESET}"
		log "    Follow logs: ${COLCYAN}${0} -n ${NETWORK} -a logs${COLRESET}"
	fi
	if [[ "$ret" -eq 0 && "${action}" == "down" && "${profile}" != "bns" ]]; then
		log_info "Brought down ${COLYELLOW}${NETWORK}${COLRESET}"
	fi
}

# Check for required binaries, exit if missing
for cmd in docker id; do
	command -v "${cmd}" >/dev/null 2>&1 || log_exit "Missing command: ${cmd}"
done

# Check for docker compose specifically (as a subcommand)
if ! docker compose version >/dev/null 2>&1; then
	log_exit "Missing command: docker compose (Docker Compose v2 required)"
fi

# If no args are provided, print usage
if [[ ${#} -eq 0 ]]; then
	${VERBOSE} && log "No args provided"
	${VERBOSE} && log "calling usage function"
	usage
fi

USER_ID=$(id -u "$(whoami)")
export USER_ID="${USER_ID}"
export DOCKER_NETWORK="${DOCKER_NETWORK}"
${VERBOSE} && log "Exporting USER_ID: ${USER_ID}"
${VERBOSE} && log "Exporting DOCKER_NETWORK: ${DOCKER_NETWORK}"

# loop through the args and try to determine what options we have
#   - simple check for logs/status/upgrade/bns since these are not network dependent
while [ ${#} -gt 0 ]
do
	case ${1} in
	-n|--network)
		# Retrieve the network arg, converted to lowercase
		if [ "${2}" == "" ]; then
			log_error "Missing required value for ${COLYELLOW}${1}${COLRESET}"
			${VERBOSE} && log "calling usage function"
			usage
		fi
		NETWORK=$(echo "${2}" | tr -d ' ' | awk '{print tolower($0)}')
		${VERBOSE} && log "calling check_flags function with (SUPPORTED_NETWORKS: ${SUPPORTED_NETWORKS[*]}) (NETWORK: ${NETWORK})"
		if ! check_flags "${SUPPORTED_NETWORKS[*]}" "${NETWORK}"; then
			log_error "Network (${COLYELLOW}${NETWORK}${COLRESET}) not supported"
			${VERBOSE} && log "calling usage function"
			usage
		fi
		${VERBOSE} && log "Defining NETWORK: ${NETWORK}"
				${VERBOSE} && log "SUPPORTED_NETWORKS: ${SUPPORTED_NETWORKS[*]}"
		shift
		;;
	-a|--action)
		# Retrieve the action arg, converted to lowercase
		if [ "${2}" == "" ]; then
			log_error "Missing required value for ${COLYELLOW}${1}${COLRESET}"
			${VERBOSE} && log "calling usage function"
			usage
		fi
		ACTION=$(echo "${2}" | tr -d ' ' | awk '{print tolower($0)}')
		${VERBOSE} && log "calling check_flags function with (SUPPORTED_ACTIONS: ${SUPPORTED_ACTIONS[*]}) (ACTION: ${ACTION})"
		if ! check_flags "${SUPPORTED_ACTIONS[*]}" "${ACTION}"; then
			log_error "Action (${COLYELLOW}${ACTION}${COLRESET}) not supported"
			${VERBOSE} && log "calling usage function"
			usage
		fi
		${VERBOSE} && log "Defining ACTION: ${ACTION}"
		# If the action is log/logs, we also accept a second option 'export' to save the log output to file
		if [[ "${ACTION}" =~ ^(log|logs)$ && "${3}" == "export" ]]; then
			${VERBOSE} && log "calling logs_export function"
			logs_export
		fi
		shift
		;;
	-f|--flags)
		# Retrieve the flags arg as a comma separated list, converted to lowercase
		# Check against the dynamic list 'FLAGS_ARRAY' which validates against folder contents
		if [ "${2}" == "" ]; then
			log_error "Missing required value for ${COLYELLOW}${1}${COLRESET}"
			${VERBOSE} && log "calling usage function"
			usage
		fi
		FLAGS=$(echo "${2}" | tr -d ' ' | awk '{print tolower($0)}')
		set -f; IFS=','
		FLAGS_ARRAY=("${FLAGS}")
		${VERBOSE} && log "calling check_flags function with (FLAGS_ARRAY: ${FLAGS_ARRAY[*]}) (FLAGS: ${FLAGS[*]})"
		if check_flags "${FLAGS_ARRAY[*]}" "bns" && [ "${ACTION}" != "bns" ]; then
			log_error "${COLYELLOW}bns${COLRESET} is not a valid flag"
			usage
		fi
		${VERBOSE} && log "Defining FLAGS: ${FLAGS[*]}"
		shift
		;;
	upgrade|pull)
		# Standalone - this can be run without supplying an action arg
		#     If .env image version has been modified, this pulls any image that isn't stored locally
		if [ "${ACTION}" == "status" ]; then
			break
		fi
		${VERBOSE} && log "calling docker_pull function"
		docker_pull
		exit 0
		;;
	log|logs)
		# Standalone - this can be run without supplying an action arg
		#     Tail the logs from the last $LOG_TAIL number of lines
		if [ "${ACTION}" == "status" ]; then
			break
		fi
		# if there is a second arg of 'export', export the logs to text file
		if [ "${2}" == "export" ]; then
			${VERBOSE} && log "calling logs_export function"
			logs_export
		fi
		${VERBOSE} && log "calling docker_logs function"
		docker_logs "${LOG_OPTS}"
		exit 0
		;;
	status)
		# Standalone - this can be run without supplying an action arg
		#     Checks the current service/network status
		if [ "${ACTION}" == "logs" ]; then
			break
		fi
		${VERBOSE} && log "calling status function"
		status
		exit 0
		;;
	bns)
		${VERBOSE} && log "calling download_bns_data function"
		download_bns_data
		;;
	-h|--help)
		${VERBOSE} && log "calling usage function"
		usage
		;;
	(-*)
		# If any unknown args are provided, fail here
		log_error "Unknown arg supplied (${COLYELLOW}${1}${COLRESET})"
		${VERBOSE} && log "calling usage function"
		usage
		;;
	(*)
		# Catchall error
		log_error "Malformed arguments"
		${VERBOSE} && log "calling usage function"
		usage
		;;
	esac
	shift
done

# If ACTION is not set, exit
if [ ! "${ACTION}" ]; then
	log_error "Missing ${COLYELLOW}-a|--action${COLRESET} arg";
	${VERBOSE} && log "calling usage function"
	usage
fi

# Explicitly export these vars since we use them in compose files
# If NETWORK is not set (either cmd line or default of mainnet), exit
if [ ! "${NETWORK}" ]; then
	log_error "Missing ${COLYELLOW}-n|--network${COLRESET} arg"
	${VERBOSE} && log "calling usage function"
	usage
fi
# STACKS_CHAIN_ID reaches the API from conf/<network>/api.env
${VERBOSE} && log "exporting NETWORK: ${NETWORK}"
export NETWORK=${NETWORK}
export PG_SHMSIZE=${PG_SHMSIZE}

# Call function based on ACTION arg
case ${ACTION} in
	up|start)
		${VERBOSE} && log "calling check_device function"
		check_device
		${VERBOSE} && log "calling docker_up function"
		docker_up
		;;
	down|stop)
		${VERBOSE} && log "calling docker_down function"
		docker_down
		;;
	restart)
		${VERBOSE} && log "calling docker_down function"
		docker_down
		${VERBOSE} && log "calling docker_up function"
		docker_up
		;;
	log|logs)
		${VERBOSE} && log "calling docker_logs function"
		docker_logs "${LOG_OPTS}"
		;;
	import|export)
		${VERBOSE} && log "calling event_replay function"
		event_replay "${ACTION}"
		;;
	upgrade|pull)
		${VERBOSE} && log "calling docker_pull function"
		docker_pull
		;;
	status)
		${VERBOSE} && log "calling status function"
		status
		;;
	reset)
		${VERBOSE} && log "calling reset_data function"
		reset_data
		;;
    bns)
		${VERBOSE} && log "calling download_bns_data function"
		download_bns_data
		;;
	*)
		${VERBOSE} && log "calling usage function"
		usage
		;;
esac
${VERBOSE} && log "End of script: exiting"
exit 0
