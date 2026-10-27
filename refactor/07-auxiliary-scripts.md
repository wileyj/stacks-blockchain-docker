# 07 — Auxiliary Scripts

## `scripts/seed-chainstate.sh`

> **2026-10-19 [V]:** with the default pins (API 9.0.1, core 4.0.1) the archive URLs below **do not exist** (`NoSuchKey`). The archive now publishes `stacks-blockchain-api-pg-17-latest.dump` and `<net>-stacks-blockchain-latest.tar.zst`, and the real versions are inside the `.sha256` files. HEAD requests return 403. See D52, D53 and [15](./15-verification-log.md) #8b.

**Purpose:** seed a node and API from the Hiro Archive instead of syncing from genesis. It downloads the stacks-node chainstate tarball and the API Postgres dump, verifies both, extracts the chainstate, restores the dump into `persistent-data/$NETWORK/postgres`, and renames the DB/schema to match `.env`.

**Invocation:** `sudo ./scripts/seed-chainstate.sh`, **from the repo root**. It takes no arguments; everything comes from `.env`.

### Inputs

| Input | Source | Use |
|---|---|---|
| `NETWORK` | `.env` | archive path segment and target dir |
| `POSTGRES_VERSION`, `STACKS_BLOCKCHAIN_API_VERSION`, `STACKS_BLOCKCHAIN_VERSION` | `.env` | archive filenames |
| `DUMP_VERSION` | hard-coded `latest` | archive filenames |
| `PG_USER`, `PG_PASSWORD`, `PG_DATABASE`, `PG_SCHEMA`, `PG_SHMSIZE` | `.env` | restore and rename |
| `VERBOSE` | `.env` | logging |

### Archive URLs

```
https://archive.hiro.so/${NETWORK}/stacks-blockchain-api-pg/stacks-blockchain-api-pg-${POSTGRES_VERSION}-${STACKS_BLOCKCHAIN_API_VERSION}-latest.dump   (+ .sha256)
https://archive.hiro.so/${NETWORK}/stacks-blockchain/${NETWORK}-stacks-blockchain-${STACKS_BLOCKCHAIN_VERSION}-latest.tar.zst                        (+ .sha256)
```

Downloads go to `$SCRIPTPATH`, i.e. the current directory:

| File | Name |
|---|---|
| API dump | `stacks-blockchain-api-pg-<pg>-<api>-latest.dump` |
| API dump checksum | `….dump.sha256` |
| Chainstate | `<net>-blockchain-<core>-latest.tar.zst` |
| Chainstate checksum | `<net>-blockchain-<core>-latest.sha256` |

### Steps

| # | Lines | Step |
|---|---|---|
| 1 | 7-15 | `SCRIPTPATH` = **current working directory** (the path is built from `dirname '${0}'` in single quotes, so it resolves to `.`) **[V]**. Create `.env` from `sample.env` if missing; source it |
| 2 | 17-22 | `CURRENT_USER` from `who am i` (fallback `whoami`), used to chown back to the invoking user. `export $CURRENT_USER` is a typo (it exports a variable named after the user) |
| 3 | 70-72 | Require `EUID == 0` |
| 4 | 176-181 | **`rm -rf persistent-data/$NETWORK` unconditionally, with no prompt**, then `mkdir -p …/stacks-blockchain …/postgres` |
| 5 | 207-210 | `check_network`: if services are running → message + exit. **This runs after step 4** (D01) |
| 6 | 212-216 | `download_file` and `verify_checksum` for the dump, then the chainstate |
| 7 | 221 | `tar -xvf <chainstate>.tar.zst -C persistent-data/$NETWORK/stacks-blockchain/` (needs a tar with zstd support) |
| 8 | 225 | `chown -R $CURRENT_USER persistent-data/$NETWORK` |
| 9 | 231-233 | `docker run -d --rm --name postgres_import --shm-size … -e POSTGRES_PASSWORD -v <dump>:/tmp/stacks_node_postgres.dump -v persistent-data/$NETWORK/postgres:/var/lib/postgresql/data postgres:${POSTGRES_VERSION}-alpine`; `sleep 15` |
| 10 | 237-238 | `pg_restore --username $PG_USER --verbose --create --dbname postgres /tmp/stacks_node_postgres.dump`. The dump creates DB `stacks_blockchain_api` with schema `stacks_blockchain_api` |
| 11 | 239-241 | `ALTER USER $PG_USER PASSWORD '$PG_PASSWORD'` (the command, password included, is `echo`ed to stdout first) |
| 12 | 243-251 | If `PG_DATABASE` ≠ `stacks_blockchain_api` **and** `PG_SCHEMA` ≠ `stacks_blockchain_api`: `DROP SCHEMA IF EXISTS public` in `stacks_blockchain_api`, then `ALTER SCHEMA stacks_blockchain_api RENAME TO ${PG_SCHEMA:-public}` |
| 13 | 252-256 | If `PG_DATABASE` == `postgres`: `DROP DATABASE postgres` (from `template1`) |
| 14 | 257-261 | If `PG_DATABASE` ≠ `stacks_blockchain_api`: `ALTER DATABASE stacks_blockchain_api RENAME TO $PG_DATABASE` |
| 15 | 263 | `docker stop postgres_import` (auto-removed via `--rm`) |
| 16 | 265-277 | Delete the four downloaded files |

**Result with defaults:** the API's data lives in database `postgres`, schema `public`. This matches `PG_DATABASE=postgres` and `PG_SCHEMA=public`.

### Open upstream issues for this script

- [#152](https://github.com/stx-labs/stacks-blockchain-docker/issues/152): step 4's `rm -rf` fails with `Device or resource busy` when `postgres/` or `stacks-blockchain/` are dedicated mounts. The contents are already deleted by then (D48).
- [#132](https://github.com/stx-labs/stacks-blockchain-docker/issues/132): `sha256sum` verification is slow. The maintainer's comment calls the script a POC to be rewritten (D49).
- The rewrite spec is in [14 §9](./14-platform-and-architecture.md).

### `download_file(url, dest)`

- Skips the download if `dest` already exists. A partial file therefore counts as complete, and the checksum then fails.
- A HEAD request logs the status code but does not abort on non-200, because archive.hiro.so redirects to pre-signed R2 URLs.
- The size comes from `Content-Length` via `awk -v IGNORECASE=1` (gawk-only) and is formatted with `numfmt` (GNU coreutils). A missing size defaults to 0.
- Uses `aria2c -x10 -s10 --max-tries=10 --retry-wait=5` if available, otherwise `curl -L -# -C - --retry 10 --retry-delay 5`.

### `verify_checksum(file, url, checksum_file)`

Downloads the `.sha256` if it is missing. Compares the first field against `sha256sum <file>` and exits on mismatch. Needs `sha256sum`, which macOS lacks by default.

### `exit_error`

Prints the message, then `docker stop postgres_import` (best effort), then `exit 1`.

---

## `scripts/postgres-upgrade.sh` (marked WIP in the file)

**Purpose:** move existing **mainnet** Postgres data from major version 13 to `POSTGRES_VERSION` with a `pg_dumpall` → wipe → restore cycle.

| # | Step |
|---|---|
| 1 | `SCRIPTPATH` = cwd (same single-quote issue); create and source `.env` |
| 2 | Prints a start timestamp. **Bug:** the escaped quotes in `$(date \"+%D…\")` are passed to `date` literally, so the command errors |
| 3 | `confirm "Update postgres data to $POSTGRES_VERSION ?"`; if `pg_dump/dump.sql` exists, confirm overwrite and delete it |
| 4 | `sudo docker run -d --rm --name postgres_dump -e POSTGRES_PASSWORD -v ./pg_dump:/tmp -v persistent-data/mainnet/postgres:/var/lib/postgresql/data postgres:13-alpine`; `sleep 15` |
| 5 | `pg_dumpall -U postgres > /tmp/dump.sql`; stop the container |
| 6 | **`sudo rm -rf persistent-data/mainnet/postgres`** and recreate it |
| 7 | Start `postgres:${POSTGRES_VERSION}-alpine` on the empty dir; `sleep 15`; `psql -U postgres -d template1 < /tmp/dump.sql` |
| 8 | `docker exec -it … ALTER USER postgres PASSWORD '$PG_PASSWORD'` (`-it` requires a TTY) |
| 9 | Stop the container; `rm -rf pg_dump`; exit 0 |

Constraints:
- mainnet only; source version hard-coded to 13
- the user `postgres` is hard-coded
- there is no check that services are stopped
- the old data dir is deleted before the restore has been verified (the dump in `pg_dump/` is the only copy until the end)
- `docs/issues.md` refers to it by the wrong name, `postgres_upgrade.sh`

---

## `scripts/postgres-initdb.sh` (runs inside the postgres container)

Mounted as `/docker-entrypoint-initdb.d/postgres-initdb.sh`. The image entrypoint runs it **only when the data dir is empty** (first initialization; not after a seed restore).

```sql
-- psql -v ON_ERROR_STOP=1 --username "$PG_USER" --dbname template1
SELECT 'CREATE DATABASE $PG_DATABASE' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$PG_DATABASE')\gexec
\c $PG_DATABASE;
CREATE SCHEMA IF NOT EXISTS $PG_SCHEMA;
GRANT ALL PRIVILEGES ON DATABASE $PG_DATABASE TO $PG_USER;
```

The `PG_*` values reach it through `env_file`. With the defaults it is redundant: the image already creates `POSTGRES_DB=postgres`, and `public` exists. Identifiers are interpolated without quoting.

---

## `scripts/setup-bns.sh` (runs inside the `bns_data` container)

**Purpose:** fetch and verify the legacy BNS v1 export used by older API versions' BNS import.

| # | Step |
|---|---|
| 1 | `BNS_FILES=(chainstate.txt name_zonefiles.txt subdomain_zonefiles.txt subdomains.csv)` |
| 2 | `USER_ID` is defaulted from the env, then **unconditionally overwritten** with `id -u $(whoami)` inside the container (root → 0). The final chown goes to root, defeating the `USER_ID` passthrough (D11) |
| 3 | `TARFILE=$BNS_IMPORT_DIR/export-data.tar.gz`. If missing: `wget https://storage.googleapis.com/blockstack-v1-migration-data/export-data.tar.gz` |
| 4 | For each file: `check_sha256`. If `<f>` and `<f>.sha256` both exist, compare the full contents of `.sha256` with `sha256sum <f>`. On a mismatch or a missing file, increment `counter`, `extract_files`, and re-check. After 2 attempts → exit 1 |
| 5 | `extract_files`: `tar -xzf $TARFILE -C $BNS_IMPORT_DIR/ <f>.sha256` and then `<f>` |
| 6 | `chown -R $USER_ID $BNS_IMPORT_DIR`; exit 0 |

Notes:
- The checksums come from the same tarball, so they catch extraction corruption, not a tampered download.
- API 9.0.1 **still consumes `BNS_IMPORT_DIR`** **[S]**: the BNS v1 import runs when block height 1 arrives, so only during a genesis sync ([15](./15-verification-log.md) #11b). After a seed or an event replay it is irrelevant.
