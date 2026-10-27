# 06 — Configuration Reference

## `.env` lifecycle

| Event | Behavior |
|---|---|
| First run of `manage.sh`, `seed-chainstate.sh` or `postgres-upgrade.sh` | `cp -a sample.env .env` if `.env` is missing (the helper scripts use `$PWD/.env`, see [07](./07-auxiliary-scripts.md)) |
| Every `manage.sh` run | `source .env` (bash). `--env-file .env` is passed to compose for interpolation, and `env_file: .env` passes every key into the containers |
| Every `docker_up` | `cp -a .env .env.save` (backup, gitignored via `*.save`) |
| mocknet `up` | `.env` is edited in place (`sed -i.tmp`) to comment and then uncomment `STACKS_EXPORT_EVENTS_FILE` and `BNS_IMPORT_DIR`. The uncomment step is skipped when `.env` `NETWORK` ≠ `mocknet` **[V]** |
| `sample.env` changes upstream | **Not propagated** to an existing `.env` |

## Consumer legend

- **M**: `manage.sh` shell logic
- **C**: compose interpolation (`${VAR}` in YAML)
- **E**: container environment (every container except nginx, via `env_file`)
- **T**: written into `Config.toml` / `Signer.toml` by `update_configs`
- **S**: `seed-chainstate.sh`
- **U**: `postgres-upgrade.sh`
- **I**: `postgres-initdb.sh` (inside the postgres container)
- **B**: `setup-bns.sh` (inside the bns container)

## Variable catalog (`sample.env`, in file order)

| Variable | Default | Consumers | Meaning and notes |
|---|---|---|---|
| `VERBOSE` | `false` | M, S, E | Debug logging. Evaluated as a *command* (`${VERBOSE} && …`), so it must be exactly `true` or `false` |
| `DOCKER_NETWORK` | `stacks` | M (export), C | Docker network name |
| `EXPOSE_POSTGRES` | `false` | M | `true` adds `extra-services/postgres.yaml` to every compose call. Executed as a command |
| `NETWORK` | `mainnet` | M, S, C (export), E | Default network when `-n` is omitted; **the only network input for `seed-chainstate.sh`**; re-sourcing resets it (D03) |
| `AUTH_TOKEN` | `1234` | M→T, E | Node `[connection_options] auth_token` and signer `auth_password` (must match) |
| `SIGNER_PRIVATE_KEY` | empty | M→T, E | Signer `stacks_private_key` for mainnet **and mocknet**. Required with `-f signer` |
| `TESTNET_SIGNER_PRIVATE_KEY` | empty | M→T, E | Same, for testnet |
| `STACKS_SIGNER_PORT` | `30000` | M→T, E | Signer `endpoint = "0.0.0.0:<port>"`; node observer `endpoint = "stacks-signer:<port>"` |
| `SIGNER_METRICS_PORT` | `9154` | M→T, C, E | Signer `metrics_endpoint`; host port mapping |
| `NODE_ENV` | `production` | E | API runtime env |
| `GIT_TAG` | `master` | E | **Unused**: not referenced in API 9.0.1 **[S]** ([15](./15-verification-log.md) #11c); remove |
| `PG_HOST` | `postgres` | E | API DB host |
| `PG_PORT` | `5432` | E, C | API DB port; host publish port in `postgres.yaml` |
| `PG_USER` | `postgres` | E, I, S | API DB user; initdb psql user; `pg_restore --username` in seed |
| `PG_PASSWORD` | `postgres` | E, C, S, U | API DB password; `POSTGRES_PASSWORD` for the image; `ALTER USER` in seed/upgrade |
| `PG_DATABASE` | `postgres` | E, C, I, S | API DB name; `POSTGRES_DB`; initdb create; seed rename target |
| `PG_SCHEMA` | `public` | E, I, S | API schema; initdb `CREATE SCHEMA`; seed rename target |
| `PG_SHMSIZE` | `256MB` | M (export), C, S | `shm_size` (fix for "No space left on device") |
| `STACKS_CHAIN_ID_DEFAULT` | `2147483648` | E | Passed through to containers only; **manage.sh does not use it** |
| `V2_POX_MIN_AMOUNT_USTX` | `90000000260` | E | **Unused**: not referenced in API 9.0.1 **[S]** ([15](./15-verification-log.md) #11c); remove |
| `STACKS_CORE_EVENT_PORT` | `3700` | E | API event-listener port. **Coupling:** the node TOML hard-codes `stacks-blockchain-api:3700` |
| `STACKS_CORE_EVENT_HOST` | `0.0.0.0` | E | API event-listener bind |
| `STACKS_BLOCKCHAIN_API_PORT` | `3999` | E, C | API HTTP port and host mapping. **Coupling:** nginx hard-codes `:3999` |
| `STACKS_BLOCKCHAIN_API_HOST` | `0.0.0.0` | E | API bind |
| `STACKS_BLOCKCHAIN_API_DB` | `pg` | E | API datastore type |
| `STACKS_CORE_RPC_HOST` | `stacks-blockchain` | E, M→T | API→node RPC host; signer `node_host` |
| `STACKS_CORE_RPC_PORT` | `20443` | E, C, M→T | API→node RPC port; host mapping; signer `node_host`. **Coupling:** node `rpc_bind` hard-codes 20443 |
| `STACKS_CORE_P2P_PORT` | `20444` | E, C | Host mapping. **Coupling:** node `p2p_bind` hard-codes 20444 |
| `STACKS_EXPORT_EVENTS_FILE` | `/tmp/event-replay/stacks-node-events.tsv` | E, M | In-container replay TSV path. M requires it for import/export and checks `persistent-data/mainnet/event-replay/<basename>` |
| `# STACKS_API_ENABLE_FT_METADATA` | (commented) `1` | E | API token metadata |
| `# STACKS_API_ENABLE_NFT_METADATA` | (commented) `1` | E | 〃 |
| `# STACKS_API_TOKEN_METADATA_ERROR_MODE` | (commented) `warning` | E | 〃 |
| `# STACKS_ADDRESS_CACHE_SIZE` | (commented) `10000` | E | API cache |
| `# BNS_IMPORT_DIR` | (commented) `/bns-data` | M, E, B | Enables `bns`; API BNS import dir; container target dir. Must be `/bns-data` to line up with the mounts (M checks `persistent-data${BNS_IMPORT_DIR}`) |
| `RUST_BACKTRACE` | `full` | E | Node backtraces |
| `STACKS_LOG_DEBUG` | `0` | E | Node debug logs |
| `STACKS_LOG_JSON` | `0` | E | Node JSON logs |
| `NODE_METRICS_PORT` | `9153` | M→T, C, E | Node `prometheus_bind`; host mapping |
| `# STACKS_EVENT_OBSERVER` | (commented) | E | Node env alternative to the TOML observer **[S]**: adds one `*` observer with a fixed 1 s timeout, **in addition to** TOML observers. Uncommenting it alongside the TOML API observer makes the node send every event twice (D54, [15](./15-verification-log.md) #10) |
| `STACKS_SHUTDOWN_TIMEOUT` | `1200` | M | `docker compose stop -t` for the node in the ordered stop |
| `NGINX_PROXY_PORT` | `80` | C | nginx host port |
| `STACKS_BLOCKCHAIN_VERSION` | `4.0.1` | C, S | Node image tag; seed archive URL |
| `STACKS_SIGNER_VERSION` | `4.0.1` | C | Signer image tag |
| `STACKS_BLOCKCHAIN_API_VERSION` | `9.0.1` | C, S, M | API image tag; seed archive URL; M refuses replay on `5.0.1` |
| `POSTGRES_VERSION` | `17` | C, S, U | Postgres image tag; seed archive URL; upgrade target |
| `BTC_HOST` | `bitcoin.mainnet.stacks.org` | M→T | `peer_host` (mainnet) |
| `BTC_RPC_USER` | `stacks` | M→T | `username` (mainnet) |
| `BTC_RPC_PASS` | `foundation` | M→T | `password` (mainnet) |
| `BTC_RPC_PORT` | `8332` | M→T | `rpc_port` (mainnet) |
| `BTC_P2P_PORT` | `8333` | M→T | `peer_port` (mainnet) |
| `TBTC_HOST` | `bitcoin.regtest.hiro.so` | M→T | `peer_host` (testnet) |
| `TBTC_RPC_PORT` | `18443` | M→T | `rpc_port` (testnet). **No effect**: the testnet sample has no `rpc_port` line |
| `TBTC_P2P_PORT` | `18444` | M→T | `peer_port` (testnet) |

### Variables referenced but not defined in `sample.env`

| Variable | Where | Effect |
|---|---|---|
| `TBTC_RPC_USER`, `TBTC_RPC_PASS` | `update_configs` testnet aliasing | Empty. No effect, because the testnet sample has no `username`/`password` lines |
| `LOG_TAIL` | `manage.sh` | Script-internal; a `.env` value is ignored |
| `SCRIPTPATH`, `USER_ID`, `STACKS_CHAIN_ID` | exported by `manage.sh` | Compose interpolation |
| `BNS_FILES` | `manage.sh:341` | Undefined (defined only in `setup-bns.sh`) |
| `DEBUG` | `seed-chainstate.sh` verbose logger | Undefined |

## Chain IDs

| Network | `STACKS_CHAIN_ID` exported | Decimal | Delivered to |
|---|---|---|---|
| mainnet | `0x00000001` | 1 | API container only (`networks/*.yaml` `environment`) |
| testnet | `0x80000000` | 2147483648 | API |
| mocknet / fallback | `2147483648` | 2147483648 (= testnet) | API |
| standalone verbs | `2147483648` regardless of network | | |

## `Config.toml` (stacks-node)

Created once from `Config.toml.sample`, then edited in place on **every** `docker_up`, including `bns` and `event-replay` runs **[V]**. Later changes to the samples are never merged into an existing `Config.toml`.

### Keys rewritten by `update_configs` (only if the line already exists)

| Key (regex anchor) | Section | Value | mainnet sample | testnet sample | mocknet sample |
|---|---|---|:-:|:-:|:-:|
| `^peer_host` | burnchain | `"$BTC_HOST"` / `"$TBTC_HOST"` | ✔ | ✔ | ✗ |
| `^username` | burnchain | `"$BTC_RPC_USER"` | ✔ | ✗ | ✗ |
| `^password` | burnchain | `"$BTC_RPC_PASS"` | ✔ | ✗ | ✗ |
| `^rpc_port` | burnchain | `$BTC_RPC_PORT` | ✔ | ✗ | ✗ |
| `^peer_port` | burnchain | `$BTC_P2P_PORT` / `$TBTC_P2P_PORT` | ✔ | ✔ | ✗ |
| `^auth_token` | connection_options | `"$AUTH_TOKEN"` | ✔ | ✔ | ✔ |
| `^endpoint = "stacks-signer` | events_observer (signer) | `"stacks-signer:$STACKS_SIGNER_PORT"` | ✔ | ✔ | ✔ |
| `^prometheus_bind` | node | `"0.0.0.0:$NODE_METRICS_PORT"` | ✔ | ✔ | ✔ |
| signer observer block + `stacker = true` | | commented (no signer) / uncommented (signer) | ✔ | ✔ | ✔ |

Values are inserted into a `sed s///` replacement without escaping, so values containing `/`, `&`, `\` or `"` corrupt the file or break the sed (D35).

### Static per-network content (preserve on refactor)

| Setting | mainnet | testnet | mocknet |
|---|---|---|---|
| `working_dir` | `/root/stacks-blockchain/data` | same | *(unset; ephemeral)* |
| `rpc_bind` / `p2p_bind` | `0.0.0.0:20443` / `0.0.0.0:20444` | same | same |
| `bootstrap_node` | `02196f…3893@seed.mainnet.hiro.so:20444` | `029266…2ddc@seed.testnet.hiro.so:30444` | — |
| node extras | — | `always_use_affirmation_maps`, `require_affirmed_anchor_blocks`, `pox_sync_sample_secs = 30` | `wait_time_for_microblocks = 10000`, `use_test_genesis_chainstate = true` |
| burnchain `mode` | `mainnet` (chain `bitcoin`) | `krypton` | `mocknet` (`commit_anchor_block_within = 5000`) |
| burnchain extras | — | `pox_prepare_length = 100`, `pox_reward_length = 900`, epochs 1.0@0 … 2.5@6, 3.0@1900, 3.1@2000, 3.2@71525, 3.3@109280, 3.4@159350 | — |
| `[[ustx_balance]]` | — | 4 × 10^16 µSTX | 4 × 10^14 µSTX (comments include the test mnemonics/private keys) |
| `connection_options` | `private_neighbors = false` | same | same + `read_only_call_limit_*` |
| API observer | `stacks-blockchain-api:3700`, `["*"]`, `timeout_ms = 300_000` | same, `60_000` | same, `60_000` |
| signer observer | `stacks-signer:30000`, `["stackerdb","block_proposal","burn_blocks"]` | same | same |

The API observer's `timeout_ms` also sets the dispatcher's maximum retry delay (3 ×), so it is 900 s on mainnet today (D55). The refactor renders `60_000` on every network, overridable via `API_OBSERVER_TIMEOUT_MS` (Q26 in [11](./11-refactor-guidance.md)).

## `Signer.toml` (stacks-signer)

Created from the sample on the first signer start, then rewritten on every signer start.

| Key | Source | mainnet | testnet | mocknet |
|---|---|---|---|---|
| `node_host` | `"$STACKS_CORE_RPC_HOST:$STACKS_CORE_RPC_PORT"` | rewritten | rewritten | rewritten |
| `endpoint` | `"0.0.0.0:$STACKS_SIGNER_PORT"` | rewritten | rewritten | rewritten |
| `metrics_endpoint` | `"0.0.0.0:$SIGNER_METRICS_PORT"` | rewritten | rewritten | rewritten |
| `auth_password` | `"$AUTH_TOKEN"` | rewritten | rewritten | rewritten |
| `stacks_private_key` | `SIGNER_PRIVATE_KEY` (testnet: `TESTNET_SIGNER_PRIVATE_KEY`) | rewritten | rewritten | rewritten (mainnet var) |
| `network` | static | `mainnet` | `testnet` | `testnet` |
| `db_path` | static | `/root/stacks-signer/data/signer.sqlite` | same | same |
| `block_proposal_timeout_ms` | static | `180000` | same | same |

## Hard-coded couplings (values that must agree, but only some are templated)

| Value | Defined in | Must match | Templated? |
|---|---|---|---|
| Node RPC 20443 | TOML `rpc_bind` | `STACKS_CORE_RPC_PORT` (compose mapping, API, signer) | **No** (TOML static) |
| Node P2P 20444 | TOML `p2p_bind` | `STACKS_CORE_P2P_PORT` | **No** |
| API events 3700 | TOML API observer endpoint | `STACKS_CORE_EVENT_PORT` | **No** |
| API HTTP 3999 | nginx upstream | `STACKS_BLOCKCHAIN_API_PORT` | **No** |
| Signer events 30000 | TOML signer observer / Signer.toml endpoint | `STACKS_SIGNER_PORT` | Yes |
| Metrics 9153 / 9154 | TOML / Signer.toml | `NODE_METRICS_PORT` / `SIGNER_METRICS_PORT` | Yes |
| Auth token | TOML `auth_token` / Signer.toml `auth_password` | `AUTH_TOKEN` | Yes |
| Container DNS names | TOML, Signer.toml, nginx, `.env` (`PG_HOST`, `STACKS_CORE_RPC_HOST`) | compose service names | partly |
| `/bns-data` | `BNS_IMPORT_DIR` | API and bns container mount targets | No |
| `/tmp/event-replay` | `STACKS_EXPORT_EVENTS_FILE` dir | API mount target | No |

## Default credentials shipped in the repo

| Credential | Default | Exposure |
|---|---|---|
| Node RPC auth / signer password | `1234` | Node RPC 20443 is published on all interfaces |
| Postgres superuser | `postgres` / `postgres` | Published on 0.0.0.0:5432 when exposed |
| Bitcoin RPC | `stacks` / `foundation` | Public foundation node credentials |
| mocknet test keys | private keys in `conf/mocknet/Config.toml.sample` comments | Test-only accounts |
