# sbd in a container, for hosts without the binary: the official Docker CLI image (with the Compose
# plugin) plus sbd. Run it from the checkout with the Docker socket and the checkout at the same path,
# so the bind mounts compose creates resolve on the host:
#   docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock -v "$PWD":"$PWD" -w "$PWD" \
#     ghcr.io/<owner>/sbd status
FROM golang:1.25 AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN go vet ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/sbd .

FROM docker:28.5.2-cli
COPY --from=build /out/sbd /usr/local/bin/sbd
ENTRYPOINT ["/usr/local/bin/sbd"]
