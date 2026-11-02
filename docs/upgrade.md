# Upgrades

Image versions are pinned in `.env` (`STACKS_BLOCKCHAIN_VERSION`, `STACKS_SIGNER_VERSION`,
`STACKS_BLOCKCHAIN_API_VERSION`, `POSTGRES_VERSION`). Every command below is shown with plain Docker
Compose and with `sbd`.

## Patch and minor versions

Change the versions in `.env`, then pull and restart. The node and the API migrate their data on start.

```bash
docker compose pull --ignore-buildable
docker compose --profile '*' down
docker compose up -d
# sbd: sbd pull && sbd restart
```

## A new PostgreSQL major

Raise `POSTGRES_VERSION` in `.env`, then upgrade the database before starting the stack (the new
postgres can't open an older major's files):

```bash
docker compose --profile '*' down
docker compose run --rm pg-upgrade --dry-run
docker compose run --rm pg-upgrade
docker compose up -d
# sbd: sbd stop && sbd db upgrade && sbd start
```

See [Usage](./usage.md#upgrade-the-api-database-to-a-new-postgresql-major) for the modes and what is
kept.

## Breaking API schema changes (major API versions)

When a major API version can't migrate the existing database, rebuild the data from a Hiro archive
snapshot made with the new version:

```bash
docker compose --profile '*' down
# set the new versions in .env
docker compose run --rm seed --dry-run    # shows the snapshot that matches the new pins
docker compose run --rm seed
docker compose up -d
# sbd: sbd stop && sbd seed && sbd start
```

If no snapshot matches the new pins yet, the seed says so and names the latest one; wait for a
matching snapshot or sync from genesis.

## An install from before the compose layout

Installs from before `compose.yaml` (the `compose-files/` layout and a hand-edited `Config.toml`) need
two changes, and `sbd migrate` makes them without moving any data (`sbd plan migrate` previews it):

- `.env` needs `COMPOSE_PROFILES` (for example `node,api`); without it, `docker compose up -d` starts
  nothing but the config job.
- A hand-edited `conf/<network>/Config.toml` is regenerated now. `docker compose run --rm config
  suggest` writes your own settings from it to `Config.overrides.toml.suggested`; review it and rename it
  to `Config.overrides.toml`.

Stop a stack started with the old layout with `./manage.sh -n <network> -a stop` before starting the
new one.

_API event replay (`-a export` / `-a import`) was removed: the archive snapshots replace it._
