# 01 — System Overview

## Purpose

The repository wraps Docker Compose so an operator can run a Stacks blockchain follower node with only a few commands, together with:

- the **Hiro Stacks Blockchain API** and the **Postgres** database it uses;
- an optional **Stacks signer**;
- an optional **nginx reverse proxy** in front of the API;
- maintenance helpers: chainstate seeding from the Hiro archive, legacy BNS v1 data download, API event-replay import/export, and a Postgres major-version upgrade.

The main entry point is `./manage.sh`. Everything else is either a compose fragment it assembles, a config template it renders, or a standalone helper script.

## Component map

```mermaid
flowchart LR
  subgraph Host
    M[manage.sh] -->|assembles -f files + --profile| DC[docker compose]
    SEED[scripts/seed-chainstate.sh] -->|docker run postgres_import| D[(docker)]
    PGU[scripts/postgres-upgrade.sh] -->|docker run postgres_dump| D
    ENV[.env] -.sourced.-> M
    ENV -.--env-file / env_file.-> DC
    CFG[conf/NETWORK/Config.toml\nSigner.toml] -.sed-rendered by.- M
  end

  subgraph "Docker network ${DOCKER_NETWORK} (default: stacks)"
    N[stacks-blockchain\nstacks-core node]
    A[stacks-blockchain-api\nHiro API]
    P[(postgres)]
    S[stacks-signer\noptional]
    X[nginx proxy\noptional]
    B[bns_data\nbash, one-shot]
  end

  DC --> N & A & P & S & X & B
  N -- "events :3700 (events_observer *)" --> A
  N -- "events :30000 (stackerdb, block_proposal, burn_blocks)" --> S
  S -- "RPC :20443 (auth_password = AUTH_TOKEN)" --> N
  A -- "SQL :5432" --> P
  A -- "RPC proxy STACKS_CORE_RPC_HOST:20443" --> N
  X -- "HTTP :3999" --> A
  N -- "RPC :8332 / P2P :8333" --> BTC[(Bitcoin node\nBTC_HOST)]
  N -- "P2P :20444" --> PEERS[(Stacks peers / bootstrap_node)]
  B -- "wget" --> GCS[(storage.googleapis.com\nBNS v1 export)]
  SEED -- "curl/aria2c" --> HIRO[(archive.hiro.so)]
```

## Supported dimensions

| Dimension | Values | Source of truth |
|---|---|---|
| Network (`-n`) | `mainnet`, `testnet`, `mocknet` | Discovered at runtime from `compose-files/networks/*.yaml` (`manage.sh:121-127`) |
| Flags (`-f`) | `signer`, `proxy`, `postgres` (accepted but undocumented), `bns` (only internally / with `-a bns`) | Discovered from `compose-files/extra-services/*.yaml` (`manage.sh:111-118`) |
| Compose profiles | `stacks-blockchain` (normal), `event-replay` (API import/export), `bns` (BNS download) | `PROFILE` variable, switched internally |
| Actions (`-a`) | `up start down stop restart log logs import export upgrade pull status reset bns` | Hard-coded list (`manage.sh:131-146`) |

**Single-instance constraint [S]:** every service has a fixed `container_name` (`postgres`, `stacks-blockchain`, `stacks-blockchain-api`, `stacks-signer`, `nginx`, `bns_data`), and the compose project name is always `compose-files` **[V]**. Only one network's stack can run on a host at a time, and every "is it running?" check ignores which network is running.

## File inventory

| Path | Kind | Role |
|---|---|---|
| `manage.sh` | bash, 1150 lines | CLI entry point. Arg parsing, config rendering, compose orchestration ([02](./02-cli-reference.md), [03](./03-manage-sh-internals.md)) |
| `sample.env` | env template | Copied to `.env` on first run. Holds every tunable ([06](./06-configuration-reference.md)) |
| `compose-files/common.yaml` | compose | Base services: `postgres`, `stacks-blockchain`, `stacks-blockchain-api`; the default network |
| `compose-files/networks/{mainnet,testnet,mocknet}.yaml` | compose overlay | Per-network volumes and `STACKS_CHAIN_ID` env for the API |
| `compose-files/extra-services/signer.yaml` | compose overlay | `stacks-signer` service |
| `compose-files/extra-services/proxy.yaml` | compose overlay | `nginx` service |
| `compose-files/extra-services/postgres.yaml` | compose overlay | Publishes the postgres port to the host |
| `compose-files/extra-services/bns.yaml` | compose overlay | One-shot `bns_data` container that runs `scripts/setup-bns.sh` |
| `compose-files/event-replay/api-{import,export}-events.yaml` | compose overlay | Replace the API command with `import-events` / `export-events` |
| `conf/{mainnet,testnet,mocknet}/Config.toml.sample` | TOML template | stacks-node config, copied to `Config.toml` on first start |
| `conf/{mainnet,testnet,mocknet}/Signer.toml.sample` | TOML template | stacks-signer config, copied to `Signer.toml` on first signer start |
| `conf/nginx/default.conf` | nginx | Proxy `/` to the API, `/status` returns `ok` |
| `scripts/seed-chainstate.sh` | bash, run as root | Download and restore chainstate + API Postgres dump from archive.hiro.so ([07](./07-auxiliary-scripts.md)) |
| `scripts/postgres-upgrade.sh` | bash, WIP | Dump/restore Postgres 13 data into `POSTGRES_VERSION` (mainnet only) |
| `scripts/postgres-initdb.sh` | bash, in-container | Postgres first-init hook: create DB and schema |
| `scripts/setup-bns.sh` | bash, in-container | Download and verify the legacy BNS v1 export |
| `docs/*.md` (6) | docs | User docs ([10](./10-documentation-drift.md) lists the drift) |
| `README.md` | docs | Quickstart |
| `packer/README.md` | docs | Points to an external machine-image repo; nothing else |
| `.github/stale.yml` | repo meta | Probot "stale" bot config (issues: stale at 180 days, closed 7 days later). **No CI workflows, tests or linters exist.** |
| `.gitignore` | repo meta | Ignores generated config, `.env`, `persistent-data/`, `exported-logs/`, `*.save`, `*.log`, etc. |
| `LICENSE`, `CODE_OF_CONDUCT.md` | legal | GPL-3.0; contributor covenant |

## Runtime topology (defaults from `sample.env`)

| Service | Image (default tag) | Container | Profiles | Host ports | Restart |
|---|---|---|---|---|---|
| postgres | `postgres:${POSTGRES_VERSION:-15}-alpine` → `17` | `postgres` | stacks-blockchain, event-replay | none (5432 with `-f postgres` or `EXPOSE_POSTGRES=true`) | on-failure |
| stacks-blockchain | `ghcr.io/stacks-network/stacks-core:${STACKS_BLOCKCHAIN_VERSION:-latest}` → `4.0.1` | `stacks-blockchain` | stacks-blockchain | 20443, 20444, 9153 | on-failure |
| stacks-blockchain-api | `hirosystems/stacks-blockchain-api:${STACKS_BLOCKCHAIN_API_VERSION:-latest}` → `9.0.1` | `stacks-blockchain-api` | stacks-blockchain, event-replay | 3999 | always ("no" in event-replay) |
| stacks-signer | `ghcr.io/stacks-network/stacks-signer:${STACKS_SIGNER_VERSION:-latest}` → `4.0.1` | `stacks-signer` | stacks-blockchain | 9154 | on-failure |
| nginx | `nginx:alpine` (unpinned) | `nginx` | stacks-blockchain | `${NGINX_PROXY_PORT:-80}`→80 | on-failure |
| bns | `bash` (unpinned, implicit `latest`) | `bns_data` | bns | none | none |

Full detail: [05 — Compose topology](./05-compose-topology.md).

## Host dependencies

| Tool | Needed by | Checked? |
|---|---|---|
| `bash` (3.2+ OK; uses arrays, `[[ ]]`, `read -n`) | all scripts | no |
| `docker` and the `docker compose` v2 plugin | `manage.sh`, seed, upgrade | **yes**: `manage.sh:918-925` (`docker`, `id`, `docker compose version`) |
| **GNU** `sed` | `manage.sh` `update_configs` (`s///mg`, labels in `{}`) | no. BSD sed fails **[V]** |
| `awk`, `tr`, `grep`, `cp`, `rm`, `mkdir`, `date`, `uname`, `id`, `whoami` | `manage.sh` | only `id` |
| `curl`, optional `aria2c`, `numfmt`, `sha256sum`, gawk (for `IGNORECASE`), `tar` with zstd support plus `zstd` | `seed-chainstate.sh` | only `aria2c` (optional) |
| root / `sudo` | `seed-chainstate.sh` (enforced), `postgres-upgrade.sh` (calls `sudo` itself), `reset` (often, because Docker creates root-owned dirs) | seed only |
| `jq` | README `curl … | jq` examples only | n/a (not used by scripts) |

## External endpoints contacted

| Endpoint | By | Purpose |
|---|---|---|
| `bitcoin.mainnet.stacks.org:8332/8333` (user `stacks` / pass `foundation`) | stacks-node (mainnet) | Burnchain RPC/P2P |
| `bitcoin.regtest.hiro.so:18444` | stacks-node (testnet) | Burnchain P2P |
| `seed.mainnet.hiro.so:20444`, `seed.testnet.hiro.so:30444` | stacks-node | Bootstrap peers |
| `https://archive.hiro.so/<network>/…` | `seed-chainstate.sh` | Chainstate tarball, API pg dump, `.sha256` files |
| `https://storage.googleapis.com/blockstack-v1-migration-data/export-data.tar.gz` | `setup-bns.sh` (in container) | Legacy BNS v1 data |
| `ghcr.io`, Docker Hub | docker pull | Images |
