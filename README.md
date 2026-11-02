# Stacks Blockchain with Docker

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Pull Requests Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg?style=flat)](http://makeapullrequest.com)

Run a Stacks node, and optionally the Stacks Blockchain API, a signer, a proxy and monitoring, with
Docker Compose.

⚠️ **[Docker Compose `2.24.0` or newer is required](./docs/docker.md)** (`2.27.2` or later recommended).
Mainnet needs about **1.15 TB** of disk for the node and the API ([requirements](./docs/requirements.md)).

There are two equal ways to run it, and you can switch between them at any time:

- **Plain Docker Compose**, from the repo root. Everything works this way; nothing else is needed.
- **`sbd`**, an optional helper that runs the same compose commands and adds preflight checks
  (`sbd doctor`), validation and `sbd plan <command>`, which prints exactly what a command would do.

All settings live in `.env`, which both read. The [usage guide](./docs/usage.md) has every task side
by side.

## Quickstart

```bash
git clone https://github.com/stx-labs/stacks-blockchain-docker && cd stacks-blockchain-docker
cp sample.env .env      # NETWORK=mainnet and COMPOSE_PROFILES=node,api by default
```

| | Plain Docker Compose | `sbd` |
|---|---|---|
| Check the host | — | `sbd doctor` |
| Start | `docker compose up -d` | `sbd start` (or `sbd start --network testnet --with signer`) |
| Status | `docker compose ps` | `sbd status` |
| Logs | `docker compose logs -f` | `sbd logs` |
| Stop (graceful, node first) | `docker compose --profile '*' down` | `sbd stop` |

Services are chosen with `COMPOSE_PROFILES` in `.env`: `node`, `api` (the API and postgres), `signer`,
`proxy` (nginx) and `monitoring` (Prometheus and Grafana). A signer also needs its private key in
`.env` (`SIGNER_PRIVATE_KEY`, or `TESTNET_SIGNER_PRIVATE_KEY` on testnet).

Get `sbd` from the repo's [GitHub Releases](https://github.com/stx-labs/stacks-blockchain-docker/releases)
(Linux x86-64 for now), run it as an image, or build it from source; see
[Getting `sbd`](./docs/usage.md#plain-docker-compose-and-sbd).

## Seed from the Hiro archive instead of syncing from genesis

A sync from genesis takes days. The `seed` job restores the chainstate and the API database from a
[Hiro Archive](https://docs.hiro.so/hiro-archive) snapshot instead. It runs in a container, so the host
needs nothing but Docker.

```bash
docker compose --profile '*' down          # the seed refuses to run while the stack is up
docker compose run --rm seed --dry-run     # the snapshot it would use, sizes, free space
docker compose run --rm seed               # asks before replacing data; --yes to skip
docker compose up -d
# with sbd: sbd stop && sbd seed --dry-run && sbd seed && sbd start
```

- Downloads the chainstate and the API dump of the same date in parallel, with several ranged
  connections per file (`SEED_CONNECTIONS`, default 4). It verifies them while downloading, and resumes
  an interrupted download.
- Replaces the existing data only once both are restored; a failure leaves the old data as it was.
- Archives you already have go in `SEED_DOWNLOAD_DIR` (default `./persistent-data/seed-downloads`):
  `docker compose run --rm seed --chainstate <file> --pgdump <file>`, with the chainstate as `.tar.zst`,
  `.tar.gz`, `.tar.xz` or `.tar`.
- **Mainnet:** one API index (`nft_events_asset_identifier_index`) takes about 19 hours to rebuild.
  Set `SEED_DEFER_INDEXES=nft_events_asset_identifier_index` in `.env` (or pass `--defer-index`): the
  stack then starts as soon as everything else is restored, about 2 hours, and the `build-indexes` job
  builds that index in the background while the API runs. Until it finishes, NFT-mint queries by asset
  identifier are slow.

## Accessing the services

The node first downloads the Bitcoin headers, which takes several minutes; until then `/v2/info`
doesn't answer. Follow the progress with `docker compose logs -f stacks-blockchain` (or `sbd logs
stacks-blockchain`).

```bash
curl -sL localhost:20443/v2/info | jq     # stacks-blockchain
curl -sL localhost:3999/extended | jq     # stacks-blockchain-api
curl -sL localhost/v2/info | jq           # through the proxy (proxy profile)
```

## Documentation

- [Requirements](./docs/requirements.md): host, disk, Docker Desktop advice
- [Docker setup](./docs/docker.md)
- [Configuration](./docs/config.md): `.env` and the node/signer config overrides
- [Usage](./docs/usage.md): every task with plain Compose and with `sbd`
- [Upgrading](./docs/upgrade.md): new image versions, PostgreSQL majors, older installs
- [Monitoring](./docs/monitoring.md)
- [Common issues](./docs/issues.md)

`./manage.sh`, the original script, still works but is deprecated and will be removed in a later
release.
