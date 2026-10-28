# Upgrades

Image versions are pinned in `.env` (`STACKS_BLOCKCHAIN_VERSION`, `STACKS_SIGNER_VERSION`,
`STACKS_BLOCKCHAIN_API_VERSION`, `POSTGRES_VERSION`).

## Patch and minor versions

Change the versions in `.env`, then pull and restart. The node and the API migrate their data on start.

```bash
docker compose pull
docker compose --profile '*' down
docker compose up -d
# or: ./manage.sh -n <network> -a pull && ./manage.sh -n <network> -a restart
```

## Breaking API schema changes (major API versions)

When a major API version can't migrate the existing database, rebuild the data from a Hiro archive
snapshot made with the new version instead of replaying events:

```bash
docker compose --profile '*' down
# set the new versions in .env
docker compose run --rm seed --dry-run    # shows the snapshot that matches the new pins
docker compose run --rm seed
docker compose up -d
```

If no snapshot matches the new pins yet, the seed says so and names the latest one; wait for a
matching snapshot or sync from genesis.

_API event replay (`-a export` / `-a import`) was removed: the archive snapshots replace it._
