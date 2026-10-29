# Stacks Blockchain with Docker

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Pull Requests Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg?style=flat)](http://makeapullrequest.com)

Run your own Stacks Blockchain node easily with just few commands.

⚠️ **[docker compose version `2.24.0` or greater is required](./docs/docker.md)** (`2.27.2` or later recommended)
⚠️ **On Ubuntu, you may need to run Docker commands with `sudo` unless your user is in the `docker` group**

---

## Quickstart

```bash
git clone https://github.com/stacks-network/stacks-blockchain-docker && cd stacks-blockchain-docker
cp sample.env .env
```

### Sync from genesis (no signer)

```bash
./manage.sh -n mainnet -a start
```

Or with plain Docker Compose, no script needed: set `NETWORK` and `COMPOSE_PROFILES` in `.env`, then

```bash
docker compose up -d                  # starts the services in COMPOSE_PROFILES (default: node,api)
docker compose ps                     # status and health
docker compose logs -f                # follow the logs
docker compose --profile '*' down     # graceful stop (the node gets up to STACKS_SHUTDOWN_TIMEOUT)
```

### Sync from genesis (with signer)

```bash
./manage.sh -n mainnet -a start -f signer
```

### Seed chainstate from Hiro Archiver

Using data from the [Hiro Archiver](https://docs.hiro.so/hiro-archive) service, this script will download the latest files, extract them and restore the postgres data. \
_**Note**: it can take a long time to process the data, and you'll need at a minimum roughly **1.5TB** of free space to download and process the chainstate archives_\
_**Note**: for faster downloads, install [aria2](https://aria2.github.io/) on your system_

Example aria2c install on a debian/ubuntu based system:
```bash
# 1. Install build dependencies
sudo apt-get update
sudo apt-get install -y build-essential autoconf automake autotools-dev autopoint \
  libtool pkg-config gettext libssl-dev libc-ares-dev libxml2-dev \
  zlib1g-dev libsqlite3-dev libssh2-1-dev

# 2. Clone the source
git clone https://github.com/aria2/aria2
cd aria2

autopoint --force
autoreconf -fi

# 3. Configure without translations (avoids the po/ gettext build entirely)
./configure --disable-nls --with-openssl

# 4. Build and install
make -j$(nproc)
sudo make install

# 5. Verify
aria2c --version
```

```bash
docker compose --profile '*' down                 # the seed refuses to run while the stack is up
docker compose run --rm seed --dry-run            # optional: show the snapshot it will use
docker compose run --rm seed                      # asks before replacing data; --yes to skip
docker compose up -d                              # or: ./manage.sh -n mainnet -a start
```

The seed runs in a container (no sudo, curl, zstd or aria2 needed on the host): it downloads the
chainstate and API dump of the same date in parallel (`SEED_CONNECTIONS` ranged connections per file,
default 4), verifies them while downloading, resumes interrupted downloads, and only replaces the
existing data once both are restored. Archives you already have go in `SEED_DOWNLOAD_DIR` (default
`./persistent-data/seed-downloads`): `docker compose run --rm seed --chainstate <file> --pgdump <file>`
(chainstate as `.tar.zst`, `.tar.gz`, `.tar.xz` or `.tar`, keeping the archive's file name). The older
`sudo ./scripts/seed-chainstate.sh` still works.

**Mainnet:** one API index (`nft_events_asset_identifier_index`, a hash index over very skewed data)
takes 18 hours or more to rebuild, and the node stays down until the restore finishes. Defer it with
`SEED_DEFER_INDEXES=nft_events_asset_identifier_index` in `.env` (or `--defer-index <name>`): the
stack starts as soon as everything else is restored, and the `build-indexes` job builds the index in
the background (`CREATE INDEX CONCURRENTLY`) while the API runs. Until it finishes, API queries that
filter NFT events by asset identifier are slow.

- [Requirements](./docs/requirements.md)
- [Docker Setup](./docs/docker.md)
- [Configuration](./docs/config.md)
- [Upgrading](./docs/upgrade.md)
- [Usage](./docs/usage.md)
- [Monitoring](./docs/monitoring.md)
- [Common Issues](./docs/issues.md)

---

## Accessing the services

_Downloading the initial Bitcoin headers can take several minutes. \
Until the headers are downloaded, the `/v2/info` endpoints won't return any data (the API starts serving once the node does). \
Use the command `./manage.sh -n <network> -a logs` to check the sync progress._

**stacks-blockchain**:

```bash
curl -sL localhost:20443/v2/info | jq
```

**stacks-blockchain-api**:

```bash
curl -sL localhost:3999/v2/info | jq
```

**proxy** _(optional argument)_:

```bash
curl -sL localhost/v2/info | jq
curl -sL localhost/ | jq
```
