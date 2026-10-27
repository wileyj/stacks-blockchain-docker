#!/usr/bin/env bash
# Shared helpers for .github/ci/*.sh. Source it; expects REPO and TMP to be set.

# Pinned value from sample.env (never the user's .env)
pin() { sed -n "s/^${1}=//p" "${REPO}/sample.env" | tail -1; }

# Throwaway signer key, valid secp256k1 scalar; only used to render a checkable Signer.toml
TEST_SIGNER_KEY="0000000000000000000000000000000000000000000000000000000000000001"

# copy_repo <dest>: tracked plus new (not ignored) files, so uncommitted work is tested as well;
# never .env, conf/*/Config.toml or persistent-data/
copy_repo() {
	mkdir -p "${1}"
	# skip index entries already deleted in the working tree
	(cd "${REPO}" && git ls-files -z --cached --others --exclude-standard \
		| while IFS= read -r -d '' f; do [ -e "${f}" ] && printf '%s\0' "${f}"; done \
		| xargs -0 tar cf -) | tar xf - -C "${1}"
}

# render <workdir> <network> <profiles>
#   Copies the repo into <workdir> (copy_repo), creates .env from sample.env and runs the compose
#   `config` job, which renders conf/<network>/Config.toml (and Signer.toml with the signer profile)
#   exactly as `docker compose up` does.
render() {
	local work="${1}" network="${2}" profiles="${3}"
	copy_repo "${work}"
	{
		cat "${work}/sample.env"
		echo "NETWORK=${network}"
		echo "COMPOSE_PROFILES=${profiles}"
		echo "SIGNER_PRIVATE_KEY=${TEST_SIGNER_KEY}"
		echo "TESTNET_SIGNER_PRIVATE_KEY=${TEST_SIGNER_KEY}"
		echo "DOCKER_NETWORK=sbd-render"
	} > "${work}/.env"
	(cd "${work}" && docker compose -p sbd-render run --rm --quiet-pull config && docker compose -p sbd-render down >/dev/null 2>&1)
}
