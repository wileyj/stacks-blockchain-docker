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
job runs; copy any custom settings from it into `Config.overrides.toml`.

The `[[events_observer]]` blocks follow the enabled services: the API observer only with the `api`
profile, the signer observer only with `signer`. Any configured observer is a liveness dependency of the
node, which blocks delivering events while it is unreachable.

## Environment Variables

Most variables in `.env` shouldn't be modified, but there are a few you may wish to change before starting the services.

### Global Settings

| Name                            | Description                                           | Default Value             |
| ------------------------------- | ----------------------------------------------------- | ------------------------- |
| `VERBOSE`                       | Enables verbose logging when `./manage.sh` is invoked | `false`                   |
| `DOCKER_NETWORK`                | Name of docker network used to launch services        | `stacks`                  |
| `COMPOSE_PROFILES`              | Services `docker compose up -d` starts: `node`, `api` (API + postgres), `signer`, `proxy`. `./manage.sh` selects them with `-f` instead | `node,api` |
| `DATA_ROOT`                     | Where the data lives: `<DATA_ROOT>/<network>/{stacks-blockchain,postgres,stacks-signer,event-replay}` and `<DATA_ROOT>/bns-data` | `./persistent-data` |
| `STACKS_DATA_DIR`, `POSTGRES_DATA_DIR`, `SIGNER_DATA_DIR`, `EVENT_REPLAY_DIR` | One component's data dir, e.g. on a dedicated disk | under `DATA_ROOT` |
| `METRICS_BIND_ADDR`             | Host address the node and signer metrics ports are published on | `127.0.0.1` |
| `EXPOSE_POSTGRES`               | Publish postgres on the host (`compose.postgres-port.yaml`; with plain compose add it to `COMPOSE_FILE`) | `false` |
| `POSTGRES_BIND_ADDR`            | Host address for the published postgres port      | `127.0.0.1`               |
| `MONITORING_BIND`, `PROMETHEUS_PORT`, `GRAFANA_PORT`, `GRAFANA_ADMIN_PASSWORD`, `PROMETHEUS_RETENTION`, `MONITORING_GRAFANA`, `MONITORING_CONTAINER_METRICS` | The `monitoring` profile, see [Monitoring](./monitoring.md) | `127.0.0.1`, `9090`, `3000`, empty, `15d`, `1`, `1` |
| `STACKS_BLOCKCHAIN_VERSION`     | Stacks Blockchain Docker image version                | `latest released version` |
| `STACKS_SIGNER_VERSION`         | Stacks Signer Docker image version                    | `latest released version` |
| `STACKS_BLOCKCHAIN_API_VERSION` | Stacks Blockchain API Docker image version            | `latest released version` |
| `POSTGRES_VERSION`              | Postgres Docker image version                         | `14`                      |
| `NGINX_PROXY_PORT`              | HTTP port for the nginx proxy                         | `80`                      |

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
| `API_OBSERVER_TIMEOUT_MS`    | Timeout (ms) for the node's event delivery to the API. The node retries a failed delivery with a backoff of up to 3x this value, so it also bounds how long the node stays blocked after the API comes back. Written to `timeout_ms` of the API `[[events_observer]]` in `Config.toml` on every start | `60000` |
| `STACKS_EXPORT_EVENTS_FILE`  | File to store the API events file.<br>Locally this is stored at `./persistent-data/<network>/event-replay/` | `/tmp/event-replay/stacks-node-events.tsv` |

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
| `BTC_HOST`     | FQDN of bitcoin mainnnet host         | `bitcoin.mainnet.stacks.org` |
| `BTC_RPC_USER` | RPC username for bitcoin mainnet host | `stacks`                     |
| `BTC_RPC_PASS` | RPC password for bitcoin mainnet host | `foundation`                 |
| `BTC_RPC_PORT` | RPC port for bitcoin mainnet host     | `8332`                       |
| `BTC_P2P_PORT` | P2P port for bitcoin mainnet host     | `8333`                       |

#### Testnet Burnchain Defaults

| Name            | Description                           | Default Value                |
| --------------- | ------------------------------------- | ---------------------------- |
| `TBTC_HOST`     | FQDN of bitcoin testnet host         | `bitcoin.regtest.hiro.so` |
| `TBTC_RPC_PORT` | RPC port for bitcoin testnet host     | `18332`                      |
| `TBTC_P2P_PORT` | P2P port for bitcoin testnet host     | `18333`                      |
