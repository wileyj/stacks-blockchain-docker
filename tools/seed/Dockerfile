# The `seed` job image (see main.go). Built from the (Debian, glibc) postgres image of the stack's
# POSTGRES_VERSION, so pg_restore and the temporary restore server match the database the API will run
# on. Chainstate archives are extracted with GNU tar + zstd/xz/gzip: on glibc as fast as on the host;
# Alpine's musl builds of the same tools were about 40% slower (refactor/15 "Phase 3c").
ARG PG_VERSION=17
FROM golang:1.25 AS build
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends zstd xz-utils >/dev/null && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY tools/seed/go.mod tools/seed/go.sum ./
RUN go mod download
COPY tools/seed/ ./
RUN go vet ./... && go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed .

FROM postgres:${PG_VERSION}
# ca-certificates: the Debian postgres image ships without them (HTTPS to the archive)
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends ca-certificates zstd xz-utils >/dev/null && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/seed /usr/local/bin/seed
ENTRYPOINT ["/usr/local/bin/seed"]
