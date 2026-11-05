# Usage

The stack is the root [`compose.yaml`](../compose.yaml), and plain `docker compose`, run from the repo
root, is a complete way to use it. `.env` holds every setting: `NETWORK` and `COMPOSE_PROFILES` choose
the network and the services. **`sbd`** is an optional helper that runs the same commands and adds
preflight checks (`sbd doctor`), friendly validation, `--json` output, and `sbd plan <command>`, which
prints the exact `.env` edits and `docker compose` commands instead of running them. Use either, or
both: neither keeps state the other doesn't see.

## Plain Docker Compose and `sbd`

| Task | Plain Docker Compose | `sbd` |
|---|---|---|
| Start the services in `COMPOSE_PROFILES` | `docker compose up -d` | `sbd start` |
| Change network or services | edit `NETWORK` / `COMPOSE_PROFILES` in `.env`, then `docker compose up -d` | `sbd start --network testnet --with signer,monitoring` (also `--without`, `--profiles`) |
| Stop (node first, graceful) | `docker compose --profile '*' down` | `sbd stop` |
| Restart | `docker compose --profile '*' down`, then `docker compose up -d` | `sbd restart` |
| Status | `docker compose --profile '*' ps --all` | `sbd status [--json]` (exit 0 = running, 1 = not) |
| Follow the logs | `docker compose logs -f --tail 100` | `sbd logs` |
| One service's logs | `docker compose logs -f --tail 100 <service>` | `sbd logs <service>` |
| Export every container's log | `docker compose --profile '*' logs --no-color --timestamps <service> > exported-logs/<container>.log` | `sbd logs export` |
| Pull the images pinned in `.env` | `docker compose pull --ignore-buildable` | `sbd pull` |
| Render `Config.toml`/`Signer.toml` (`up` does it too) | `docker compose run --rm config` | `sbd config render` |
| Seed from the Hiro archive (stopped stack) | `docker compose run --rm seed` | `sbd seed [seed options]` |
| Delete this network's data (stopped stack) | `docker compose run --rm reset --yes` (without `--yes`, it asks on a terminal) | `sbd reset [--yes]` |
| Download legacy BNS data (stopped stack) | `docker compose run --rm bns` | `sbd bns` |
| Upgrade the API database after raising `POSTGRES_VERSION` (stopped stack) | `docker compose run --rm pg-upgrade` (`--dry-run`, `--mode auto\|clone\|copy\|link`) | `sbd db upgrade` |
| Change a setting | edit `.env` | `sbd env set KEY=VALUE` (keeps a copy in `.env.save`) |
| Preflight checks | — | `sbd doctor` |
| Upgrade an install from the old layout | add `COMPOSE_PROFILES` to `.env`; `docker compose run --rm config suggest` writes your hand-edited `Config.toml` settings to `Config.overrides.toml.suggested` | `sbd migrate` (`sbd plan migrate` to preview) |

`sbd` finds the checkout from the current directory (or `-C <dir>`), creates `.env` from `sample.env`
when there is none, and refuses `seed`, `reset`, `bns` and `db upgrade` while the stack runs. The jobs
refuse on their own too, so plain Compose is just as safe.

Getting `sbd`:
- **Release binary** (Linux x86-64 for now): download `sbd-<version>-linux-amd64` from the repo's
  GitHub Releases, check it, and put it on your `PATH`:
  `sha256sum -c --ignore-missing SHA256SUMS`, and (with the GitHub CLI) its build provenance:
  `gh attestation verify sbd-<version>-linux-amd64 --repo <owner>/stacks-blockchain-docker`, which
  shows it was built by the release workflow from the signed tag. Releases can't be changed once
  published.
- **Container image** (linux/amd64 for now), no install: run it from the checkout with the Docker
  socket and the checkout mounted at the same path, so the paths compose uses resolve on the host:
  `docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock -v "$PWD":"$PWD" -w "$PWD" ghcr.io/<owner>/sbd status`
  (provenance: `gh attestation verify oci://ghcr.io/<owner>/sbd:<version> --repo <owner>/stacks-blockchain-docker`)
- **From source** with Go 1.25, on any OS: `cd tools/sbd && go build -o sbd .`, or without Go:
  `docker run --rm -v "$PWD":/src -w /src/tools/sbd golang:1.25 go build -o sbd .`.

## Services

`COMPOSE_PROFILES` in `.env` lists the services `up` starts:

| Profile | Services | Notes |
|---|---|---|
| `node` | stacks-node | |
| `api` | Stacks Blockchain API and postgres | The node delivers events to the API, so a configured API is a liveness dependency of the node |
| `signer` | stacks-signer | Needs `SIGNER_PRIVATE_KEY` (testnet: `TESTNET_SIGNER_PRIVATE_KEY`) |
| `proxy` | nginx on port 80 | |
| `monitoring` | Prometheus, Grafana, exporters on 127.0.0.1 | Set `GRAFANA_ADMIN_PASSWORD` first; see [Monitoring](./monitoring.md) |

The default is `node,api`; `node` alone runs a follower without the API. After removing a profile, stop
with `docker compose --profile '*' down` (or `sbd stop`): a plain `up` or `down` leaves the removed
profile's containers running.

## Stopping

The node is stopped first and gets up to `STACKS_SHUTDOWN_TIMEOUT` seconds to finish its run loop.
Don't pass `-t` to `down`: it would override that.

## Logs

`docker compose logs -f` (or `sbd logs`) follows every service; name one to follow only it.
`sbd logs export` writes each container's full log to `./exported-logs/<container>.log`, overwriting the
previous export.

## Delete this network's data

Empties this network's data dirs (chainstate, API database, signer) after listing them and asking for
confirmation. It runs in a container, so no sudo is needed, and it keeps the dirs themselves, so
dedicated mounts keep working.

```bash
docker compose --profile '*' down
docker compose run --rm reset      # --yes (or CONFIRM=yes) to skip the prompt
# sbd: sbd stop && sbd reset
```

## Seed from the Hiro archive

Restores the chainstate and the API database from a snapshot instead of syncing from genesis (see the
[README](../README.md#seed-from-the-hiro-archive-instead-of-syncing-from-genesis)); the stack must be
stopped. The archive node's own databases (`peer.sqlite`, its peer list, and `event_observers.sqlite`,
its pending event deliveries) are left out: the node creates new ones and finds its peers from the
bootstrap node.

```bash
docker compose --profile '*' down
docker compose run --rm seed --dry-run
docker compose run --rm seed
# with archives you already have, placed in SEED_DOWNLOAD_DIR:
docker compose run --rm seed --chainstate <file>.tar.zst --pgdump <file>.dump
# mainnet: build the slowest API index after the stack starts (see the README)
docker compose run --rm seed --defer-index nft_events_asset_identifier_index
docker compose logs -f build-indexes   # progress of deferred index builds once the stack runs
```

A deferred index builds with `CREATE INDEX CONCURRENTLY` while the stack runs (about 19 hours for the
mainnet NFT index). Until it's done, don't run `VACUUM` or `ANALYZE` on its table (`nft_events`): the
two deadlock, postgres cancels the build, and `build-indexes` starts it over from the beginning.

With `sbd`, the same options follow `sbd seed`, for example `sbd seed --dry-run`.

## Upgrade the API database to a new PostgreSQL major

After raising `POSTGRES_VERSION` in `.env` (for example from 16 to 17), upgrade the existing database
before starting the stack; the new postgres can't open an older major's files. The job runs
`pg_upgrade` with the stack's own postgres image, builds the new cluster next to the old one in the
data dir, and swaps it in only after the upgrade and the statistics rebuild succeed.

```bash
docker compose --profile '*' down
docker compose run --rm pg-upgrade --dry-run   # versions, size, mode, free space
docker compose run --rm pg-upgrade             # asks first; --yes (or CONFIRM=yes) without a terminal
# sbd: sbd stop && sbd db upgrade --dry-run && sbd db upgrade
```

`--mode`: `auto` (the default) uses `clone` where the filesystem supports copy-on-write files (XFS
with reflink, Btrfs: instant, no extra space), else `copy` if the database fits a second time, else it
stops and suggests `link`: hard links, minutes and no extra space, but once the new cluster has run the
old one can't be used again. Roles, passwords and `pg_hba.conf` are kept; the new release's
`postgresql.conf` defaults are used. The image can upgrade from the older majors its Alpine release
packages (16 for PostgreSQL 17); for anything older, re-seed instead.

## Download BNS data

Only for a sync **from genesis** with `BNS_IMPORT_DIR` uncommented in `.env`: the API imports the
legacy BNS v1 data from `./persistent-data/bns-data` while it syncs. A seeded node already has it.

```bash
docker compose run --rm bns     # sbd: sbd bns
```

## `./manage.sh` (deprecated)

`./manage.sh` still works the way it did, wrapping the same `compose.yaml`, and prints a deprecation
notice on stderr. It will be removed in a later release. Its flags apply to one run only, while
`COMPOSE_PROFILES` and `sbd start --with` persist the choice in `.env`.

| `./manage.sh` | Plain Docker Compose | `sbd` |
|---|---|---|
| `-n <network> -a start [-f signer,proxy,monitoring]` | set `NETWORK` and `COMPOSE_PROFILES`, then `docker compose up -d` | `sbd start --network <network> --profiles node,api,…` |
| `-n <network> -a stop` | `docker compose --profile '*' down` | `sbd stop` |
| `-n <network> -a restart` | `docker compose --profile '*' down`, then `docker compose up -d` | `sbd restart` |
| `-n <network> -a logs` / `logs export` | `docker compose logs -f` | `sbd logs` / `sbd logs export` |
| `-n <network> -a pull` | `docker compose pull --ignore-buildable` | `sbd pull` |
| `-n <network> -a reset` | `docker compose run --rm reset` | `sbd reset` |
| `bns` | `docker compose run --rm bns` | `sbd bns` |
| `status` | `docker compose ps` | `sbd status` |

A stack started from the old `compose-files/` layout is stopped by `./manage.sh -n <network> -a stop`;
see [Upgrading](./upgrade.md#an-install-from-before-the-compose-layout) for moving such an install over.
API event replay (`-a export` / `-a import`) was removed: resync from a Hiro archive snapshot instead.
