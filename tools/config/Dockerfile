# The `config` job image: renders conf/<network>/Config.toml and Signer.toml (see main.go).
# Built locally by `docker compose` (compose.yaml `build:`); the build also runs the unit tests,
# which need the repo's conf/ templates, so the build context is the repo root.
FROM golang:1.25 AS build
WORKDIR /src/tools/config
COPY tools/config/go.mod tools/config/go.sum ./
RUN go mod download
COPY tools/config/ ./
COPY conf/ /src/conf/
RUN go vet ./... && go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/config .

FROM scratch
COPY --from=build /out/config /config
ENTRYPOINT ["/config"]
