# 16 — Functional tests before the next PR (owner, 2026-11-16)

Two hands-on passes, as real users would meet the changes. Use only what the README and `docs/` say:
any step that needs knowledge from outside them is a finding. Note what surprises you, what's unclear,
and how long things take.

Each mode runs twice, on **testnet** and on **mainnet** (owner), on its own throwaway VM (e.g. Debian or
Ubuntu, 4 CPUs; 8 GB RAM for testnet, 16 GB for mainnet) with its own volume for the data:

| Run | Volume | Why |
|---|---|---|
| Mode 1, testnet | **150 GB** | The seed peaks near 60 GB: chainstate and API database about 25 GB each, plus 10.5 GiB of archive downloads (deleted after); room to grow |
| Mode 1, mainnet | **2 TB** | The seed peaks near 1.6 TB: chainstate about 720 GB, API database about 520 GB, plus about 360 GB of downloads (deleted after) |
| Mode 2, testnet | **150 GB** | The old stack syncs from genesis; the full testnet chain is about 50 GB |
| Mode 2, mainnet | **500 GB** | The old stack syncs from genesis for a few hours only: the upgrade test needs a running old install, not a synced one. A full mainnet genesis sync takes days, and the old seed script fails on Hiro's current dumps (`pg_restore --create`), so don't use it. Add space if you let it run on toward the tip |

Docker's images add 4–5 GB if Docker keeps them on the volume. The old layout always writes to
`./persistent-data`, so for mode 2 clone the repo onto the volume itself; for mode 1, clone it there or set
`DATA_ROOT` to the volume.

Until the PR merges, the new layout comes from the fork's
`chore/refactor` branch, and `sbd` is built from source (`docs/usage.md`, "Getting sbd"), since the last
release predates these changes.

## Mode 1: a new user on a new VM

| # | Do | Expect |
|---|---|---|
| 1 | Install Docker following `docs/docker.md` (or Docker's own guide) | `docker compose version` is 2.24.0 or newer |
| 2 | `git clone -b chore/refactor https://github.com/wileyj/stacks-blockchain-docker && cd stacks-blockchain-docker` | |
| 3 | Build `sbd` as `docs/usage.md` says, then `sbd init`: testnet or mainnet, no signer, proxy yes (HTTPS no), monitoring yes, Bitcoin peer default, P2P reachable no, data in the default place | Every question is clear; `.env` is written; secrets show as `<hidden>` and the end lists how to read them (`sbd env get`) |
| 4 | Bootstrap: let `sbd init` seed from the Hiro archive (its own yes), or `sbd seed` afterwards | It shows the snapshot, sizes and free space first; downloads, verifies, restores (testnet: minutes; mainnet: a few hours, with the slow NFT index built afterwards by `build-indexes`) |
| 5 | `sbd doctor`, `sbd start` | Doctor has no failures; the node and API come up; `sbd status` shows them healthy |
| 6 | `sbd ui` | The node catches up to the tip; CHAIN turns "synced ✔"; PROBLEMS empties as it does; `q` leaves the stack running |
| 7 | `curl localhost:3999/extended`, `curl localhost/extended` (proxy), Grafana at `localhost:3000` | The API directly and through the proxy; Grafana dashboards with data |
| 8 | `sbd update api` | Only the API is recreated; the node keeps running and the API is back level with it |
| 9 | `sbd stop`, then `docker compose up -d` and `docker compose --profile '*' down` | Plain Compose runs the same stack without `sbd` |

## Mode 2: an existing user upgrading from `manage.sh`

| # | Do | Expect |
|---|---|---|
| 1 | On its own VM, the old layout as users have it today: `git clone https://github.com/stx-labs/stacks-blockchain-docker && cd stacks-blockchain-docker` (upstream `master`, `d2e558c`), `cp sample.env .env`, set `NETWORK` (testnet or mainnet), and optionally hand-edit `conf/testnet/Config.toml` (e.g. a setting of your own) | |
| 2 | `./manage.sh -n <network> -a start` and let it sync a few hours (don't use its seed script) | The old stack runs (containers in the `compose-files` project) |
| 3 | Upgrade the checkout: `git remote add fork https://github.com/wileyj/stacks-blockchain-docker && git fetch fork && git checkout -b upgrade fork/chore/refactor` | `manage.sh` is gone; the README points to `sbd` and plain Compose |
| 4 | Follow `docs/upgrade.md` "An install from before the compose layout": stop the old stack (`docker stop -t 1200 stacks-blockchain`, `docker compose -p compose-files down`) | Nothing from the old project is left running |
| 5 | Build `sbd`, then `sbd plan migrate` and `sbd migrate` | It adds `COMPOSE_PROFILES`, lists obsolete settings, and moves a hand-edited `Config.toml` aside with your settings in `Config.overrides.toml.suggested`; no data moves |
| 6 | Review and rename the suggestion to `Config.overrides.toml`; `sbd doctor`; `sbd start` | The node resumes from its existing chainstate (no resync) and keeps your own setting in the rendered `Config.toml` |
| 7 | `sbd ui`, then `sbd stop` | As in mode 1 |

**Afterwards:** note the results in [15](./15-verification-log.md), fix what they turn up, then the PR and
the release ([HANDOFF](./HANDOFF.md) "Next"). The VMs can go.
