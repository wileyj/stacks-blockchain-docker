# The `seed` job image (see main.go). Built FROM THE SAME postgres image as the stack's postgres service
# (postgres:<POSTGRES_VERSION>-alpine): the restore server builds every index, and text indexes are
# ordered by the C library's collation. Indexes built on glibc (Debian) and then served by musl (Alpine)
# are misordered: amcheck failures and a unique index that broke API ingestion (refactor/15 "Phase 3c").
# Chainstate archives are extracted with GNU tar + zstd/xz/gzip. musl's tools are about 40% slower than
# glibc's, which costs nothing overall: extraction runs in parallel with the much longer restore.
ARG PG_VERSION=17
FROM golang:1.25 AS build
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends zstd xz-utils >/dev/null && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY tools/seed/go.mod tools/seed/go.sum ./
RUN go mod download
COPY tools/seed/ ./
RUN go vet ./... && go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed .

## The `pg-upgrade` job (pgupgrade.go): this image's binaries build the new cluster, and Alpine's
## packages of every older major it ships provide the old server for pg_upgrade (musl on both sides)
FROM postgres:${PG_VERSION}-alpine AS pg-upgrade
ARG PG_VERSION
RUN for v in $(seq 13 $((PG_VERSION - 1))); do apk add --no-cache "postgresql${v}" >/dev/null 2>&1 && echo "old server: PostgreSQL ${v}"; done; true
COPY --from=build /out/seed /usr/local/bin/seed
ENTRYPOINT ["/usr/local/bin/seed", "pg-upgrade"]

## The `seed` job (the default target, last)
FROM postgres:${PG_VERSION}-alpine AS seed
# GNU tar (the image has busybox tar), and the decompressors it runs
RUN apk add --no-cache tar zstd xz
COPY --from=build /out/seed /usr/local/bin/seed
ENTRYPOINT ["/usr/local/bin/seed"]
