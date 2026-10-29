# Usage

The stack is the root [`compose.yaml`](../compose.yaml), and plain `docker compose` (run from the repo
root) is a complete way to use it: `.env` holds every setting, with `NETWORK` and `COMPOSE_PROFILES`
choosing the network and the services. Two optional helpers sit on top and run the same commands:

- **`sbd`**, a single binary (Linux, macOS, Windows) with preflight checks (`sbd doctor`), friendly
  validation, `--json` output and `sbd plan <command>`, which prints the exact `.env` edits and
  `docker compose` commands instead of running them.
- **`./manage.sh`**, the original script (bash; see "Script options" below).

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
| Change a setting | edit `.env` | `sbd env set KEY=VALUE` (keeps a copy in `.env.save`) |
| Preflight checks | — | `sbd doctor` |

`sbd` finds the checkout from the current directory (or `-C <dir>`), creates `.env` from `sample.env`
when there is none, and refuses `seed`, `reset` and `bns` while the stack runs; the jobs refuse on their
own too, so plain Compose is just as safe. Build it with Go 1.25 (`cd tools/sbd && go build -o sbd .`),
or without Go: `docker run --rm -v "$PWD":/src -w /src/tools/sbd golang:1.25 go build -o sbd .`.

# Script options

`./manage.sh` wraps the same [`compose.yaml`](../compose.yaml). Each section below also shows the
plain `docker compose` equivalent.

## Usage

```
Usage:
    ./manage.sh -n <network> -a <action> <optional args>
        -n|--network: [ mainnet | testnet ]
        -a|--action: [ start | stop | logs | reset | upgrade | import | export | bns ]
    optional args:
        -f|--flags: [ signer,proxy,postgres,monitoring ]
        export: combined with 'logs' action, exports logs to a text file
    ex: ./manage.sh -n mainnet -a start -f proxy
	ex: ./manage.sh -n mainnet -a start -f signer,proxy
    ex: ./manage.sh --network mainnet --action start --flags proxy
    ex: ./manage.sh -n mainnet -a logs export
```

### Starting Services

```bash
./manage.sh -n <network> -a start
```

```bash
./manage.sh -n <network> -a restart
```

#### With optional signer

```bash
./manage.sh -n <network> -a start -f signer
```

```bash
./manage.sh -n <network> -a restart -f signer
```

#### With optional proxy

```bash
./manage.sh -n <network> -a start -f proxy
```

```bash
./manage.sh -n <network> -a restart -f proxy
```

#### With monitoring

Prometheus and Grafana on 127.0.0.1; set `GRAFANA_ADMIN_PASSWORD` in `.env` first. See [Monitoring](./monitoring.md).

```bash
./manage.sh -n <network> -a start -f monitoring
```

---

### Stopping Services

```bash
./manage.sh -n <network> -a stop
# or: docker compose --profile '*' down
```

The node is stopped first and gets up to `STACKS_SHUTDOWN_TIMEOUT` seconds to finish its run loop;
don't pass `-t` to `down`, which would override that. A stack started with an older version of this
repo (the `compose-files/` layout) is stopped the same way by `./manage.sh -a stop`.

---

### Event Replay (removed)

API event replay (`-a export` / `-a import`) was removed: resync from a Hiro archive snapshot instead
(see "Seed from the Hiro archive" below).

---

### Logging

#### Stream logs to terminal

```bash
./manage.sh -n <network> -a logs
```

#### Export docker log files

This will create a log file in `./exported-logs` for every running service. \
_Note that each time you run this command the log files will be overwritten._

```bash
./manage.sh -n <network> -a logs export
```

---

### Ensure all images are up to date

```bash
./manage.sh -n <network> -a pull
```

### Delete all chainstate data

Empties this network's data dirs (chainstate, API database, signer) after listing them and asking for
confirmation. It runs in a container, so no sudo is needed, and the dirs themselves are kept, so
dedicated mounts keep working.

```bash
./manage.sh -n <network> -a reset
# or, with the stack stopped: docker compose run --rm reset   (--yes to skip the prompt)
```

### Seed from the Hiro archive

Restores the chainstate and the API database from a snapshot instead of syncing from genesis. See the
README for details; the stack must be stopped. The archive node's own databases (`peer.sqlite`, its
peer list, and `event_observers.sqlite`, its pending event deliveries) are left out: the node creates
new ones and finds its peers from the bootstrap node.

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

### Download BNS data

Store legacy BNS data to `./persistent-data/bns-data` \
_This step is required if the env var `BNS_IMPORT_DIR` is uncommented_

```bash
./manage.sh bns
```
