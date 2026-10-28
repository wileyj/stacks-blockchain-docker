# The `seed` job image (see main.go). Built from the postgres image of the stack's POSTGRES_VERSION,
# so pg_restore and the temporary restore server match the database the API will run on.
ARG PG_VERSION=17
FROM golang:1.25 AS build
WORKDIR /src
COPY tools/seed/go.mod tools/seed/go.sum ./
RUN go mod download
COPY tools/seed/ ./
RUN go vet ./... && go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed .

FROM postgres:${PG_VERSION}-alpine
COPY --from=build /out/seed /usr/local/bin/seed
ENTRYPOINT ["/usr/local/bin/seed"]
