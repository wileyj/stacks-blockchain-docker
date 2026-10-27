# 05 — Compose Topology

Resolved models (`docker compose config`, sample defaults) for each combination are in [`reference/compose-resolved/`](./reference/compose-resolved/) **[V]**.

## Project-level facts

| Fact | Value | Evidence |
|---|---|---|
| Project name | `compose-files` (basename of the first `-f` file's directory; never overridden) | **[V]** `name: compose-files` |
| Network | `default`, named `${DOCKER_NETWORK}` (sample: `stacks`), default IPAM driver; declared in `common.yaml` and again in `signer.yaml` | **[V]** |
| Bind-mount behavior | `create_host_path: true`, so Docker creates missing host dirs **as root** | **[V]** |
| Healthchecks | none | **[S]** |
| Named volumes | none (bind mounts only) | **[S]** |
| Env injection | every service except nginx has `env_file: ${SCRIPTPATH}/.env`, so **all `.env` keys reach the container environment** (including `SIGNER_PRIVATE_KEY`, `TESTNET_SIGNER_PRIVATE_KEY`, `BTC_RPC_PASS`, `PG_PASSWORD`, `AUTH_TOKEN`) | **[V]** |

## File layering

```
common.yaml                       (always)
└─ networks/<NETWORK>.yaml        (always; volumes + API STACKS_CHAIN_ID)
   ├─ extra-services/postgres.yaml (if EXPOSE_POSTGRES=true or -f postgres; all for down/logs/pull)
   ├─ extra-services/<flag>.yaml   (per -f flag on up; ALL of them for down/logs/pull)
   └─ event-replay/api-<action>-events.yaml (import/export only, profile event-replay)
```

## Services

### `postgres` (`common.yaml`)

| Key | Value |
|---|---|
| image | `postgres:${POSTGRES_VERSION:-15}-alpine` (sample sets 17) |
| shm_size | `${PG_SHMSIZE:-256MB}` |
| env | `env_file .env` + `POSTGRES_PASSWORD=${PG_PASSWORD:-postgres}`, `POSTGRES_DB=${PG_DATABASE:-postgres}` |
| volumes | `scripts/postgres-initdb.sh` → `/docker-entrypoint-initdb.d/postgres-initdb.sh`; mainnet/testnet add `persistent-data/<net>/postgres` → `/var/lib/postgresql/data`. **mocknet: no data volume** (ephemeral) |
| ports | none; `extra-services/postgres.yaml` adds `${PG_PORT:-5432}:5432` (all interfaces) |
| profiles | `event-replay`, `stacks-blockchain` |
| restart | on-failure |

### `stacks-blockchain` (`common.yaml`)

| Key | Value |
|---|---|
| image | `ghcr.io/stacks-network/stacks-core:${STACKS_BLOCKCHAIN_VERSION:-latest}` |
| command | `/bin/stacks-node start --config /src/stacks-blockchain/Config.toml` |
| ports | `${STACKS_CORE_RPC_PORT:-20443}`, `${STACKS_CORE_P2P_PORT:-20444}`, `${NODE_METRICS_PORT:-9153}` mapped to the same container port, all interfaces |
| volumes | `conf/<net>` → `/src/stacks-blockchain` (read-write; includes `Signer.toml` and the samples); mainnet/testnet: `persistent-data/<net>/stacks-blockchain` → `/root/stacks-blockchain/data`. **mocknet: no data volume** |
| depends_on | `stacks-blockchain-api` (service_started) |
| profiles | `stacks-blockchain` only (not started during event-replay) |
| restart | on-failure |

### `stacks-blockchain-api` (`common.yaml`)

| Key | Value |
|---|---|
| image | `hirosystems/stacks-blockchain-api:${STACKS_BLOCKCHAIN_API_VERSION:-latest}` |
| command | `npm run start` (overridden in event-replay) |
| ports | `${STACKS_BLOCKCHAIN_API_PORT:-3999}` (all interfaces). Event port 3700 is **not** published (internal only) |
| environment | network overlay adds `STACKS_CHAIN_ID=${STACKS_CHAIN_ID}` (from manage.sh export) |
| volumes | `persistent-data/bns-data` → `/bns-data`; event-replay dir → `/tmp/event-replay` (mainnet → `persistent-data/mainnet/event-replay`, testnet → `persistent-data/testnet/event-replay`, **mocknet → `persistent-data/mainnet/event-replay`**) |
| depends_on | none (`postgres` commented out) |
| profiles | `event-replay`, `stacks-blockchain` |
| restart | always; `"no"` in event-replay overlays |

### `stacks-signer` (`extra-services/signer.yaml`)

| Key | Value |
|---|---|
| image | `ghcr.io/stacks-network/stacks-signer:${STACKS_SIGNER_VERSION:-latest}` |
| command | `/bin/stacks-signer run --config /src/stacks-signer/Signer.toml` |
| volumes | `conf/<net>` → `/src/stacks-signer` (the same dir the node mounts); `persistent-data/<net>/stacks-signer` → `/root/stacks-signer/data` (mocknet too, so mocknet signer state persists) |
| ports | `${SIGNER_METRICS_PORT:-9154}` (all interfaces). The signer event endpoint 30000 is internal only |
| profiles | `stacks-blockchain` |
| restart | on-failure |

### `nginx` (`extra-services/proxy.yaml`)

| Key | Value |
|---|---|
| image | `nginx:alpine` (unpinned) |
| ports | `${NGINX_PROXY_PORT:-80}:80` |
| volumes | `conf/nginx/default.conf` → `/etc/nginx/conf.d/default.conf` |
| profiles | `stacks-blockchain` |
| behavior | `GET /status` → `200 ok` (text/plain). `/` → `proxy_pass http://stacks-blockchain-api:3999/` (hard-coded port), HTTP/1.1, WebSocket upgrade headers, `Host` preserved |

### `bns` (`extra-services/bns.yaml`)

| Key | Value |
|---|---|
| image | `bash` (Docker Hub official, unpinned) |
| container_name | `bns_data` |
| working_dir | `/srv` |
| volumes | `persistent-data/bns-data` → `/bns-data`; `scripts/` → `/scripts` |
| env | `env_file .env` + `USER_ID=${USER_ID}` |
| command | `sh -c "/scripts/setup-bns.sh"` (the script's own shebang selects bash; the file is mode 755 in git) |
| profiles | `bns` |
| restart | none |

## Profile membership

| Service | stacks-blockchain | event-replay | bns |
|---|:-:|:-:|:-:|
| postgres | ✔ | ✔ | |
| stacks-blockchain | ✔ | | |
| stacks-blockchain-api | ✔ | ✔ | |
| stacks-signer | ✔ (if file included) | | |
| nginx | ✔ (if file included) | | |
| bns | | | ✔ (if file included) |

## Per-network volume matrix

| Host path | mainnet | testnet | mocknet |
|---|:-:|:-:|:-:|
| `conf/<net>` → node `/src/stacks-blockchain` | ✔ | ✔ | ✔ |
| `persistent-data/<net>/stacks-blockchain` → node data | ✔ | ✔ | ✗ (ephemeral) |
| `persistent-data/<net>/postgres` → pg data | ✔ | ✔ | ✗ (ephemeral) |
| `persistent-data/bns-data` → API `/bns-data` | ✔ | ✔ | ✔ |
| `persistent-data/<X>/event-replay` → API `/tmp/event-replay` | X=mainnet | X=testnet | X=**mainnet** |
| `persistent-data/<net>/stacks-signer` (signer flag) | ✔ | ✔ | ✔ |

## Host port summary (defaults)

| Port | Service | Condition | Bind |
|---|---|---|---|
| 20443 | node RPC | always | 0.0.0.0 |
| 20444 | node P2P | always | 0.0.0.0 |
| 9153 | node Prometheus | always | 0.0.0.0 |
| 3999 | API HTTP | always | 0.0.0.0 |
| 9154 | signer metrics | `-f signer` | 0.0.0.0 |
| 80 | nginx | `-f proxy` | 0.0.0.0 |
| 5432 | postgres | `-f postgres` / `EXPOSE_POSTGRES=true` | 0.0.0.0 |

## Image version resolution

| Image | Compose fallback | `sample.env` | `docs/config.md` claims |
|---|---|---|---|
| postgres | 15 | 17 | 14 |
| stacks-core | latest | 4.0.1 | "latest released" |
| stacks-signer | latest | 4.0.1 | "latest released" |
| stacks-blockchain-api | latest | 9.0.1 | "latest released" |
| nginx | — | `alpine` (unpinned) | — |
| bash | — | implicit `latest` | — |
