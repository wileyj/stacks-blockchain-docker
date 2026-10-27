#!/usr/bin/env bash
# Shared helpers for .github/ci/*.sh. Source it; expects REPO and TMP to be set.

# Pinned value from sample.env (never the user's .env)
pin() { sed -n "s/^${1}=//p" "${REPO}/sample.env" | tail -1; }

# Throwaway signer key, valid secp256k1 scalar; only used to render a checkable Signer.toml
TEST_SIGNER_KEY="0000000000000000000000000000000000000000000000000000000000000001"

# render <workdir> <network> [manage.sh flags...]
#   Copies the tracked files into <workdir>, creates .env from sample.env and runs
#   `./manage.sh -n <network> -a start` with docker stubbed, which renders
#   conf/<network>/Config.toml (and Signer.toml with `-f signer`) exactly as a real start does.
render() {
	local work="${1}" network="${2}"; shift 2
	mkdir -p "${work}" "${TMP}/stub"
	cat > "${TMP}/stub/docker" <<'EOF'
#!/usr/bin/env bash
# manage.sh only needs `compose version` to succeed and `ps -q` to report nothing running
[ "$*" = "compose version" ] && echo "Docker Compose version v2.99.0-stub"
exit 0
EOF
	chmod +x "${TMP}/stub/docker"
	git -C "${REPO}" ls-files -z | (cd "${REPO}" && xargs -0 tar cf -) | tar xf - -C "${work}"
	cp "${work}/sample.env" "${work}/.env"
	sed -i.tmp "s/^SIGNER_PRIVATE_KEY=.*/SIGNER_PRIVATE_KEY=${TEST_SIGNER_KEY}/; s/^TESTNET_SIGNER_PRIVATE_KEY=.*/TESTNET_SIGNER_PRIVATE_KEY=${TEST_SIGNER_KEY}/" "${work}/.env"
	rm -f "${work}/.env.tmp"
	(cd "${work}" && PATH="${TMP}/stub:${PATH}" ./manage.sh -n "${network}" -a start "$@")
}
