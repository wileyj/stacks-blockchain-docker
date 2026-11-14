# Configuration

Settings live in `.env` (created from [`sample.env`](../sample.env) if missing). The node and signer
configs, `./conf/<network>/Config.toml` and `Signer.toml`, are **generated** by the `config` job on every
start from the templates in `./conf/<network>/*.sample` and the `.env` values; don't edit them, the next
start overwrites them. Put your own settings in:

- `./conf/<network>/Config.overrides.toml`: any `stacks-node` setting. Tables are merged into the
  generated file, keys you set win, and `[[events_observer]]` entries are added to the generated ones.
- `./conf/<network>/Signer.overrides.toml`: the same for the signer.

The templates follow upstream stacks-core: `conf/upstream/` holds its sample configs (commit in
`conf/upstream/SOURCE`), `conf/<network>/Config.deployment.toml` this repo's defaults, and maintainers
regenerate the templates with `tools/sync-upstream-conf.sh`.

A hand-edited `Config.toml` from an earlier version is moved to `Config.toml.legacy` the first time the
job runs; copy any custom settings from it into `Config.overrides.toml`, or let
`docker compose run --rm config suggest` (`sbd migrate`) write them to `Config.overrides.toml.suggested`
for review.

The `[[events_observer]]` blocks follow the enabled services: the API observer only with the `api`
profile, the signer observer only with `signer`. Any configured observer is a liveness dependency of the
node, which blocks delivering events while it is unreachable.

## Environment Variables

Most variables in `.env` shouldn't be modified, but there are a few you may wish to change before starting the services.

### Global Settings

| Name                            | Description                                           | Default Value             |
| ------------------------------- | ----------------------------------------------------- | ------------------------- |
| `DOCKER_NETWORK`                | Name of docker network used to launch services        | `stacks`                  |
| `COMPOSE_PROFILES`              | Services `docker compose up -d` starts: always `node` and `api` (API + postgres), plus any of `signer`, `proxy`, `monitoring`. `sbd start --with/--without` edits it | `node,api` |
| `DATA_ROOT`                     | Where the data lives: `<DATA_ROOT>/<network>/{stacks-blockchain,postgres,stacks-signer}` and `<DATA_ROOT>/bns-data` | `./persistent-data` |
| `STACKS_DATA_DIR`, `POSTGRES_DATA_DIR`, `SIGNER_DATA_DIR` | One component's data dir, e.g. on a dedicated disk | under `DATA_ROOT` |
| `API_BIND_ADDR`, `RPC_BIND_ADDR` | Host address the API (3999) and the node RPC (20443) are published on. To serve the API to other machines, prefer the `proxy` profile | `127.0.0.1` |
| `PROXY_BIND_ADDR`, `PROXY_HTTP_PORT`, `PROXY_HTTPS_PORT` | The `proxy` profile (Caddy), the way to serve the API to other machines: the address and host ports it is published on | `0.0.0.0`, `80`, `443` |
| `PROXY_TLS`, `PROXY_DOMAIN`, `PROXY_ACME_EMAIL` | HTTPS for the proxy. `PROXY_TLS=1` serves HTTPS on 443 and redirects HTTP to it: with `PROXY_DOMAIN`, a Let's Encrypt certificate Caddy obtains and renews (the name must point at this host, and 80 and 443 be reachable from the internet); without it, a self-signed one. `PROXY_ACME_EMAIL`: optional contact for expiry warnings | `0`, empty, empty |
| `CADDY_VERSION` | The proxy's Caddy image | `2.11.9-alpine` |
| `STACKS_P2P_ADDRESS` | The address peers reach this node on (`host:port`), advertised to them. Empty, the default, is a node behind NAT that only connects out. Set it when the P2P port is forwarded or public, then rebuild the peer database ([Behind NAT](./requirements.md#behind-nat)) | empty |
| `POSTGRES_LOCAL`, `POSTGRES_LOCAL_PORT` | `1` publishes postgres on `127.0.0.1:POSTGRES_LOCAL_PORT` for local tools. It is never published beyond this host: only the API uses it | `0`, `5432` |
| `SBD_UI_REFERENCE_API`          | `sbd ui` only: a public API (e.g. `https://api.hiro.so`) whose tip the dashboard compares the node with; within 15 blocks counts as synced. Unset, "synced" means the latest block is under 5 minutes old, and the dashboard makes no outbound calls | empty |
| `PROMETHEUS_PORT`, `GRAFANA_PORT`, `GRAFANA_ADMIN_PASSWORD`, `PROMETHEUS_RETENTION`, `PROMETHEUS_APP_SCRAPE_INTERVAL`, `MONITORING_GRAFANA`, `MONITORING_CONTAINER_METRICS` | The `monitoring` profile, see [Monitoring](./monitoring.md). Always on 127.0.0.1 | `9090`, `3000`, empty, `30d`, `15s`, `1`, `1` |
| `STACKS_BLOCKCHAIN_VERSION`     | Stacks Blockchain Docker image version                | `latest released version` |
| `STACKS_SIGNER_VERSION`         | Stacks Signer Docker image version                    | `latest released version` |
| `STACKS_BLOCKCHAIN_API_VERSION` | Stacks Blockchain API Docker image version            | `latest released version` |
| `POSTGRES_VERSION`              | Postgres Docker image version                         | `14`                      |

### Stacks Signer Settings

#### You must set values for the _SIGNER_PRIVATE_KEY_ on the specific network if you're running a signer, otherwise they can be left empty.

| Name                         | Description                                                                                      | Default Value |
| ---------------------------- | ------------------------------------------------------------------------------------------------ | ------------- |
| `AUTH_TOKEN`                 | Authorization token for HTTP requests made from the signer to your Stacks node                   | `1234`        |
| `SIGNER_PRIVATE_KEY`         | The private key of the signer, on mainnet.                                                       |               |
| `TESTNET_SIGNER_PRIVATE_KEY` | The private key of the signer, on testnet.                                                       |               |
| `STACKS_SIGNER_PORT`         | The port where the signer will expose an RPC endpoint for receiving events from your Stacks node | `30000`       |
| `SIGNER_METRICS_PORT`        | The port where the signer will expose an endpoint for reading metrics                            | `9154`        |

### API Settings

#### Recommened to leave these settings _as is_

| Name                         | Description                                                                                                 | Default Value                              |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------- | ------------------------------------------ |
| `NODE_ENV`                   | Application environment the API is running                                                                  | `production`                               |
| `GIT_TAG`                    | Application Git tag                                                                                         | `master`                                   |
| `PG_HOST`                    | Host of the postgres instance for the API's datastore                                                       | `postgres`                                 |
| `PG_PORT`                    | Port of the postgres service                                                                                | `5432`                                     |
| `PG_USER`                    | Postgres username                                                                                           | `postgres`                                 |
| `PG_PASSWORD`                | Postgres password                                                                                           | `postgres`                                 |
| `PG_DATABASE`                | Postgres database name                                                                                      | `postgres`                                 |
| `PG_SHMSIZE`                 | Shared memory size for Postgres                                                                             | `256MB`                                    |
| `STACKS_CORE_EVENT_PORT`     | Port to accept incoming Stacks Blockchain events                                                            | `3700`                                     |
| `STACKS_CORE_EVENT_HOST`     | IPv4 to accept incoming Stacks Blockchain events                                                            | `0.0.0.0`                                  |
| `STACKS_BLOCKCHAIN_API_PORT` | Port to run the HTTP interface                                                                              | `3999`                                     |
| `STACKS_BLOCKCHAIN_API_HOST` | IPv4 to run the HTTP interface                                                                              | `0.0.0.0`                                  |
| `STACKS_BLOCKCHAIN_API_DB`   | Database type to store data                                                                                 | `pg`                                       |
| `STACKS_CORE_RPC_HOST`       | FQDN of the Stacks Blockchain service for RPC proxy requests                                                | `stacks-blockchain`                        |
| `STACKS_CORE_RPC_PORT`       | RPC port of the Stacks Blockchain service                                                                   | `20443`                                    |
| `STACKS_CORE_P2P_PORT`       | P2P port of the Stacks Blockchain service                                                                   | `20444`                                    |
| `API_OBSERVER_TIMEOUT_MS`    | Timeout (ms) for the node's event delivery to the API. The node retries a failed delivery with a backoff of up to 3x this value, so it also bounds how long the node stays blocked after the API comes back. Written to `timeout_ms` of the API `[[events_observer]]` in `Config.toml` on every start. Measured on mainnet (2026-11): the genesis block takes the API about 38 s, every later block at most 12 s, so keep it at 60 s or more | `60000` |

#### Options commented out by default

| Name                                   | Description                                        | Default Value |
| -------------------------------------- | -------------------------------------------------- | ------------- |
| `STACKS_API_ENABLE_FT_METADATA`        | Enables storing metadata for fungible events       | `0`           |
| `STACKS_API_ENABLE_NFT_METADATA`       | Enables storing metadata for non-fungible events   | `0`           |
| `STACKS_API_TOKEN_METADATA_ERROR_MODE` | Token metadata error handling level                | `warning`     |
| `BNS_IMPORT_DIR`                       | Required with a path to import/use legacy BNS data | `/bns-data`   |

### Stacks Blockchain Settings

| Name                      | Description                                                                                            | Value  |
| ------------------------- | ------------------------------------------------------------------------------------------------------ | ------ |
| `RUST_BACKTRACE`          | Display full rust backtrace on unexepcted binary exit                                                  | `full` |
| `STACKS_LOG_DEBUG`        | Verbose output logs                                                                                    | `0`    |
| `STACKS_LOG_JSON`         | Output logs in json format                                                                             | `0`    |
| `STACKS_SHUTDOWN_TIMEOUT` | Time to wait for Stacks Blockchain to shutdown properly.<br>_recommended to leave this at the default_ | `1200` |
| `NODE_METRICS_PORT`       | The port where the node will expose an endpoint for reading metrics                                    | `9153` |

### Burnchain Settings

#### Mainnet Burnchain Defaults

| Name           | Description                           | Default Value                |
| -------------- | ------------------------------------- | ---------------------------- |
| `BTC_HOST`     | Bitcoin node the Stacks node follows over P2P. Use your own if at all possible: the default public peer is shared and often busy | `bitcoin.mainnet.stacks.org` |
| `BTC_RPC_USER` | Optional RPC username for bitcoin mainnet host. A follower doesn't need one | _(unset)_ |
| `BTC_RPC_PASS` | Optional RPC password for bitcoin mainnet host. A follower doesn't need one | _(unset)_ |
| `BTC_RPC_PORT` | RPC port for bitcoin mainnet host     | `8332`                       |
| `BTC_P2P_PORT` | P2P port for bitcoin mainnet host     | `8333`                       |

#### Testnet Burnchain Defaults

| Name            | Description                           | Default Value                |
| --------------- | ------------------------------------- | ---------------------------- |
| `TBTC_HOST`     | FQDN of bitcoin testnet host         | `bitcoin.regtest.hiro.so` |
| `TBTC_RPC_PORT` | RPC port for bitcoin testnet host     | `18332`                      |
| `TBTC_P2P_PORT` | P2P port for bitcoin testnet host     | `18333`                      |
