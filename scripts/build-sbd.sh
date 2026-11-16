#!/usr/bin/env bash
#
# Build sbd from this checkout into ./sbd (the repo root): a static binary, owned by you, with the
# version taken from git. Uses Go if it's installed and new enough, else Docker (golang image).
#
# Usage: ./scripts/build-sbd.sh [output path]
#
# The same flags as the release build (.github/workflows/release.yaml, tools/sbd/Dockerfile).
set -euo pipefail

REPO="$(cd -- "$(dirname "${0}")/.." >/dev/null 2>&1 ; pwd -P)"
OUT="${1:-${REPO}/sbd}"
case "${OUT}" in /*) ;; *) OUT="${PWD}/${OUT}" ;; esac

# The Go version tools/sbd/go.mod asks for (e.g. 1.27)
GO_WANT="$(sed -n 's/^go \([0-9]*\.[0-9]*\).*/\1/p' "${REPO}/tools/sbd/go.mod")"
VERSION="$(git -C "${REPO}" describe --tags --always --dirty 2>/dev/null || echo dev)"
LDFLAGS="-s -w -X main.version=${VERSION}"

# go_ok: an installed Go at least GO_WANT (older ones would try to download a newer toolchain)
go_ok() {
	command -v go >/dev/null 2>&1 || return 1
	local have
	have="$(go env GOVERSION 2>/dev/null | sed -n 's/^go\([0-9]*\.[0-9]*\).*/\1/p')"
	[ -n "${have}" ] || return 1
	[ "$(printf '%s\n%s\n' "${GO_WANT}" "${have}" | sort -V | head -1)" = "${GO_WANT}" ]
}

if go_ok; then
	echo "build-sbd: building ${VERSION} with $(go env GOVERSION)"
	(cd "${REPO}/tools/sbd" && CGO_ENABLED=0 go build -trimpath -ldflags="${LDFLAGS}" -o "${OUT}" .)
elif command -v docker >/dev/null 2>&1; then
	echo "build-sbd: building ${VERSION} in the golang:${GO_WANT} image (Go ${GO_WANT}+ isn't installed)"
	tmp="$(mktemp -d)"
	trap 'rm -rf "${tmp}"' EXIT
	docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -e CGO_ENABLED=0 \
		-v "${REPO}/tools/sbd":/src:ro -v "${tmp}":/out -w /src \
		"golang:${GO_WANT}" go build -trimpath -ldflags="${LDFLAGS}" -o /out/sbd .
	mv "${tmp}/sbd" "${OUT}"
else
	echo "build-sbd: needs Go ${GO_WANT} or newer (https://go.dev/dl/), or Docker" >&2
	exit 1
fi

echo "build-sbd: wrote ${OUT}"
"${OUT}" version
run="${OUT}"
case "${OUT}" in "${PWD}"/*) run="./${OUT#"${PWD}"/}" ;; esac
cat <<EOF
Run it as ${run} from here, or put it on your PATH:
  sudo install -m 0755 ${OUT} /usr/local/bin/sbd
EOF
