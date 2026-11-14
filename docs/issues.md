# Workarounds to potential issues

`sbd doctor` checks the most common causes up front: the Compose version, the Docker daemon, `.env`,
disk space at the data location, memory, and services left running outside `COMPOSE_PROFILES`.

## Postgres error: No Space left on device

tl;dr - This doesn't mean what the error message suggests it means. \
Basically, it needs more memory than is provided by default in Docker - but it can be addressed by modifying the `./env` setting `PG_SHMSIZE` to match the memory resources on your system. \
Currently, the default is set to `256MB` and this has shown to be sufficient to bypass this error, but more is obviously better if you can afford it. \
<https://stackoverflow.com/questions/56751565/pq-could-not-resize-shared-memory-segment-no-space-left-on-device>

Sample error from postgres/API logs:

```
pq: could not resize shared memory segment "/PostgreSQL.2058389254" to 12615680 bytes: No space left on device
```

## BNS v1 data

The API (9.x) imports the legacy BNS v1 data by itself during a sync **from genesis**, when
`BNS_IMPORT_DIR` is uncommented in `.env` and the files are present: download them first with
`docker compose run --rm bns` (or `sbd bns`). A node seeded from a Hiro archive snapshot already
contains the BNS data, so this only matters for genesis syncs.

## Port(s) already in use

If you have a port conflict, typically this means you already have a process using that same port. \
To resolve, find the port you have in use (i.e. `3999`) and edit your `.env` file to use a different port.

```bash
$ netstat -anl | grep 3999
tcp46      0      0  *.3999                 *.*                    LISTEN
```

## HTTPS works from outside but not from your own network

With `PROXY_DOMAIN`, the name points at your public IP. Many routers don't loop traffic from the LAN back
to their own public address (NAT loopback, or "hairpin"), at least not for every port, so the name times
out from inside while the internet reaches it fine. Test from outside (a phone off Wi-Fi). For LAN access,
resolve the name to the host's LAN address in your local DNS, or enable NAT loopback on the router.

## Containers not starting (hanging on start)

Occasionally, docker can get **stuck** and not allow new containers to start. If this happens, simply restart your docker daemon and try again.

```bash
$ sudo systemctl restart docker
```

## API Missing Parent Block Error

If the Stacks blockchain is no longer syncing blocks, and the API reports an error similar to this:\
`Error processing core node block message DB does not contain a parent block at height 1970 with index_hash 0x3367f1abe0ee35b10e77fbcaa00d3ca452355478068a0662ec492bb30ee0f13e"`,\
The API (and by extension the DB) is out of sync with the blockchain. \
To recover, re-seed both the chainstate and the API database from the same Hiro archive snapshot
(or resync from genesis):

```bash
docker compose --profile '*' down
docker compose run --rm seed
docker compose up -d
```

## Missing bit_xor

If the `stacks-blockchain-api` logs `function bit_xor(integer) does not exist`, its database is
PostgreSQL 13, which the API no longer supports. The `pg-upgrade` job upgrades from 16 and later only,
so re-seed the database (and the chainstate) from a Hiro archive snapshot instead:

```bash
docker compose --profile '*' down
docker compose run --rm seed
docker compose up -d
```

## Database issues

For the various postgres/sync issues, it may be easier to delete this network's data and resync, from
genesis or from a Hiro archive snapshot. The most common one looks like
`Error: DB does not contain a parent block at height xxxxx`.

```bash
docker compose --profile '*' down
docker compose run --rm reset             # then a genesis sync on the next start, or:
docker compose run --rm seed --dry-run    # the snapshot pair (chainstate + API dump of one date) it would use
docker compose run --rm seed              # asks before replacing data; --yes (or CONFIRM=yes) to skip
docker compose up -d
# sbd: sbd stop && sbd reset (or sbd seed) && sbd start
```

The seed needs room for the downloaded archives (about 360 GB for mainnet; set `SEED_DOWNLOAD_DIR` to
use another volume) plus the restored data; see [Requirements](./requirements.md). Nothing is replaced
until both files are downloaded, verified and restored.
