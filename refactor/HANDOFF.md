# Handoff — refactor analysis state (2026-10-26)

Use this to pick the work up in a new session or on another machine. The full index is [README.md](./README.md). Live-test recipes are in [live-tests/](./live-tests/README.md).

## Rules for this work
- Edit only files under `refactor/`. Don't modify any existing repo file.
- No git commits or pushes unless the owner asks.
- Run experiments in scratch Compose projects (`sbd-*`) with named volumes, never against the repo's `.env`, `conf/` or `persistent-data/`. Clean up containers and volumes afterwards.
- The working tree has an **uncommitted `sample.env` edit made by the owner** (core/signer 4.0.1 → 4.0.4, API 9.0.1 → 9.7.0; Postgres stays 17). Leave it alone; whether to commit it is the owner's call.

## Where things stand
- **Baseline documented:** docs 01–10 cover current behavior; defects D01–D55 are in [09](./09-defects-and-risks.md).
- **Design:** [14](./14-platform-and-architecture.md), compose-first:
  - plain `docker compose` is a first-class path (§2.1, §11);
  - the Go CLI/TUI `sbd` is optional;
  - heavy jobs run in containers (config render, seed, pg-upgrade, bns).
- **Decisions:** all settled, Q1–Q27, M1–M6 and T1–T8. See the README [Decisions log](./README.md#decisions-log) and [11 §B](./11-refactor-guidance.md). Deferred TODOs are in [11 §F](./11-refactor-guidance.md).
- **Verification:** [15](./15-verification-log.md) records every check.
- **Harness:** removed with `manage.sh` in phase 7 (2026-11-13); it characterised `manage.sh`, and its history is in git (last in `3983250`).

## What was decided or found on 2026-10-26 (macOS session)

| Topic | Result | Where |
|---|---|---|
| **P1:** mainnet follower vs the LAN BTC node | Works **without** BTC RPC credentials (P2P only; the test node's RPC needs auth and returned 401, and the node never called it). RPC/metrics up **2 min 05 s** after `up`. Headers took about 60 s against the LAN peer. 11 metric families at RPC-up, 21 once blocks were processing (families appear lazily). `burn_block_height` stays at 666051 during genesis sync | 15 "P1 results" |
| **P2:** API down | Node stalls indefinitely. Backoff doubles from 100 ms up to **3 × `timeout_ms`** (900 s with the mainnet sample). Recovery lagged the API by 2 min 15 s. **D55**, fixed by **Q26**: render `timeout_ms = 60_000`, overridable via `API_OBSERVER_TIMEOUT_MS`. A node restart was rejected, because an API that is up but has lost its DB triggers no container event | 09 D55, 11 Q26 |
| **D50:** testnet config | Obsolete. The **Hiro testnet was reset** (upstream PR #7490): new bootstrap node on 20444, new epochs including 4.0, `pox_5_*` keys. The repo sample fails `check-config` twice, then panics on the missing Epoch 4.0. **Q27:** network config comes from upstream stacks-core **`main` `sample/conf/`**, vendored with the source SHA, deployment keys and user overrides layered on top; CI runs `check-config` **and** a short boot | 09 D50, 11 Q27, 14 |
| **P6** on macOS | **PASS** with upstream `main` testnet config (`live-tests/p6-testnet-smoke.sh`) | 15 "P6" |
| **P4:** signer metrics | Done from the owner's live signer: **13 families**, with alert-rule candidates in doc 12. The local testnet attempt was blocked: Hiro's regtest BTC peer stalled at 6 000/31 691 headers. `refactor/key.js` has a loop bug (`genKeyPair()` outside the loop) | 15 "P4 results", 12 |
| **Archive (D52) correction** | The archive keeps a **rolling 30-day window of dated, versioned files**, not only `-latest`. Mainnet has no `<ver>-latest` aliases; testnet has them. The testnet archive (4.0.1/9.0.2) **is** from the reset testnet (its headers DB ends at 31 500). Archive files are owned by uid 1001 | 15 "Archive inventory", 09 D52 |
| **Q24 re-settled** | **The snapshot date matters most.** Seed downloads a chainstate + pg-dump **pair with the same date**: the newest date where both exist and the versions are compatible with the pins (same major, ≤ pin; dump pg major ≤ server). It never mixes dates and never downloads `-latest`. Older versions get a warning. It exits 1 on a major change, newer versions or no pair, naming the reason and the latest snapshot. No per-network pins. *"Refuse newer" is an assistant addition the owner may relax* | 11 Q24 |
| **TODO-1:** non-root node | Feasible: uid 1000 with `working_dir = "/data"` works. With `/root/...` it panics. **Owner, 2026-11-13: upstream first** (a stacks-core PR); this repo follows once the images don't need root | 11 §F |
| Corrections | Postgres pin is 17, not 18. The backoff cap is not 60 s or 180 s on mainnet | 15 |

## What was found on 2026-10-26 (Linux x64 session)

Host: i9-13900H (SHA-NI), Docker Engine 29.5.3, Compose v5.1.4, cgroup v2. Scratch data went on the 3 TB `/stacks/temp` LV. The owner's local mainnet pair (`mainnet-stacks-blockchain-4.0.4-20261015.tar.zst` 276 GB, `stacks-blockchain-api-pg-17-9.6.0-20261015.dump` 104 GB) is there too. Untouched.

| Test | Result | Where |
|---|---|---|
| **P1** x64 | **PASS.** No BTC RPC calls/errors without credentials; 0 ERRO/panics/restarts. RPC/metrics up **4 min 16 s** after `up` (macOS 2 min 05 s): header sync 1:45, then a **silent ~1:46 burnchain phase** with no log output, genesis MARF 3 s, then 38 s to the Prometheus bind. Outbound peers 1.5–2 min after RPC-up; about 81 blocks per 30 s. Metric families **identical** to macOS (11 at RPC-up, then 21); API 61 families | 15 "P1 results — Linux x64", 12 |
| **P5** SHA-256 | coreutils/OpenSSL/Go about **1.5 GB/s** (arm64: 2.7–2.9), busybox **184 MB/s**, Alpine coreutils 868 MB/s. NVMe cold read 4.7 GB/s, so on fast disks **the hash, not the re-read, is the bottleneck**. This corrects #9/14 §9.2; hash-while-download still fixes both | 15 #9, "P5 results", 14 |
| **P5** cAdvisor | Native cgroup v2: 67 `container_*` families including every doc-12 name; about 27 MiB idle. The script's `--disable_metrics` lacks `advtcp`, so use the list in 15's reproduction notes | 15 "P5 results", 12 |
| **P6** x64 | **PASS** (180 s, 0 restarts/panics). Upstream sample at `6d794b10f1a3` | 15 "P6 — Linux x64" |
| **P2** x64 (opt) | The stall reproduced: tip frozen while the API was down, backoff to 379 s at attempt 11. **Recovery lagged the API by 5 min 29 s** (macOS 2:15). No events lost. Strengthens D55/Q26 | 15 "P2 results — Linux x64", 09 D55 |
| **P3** x64 (opt) | `STACKS_EVENT_OBSERVER` next to the TOML observer gives **about 2.2× deliveries** to the API, and the **API stores no duplicates** (unique hashes, one canonical block per height). D54 stays Info. The 1 s timeout of the env observer under load is untested | 15 "P3 results", 09 D54 |
| **Q24** restore check | **Dropped.** The owner confirmed that an older archive into newer pins within a major is a known-good path [O] | 15 owner table, 11 Q24 |

**Working-tree note:** the tests ran while this clone was on `chore/fix_chown` (d70fae3). Afterwards, the owner's other changes were stashed and `out.txt`/`patch` went to `stash@{0}`. Branch **`chore/refactor` was created at `d2e558c`** (= `origin/master`) with only the owner's `sample.env` pin bump (core/signer 4.0.1 → 4.0.4, API 9.0.1 → 9.7.0) uncommitted, plus untracked `refactor/`. Same state as the Mac.

**Cleanup:** all `sbd-*` containers, volumes and networks were removed, along with the `/stacks/temp/sbd-p1` and `/stacks/temp/sbd-p5` scratch dirs. The pulled images (`alpine:3.20`, `golang:1.25`, `cadvisor:v0.52.1`, `postgres:17-alpine`, core 4.0.4, API 9.7.0) were left in the local image cache.

## Linux x64 test list (all done or dropped, 2026-10-26)

| # | Test | How |
|---|---|---|
| ~~P1~~ done | Repeat on x64: time to RPC/metrics, metric families, no BTC RPC errors | `live-tests/p1-compose.yaml`, defaulting to the LAN BTC node `192.168.8.252:8333` (no tunnel needed on the LAN). Recipe in [live-tests/README](./live-tests/README.md) |
| ~~P5~~ done | SHA-256 throughput (coreutils / OpenSSL / perl / busybox / Go) and cAdvisor on native Linux cgroup v2 | `live-tests/p5-sha-cadvisor.sh` |
| ~~P6~~ done | Testnet smoke test on x64 | `live-tests/p6-testnet-smoke.sh 4.0.4 180` |
| ~~(opt) P2~~ done | Repeat the API-down stall on x64 | See live-tests README |
| ~~(opt) P3~~ done | `STACKS_EVENT_OBSERVER` duplicate delivery (D54) | Add the env var to the P1 node and count deliveries |
| ~~(opt) Q24 check~~ dropped | Restore an older pair into newer pins | **Not needed:** the owner confirmed (2026-10-26) that an older archive into newer versions within a major is a known-good path. Recorded in 15 and 11 Q24 |

Record results in [15](./15-verification-log.md) next to the macOS results (same format: setup, table, consequences). Update the affected docs and this file.

**Test Bitcoin node:** `192.168.8.252`, P2P 8333, RPC 8332, **RPC requires auth** (a follower doesn't need it). Use it in scratch configs only; the repo default stays `bitcoin.mainnet.stacks.org`. From the Mac off-LAN, reach it through the SSH host `ztnuc` (see 15).

**Mac-specific notes:** Docker Desktop refuses bind mounts from the session's `/private/tmp` scratch dir, and the interactive shell aliases `sed` to `gsed`.

## Phase 0: implemented 2026-10-26 (uncommitted, owner go-ahead given)

On `chore/refactor`, nothing committed. Verified locally on the Linux host: shellcheck (error level) clean, 24/24 compose combinations, harness 50/50, `check-config` on all rendered configs, boot PASS mainnet 120 s and testnet 180 s, no `sbd-*` leftovers.

| Part | Files | Notes |
|---|---|---|
| **CI safety net** | `.github/workflows/ci.yaml`, `.github/ci/{lib,compose-config,check-config,boot-smoke}.sh` | Jobs: `static` (shellcheck `-S error`, compose config for every network × profile × flag, fails on unset vars), `harness` (runs only once `refactor/` is committed), `check-config` (renders with the real `manage.sh -a start` and docker stubbed, then validates with the pinned node/signer images), `boot` (mainnet + testnet, 180 s, no restarts/panics). Boot runs as project `sbd-boot` with renamed containers, no host ports and its own network, so it's safe next to a real stack. Before the fix, `check-config` and `boot` both reproduced D50. actionlint clean |
| **Seed hotfix** | `scripts/seed-chainstate.sh`, README, `docs/issues.md` | **D01:** running check → snapshot selection → confirm (`--yes`; non-interactive without it refuses) → download + verify both → only then delete. **D48:** empty `stacks-blockchain/` and `postgres/` contents, never the dirs; `event-replay/` is kept. **D52/Q24:** newest same-date pair ≤ pins from the archive listing; warns when older than the pins, **warns when a newer snapshot exists (owner, 2026-10-26)**; exit 1 with the reason and the latest snapshot otherwise. Also `curl --fail`, size via ranged GET (D53), `--dry-run` (no root), `SEED_DOWNLOAD_DIR`, reuse/resume of existing files, delete only what this run downloaded, SCRIPTPATH = repo root (D17), the import container is stopped only if this run started it. **Tested:** `--dry-run` selection for 8 pin sets (matches the Q24 examples); root / non-interactive refusals leave the data untouched. **End to end on testnet [V]** (15 "Seed hotfix end to end"): found and fixed two bugs that predate Phase 0: `pg_restore` failed on the dump's `hirosystems` owner role (`--no-owner --no-privileges`), and data was chowned to root without a tty (`SUDO_USER`). Run 2 passed, and the seeded 4.0.1/9.0.2 data booted on 4.0.4/9.7.0 at tip 848 711. Still untested: the running check against live containers |
| **Testnet config** | `conf/testnet/Config.toml.sample` | Network definition verbatim from stacks-core `main` `testnet-follower-conf.toml` @ `097641bd`, with the repo's deployment keys in the order `manage.sh` expects. No sync script / scheduled PR yet (Q27 mechanism, phase 1) |
| **API observer** | `manage.sh` `update_configs`, both samples, `docs/config.md` | Renders `timeout_ms = ${API_OBSERVER_TIMEOUT_MS:-60000}` into the API observer block only (custom observers untouched); non-numeric → exit 1. **Existing users' `Config.toml` is rewritten on the next start** (intended, D55). Goldens re-recorded: the only diffs are `timeout_ms` and the testnet sample |
| Other | `scripts/setup-bns.sh` (SC2068 quote), `refactor/harness/run.sh` (`LC_ALL=C`), `refactor/key.js` (loop), `live-tests/p5` (`advtcp`) | `sample.env` untouched: only the owner's pin bump |

Still open: commit `refactor/` (the harness job needs it); the owner's `sample.env` pin bump; then phase 1.

## Phase 1: done locally (2026-10-27), not yet committed

The work waits on commit signing: the forwarded gpg agent went away after 22:56, and the commits are signed with the owner's smartcard key. Slices 1+2 are staged; slice 3 is in the working tree. Verified locally:
- shellcheck: clean at error level
- compose config: 20 combinations
- harness: 52/52
- `check-config`: every rendered profile set valid
- compose-only boot: testnet `node,api`, and mainnet all profiles all healthy with `down` in 14 s
- live event-replay job
- actionlint: clean
- no `sbd-*` leftovers

| Slice | What |
|---|---|
| 1. `config` renderer | `tools/config` (Go, go-toml v2). Renders `conf/<net>/Config.toml`/`Signer.toml` from the `*.sample` templates, `.env` and `*.overrides.toml`. Observers and `stacker` follow `COMPOSE_PROFILES`; BTC RPC credentials are optional; values are escaped; a missing signer key fails naming the variable; a hand-edited file is moved to `*.legacy` once; `Signer.toml` is 0600. The image is built locally (`pull_policy: build`, so every `up` rebuilds it from cache); its build runs the unit tests, and its context is just sources + templates |
| 2. Root `compose.yaml` | `name: stacks-blockchain-docker`. Profiles `node`/`api`/`signer`/`proxy`/`bns`/`event-replay`; `config` → `config-check`/`signer-config-check`; healthchecks; `stop_grace_period`; per-service env allow-lists (shared API anchor); `conf/<net>/api.env` (`STACKS_CHAIN_ID`); `DATA_ROOT`/`*_DATA_DIR`; metrics on `METRICS_BIND_ADDR`; `compose.postgres-port.yaml` (127.0.0.1). **The node starts after its observers (API, signer) and stops before them** (15 "Phase 1 root compose.yaml") |
| 3. `manage.sh` on the root project | `compose-files/` and `conf/mocknet/` are removed. Flags map to profiles; the default start is still `node,api,proxy`. `sed` rendering, `ordered_stop` and the mocknet `.env` toggles are gone. A stack still running from the old layout is stopped node-first, and its containers/network are removed. `reset` empties dirs (D48) and follows `DATA_ROOT`. Import requires the file and export doesn't (D09); the replay dir is per network. The BNS-files check runs only for normal starts. The seed uses `compose.yaml` and the data-path variables. Goldens re-recorded after review; 2 new scenarios (old-layout stop, import with file) |
| 4. Docs | README (plain-compose quickstart, Compose ≥ 2.24.0), `docs/usage.md` (compose equivalents), `docs/config.md` (generated configs + overrides, new variables), requirements/docker |

Intended behavior changes, for the changelog:
- **Configs are generated:** `Config.toml`/`Signer.toml` are rewritten on every start, and custom settings go in `*.overrides.toml`.
- **Mocknet is removed.**
- **Bindings:** metrics and postgres bind to 127.0.0.1.
- **The project name changes** from `compose-files` to `stacks-blockchain-docker`; `manage.sh stop` handles a stack still running under the old one.

Not done:
- the Q27 sync script for the upstream templates (run by hand). **A CI job that opens PRs is deferred by the owner (2026-10-27):** no CI job gets write/create-branch permission by default; a read-only drift check is fine;
- a live `manage.sh` boot (its fixed container names conflict with the `sbd-*`-only rule; the stubbed harness + compose boot cover it);
- 12-monitoring-plan still describes the old overlay mechanism.

## Upstream template sync (Q27): tier 0 done 2026-10-27; evaluate before any upstream PR

**Tier 0 (no write token anywhere):**
- `tools/sync-upstream-conf.sh` is run by hand. It vendors the upstream follower samples into `conf/upstream/` (recording the commit in `SOURCE`, which is only bumped when a file changes), regenerates `conf/<net>/Config.toml.sample` from those plus `Config.deployment.toml`, and runs `check-config`. The maintainer reviews, makes a signed commit and opens the PR.
- `--check` is read-only.
- `.github/workflows/upstream-drift.yaml` runs it daily and on dispatch:
  - `contents: read`, no secrets;
  - only `actions/checkout`, pinned by SHA with `persist-credentials: false`;
  - it reports drift by failing.

Verified in 15.

**Tier 1 (an automated PR) is deferred.** The owner wants it done carefully. The proposed controls:
- triggered by `schedule` only;
- a protected Environment (default branch only, required reviewer, prevent self-review) for the write step;
- a split `fetch` (read-only) / `open-pr` job; the `open-pr` job runs no repo or upstream code, applies the patch only to allow-listed paths, pushes only to `bot/upstream-sync`, and uses `gh` only;
- actions pinned by SHA;
- a ruleset on the default branch: PRs, CODEOWNERS, signed commits, approval of the latest push, no bypass for Actions;
- no auto-merge; read-only default workflow permissions; no PAT or App.

**Tier 1 scope addition (owner, 2026-10-27):** besides the config templates, the automated job should also detect **new image tags**: `ghcr.io/stacks-network/stacks-core` (with `stacks-signer`, released in step) and `hirosystems/stacks-blockchain-api`. It would propose the `sample.env` pin bump in the same controlled way. Points to settle when tier 1 is built:
- which tags count: stable semver only, no `-rc`, `latest` or `develop`;
- that the stacks-core and signer versions match;
- that a Hiro archive snapshot exists for the new pins (Q24), or else a warning in the PR body;
- that the PR runs `check-config` plus the boot test against the new images.

Until then, the read-only drift workflow could report new tags the same way (fail, no write). That's open for the owner to decide.

**Before PR'ing any of this upstream:** evaluate whether `stx-labs/stacks-blockchain-docker` allows it at all. That covers the scheduled workflow, the Actions settings it needs, and possibly the vendored `conf/upstream/` approach itself. The repo settings and ruleset above are recommended for the fork regardless; they're GitHub UI/API changes, made only with the owner.

## Phase 2M monitoring: done (2026-10-27)

**The `monitoring` profile** (opt in through `COMPOSE_PROFILES` or `./manage.sh -f monitoring`):
- **Services:** Prometheus, Grafana, cAdvisor, node-exporter and postgres-exporter, as `stacks-*` containers.
- **Exposure:** everything is published on `MONITORING_BIND` (127.0.0.1) and gets explicit environment only.
- **Scrape targets** follow `COMPOSE_PROFILES`: the config job renders `conf/monitoring/prometheus.yml`.
- **Rules:** `conf/monitoring/rules/stacks.yml` holds 13 recording and alert rules, with promtool unit tests in `tools/monitoring/`.
- **Grafana:** 3 provisioned dashboards, a required admin password, no anonymous access.
- **Switches** (0/1): `MONITORING_GRAFANA`, `MONITORING_CONTAINER_METRICS`.
- **Data:** named volumes.
- **Ordering:** the node stops before Prometheus.
- **CI:** the compose matrix covers it, plus a secrets assertion, promtool check and tests, and dashboard JSON validation. A harness scenario covers `-f monitoring`.

Verified live (15 "Phase 2M"). Docs: `docs/monitoring.md`.

**Deviations from doc 12:**
- named volumes instead of `persistent-data/monitoring`;
- `stacks-` container prefixes;
- the switches are 0/1 (Compose limitation);
- cAdvisor is v0.55.1 (v0.52.1 sees no containers on Docker 29).

**Not done:**
- ~~Alertmanager (M5, later);~~ **done 2026-11-05 with Grafana alerting** (owner: Grafana for standardization; off by default; promtool can't test Grafana's evaluation, acknowledged). `MONITORING_ALERTS=1` makes the config job generate `conf/monitoring/grafana/provisioning/alerting/stacks.json` from the alert rules in `stacks.yml` (one source, still promtool-tested): one Grafana rule per alert (query A = the expression, B = `$A == $A`, same `for`, labels, annotations with `$value` → `$values.A.Value`), a contact point with the configured receivers (`GRAFANA_ALERT_WEBHOOK_URL`, `GRAFANA_ALERT_EMAIL` + `GRAFANA_SMTP_*`, referenced as `$__env{...}` so no secret is in the file), and a policy grouping by alertname/job, repeat 4 h. Refuses without a contact point or with `MONITORING_GRAFANA=0`; `MONITORING_ALERTS=0` removes the file. Verified live (testnet scratch + a webhook receiver): all 10 rules healthy, `StacksTargetDown` went pending during header sync and was delivered 15 min later with labels and templates expanded; rule errors are notified too (`DatasourceError`). Found: Grafana expands only `$__env{...}` in alerting files and keeps `$$` literally;
- the `stacks-status` companion (M3, deferred);
- ~~a burn-height/"behind Bitcoin" alert (needs a reference);~~ **done 2026-11-05:** `StacksBurnchainStalled`, no burn-height change for 2 h (with 2 h of history, so restarts don't trigger it) plus 30 min. No external reference: the soak saw routine gaps up to 92 min, and replaying 2 days of Run A it never went pending;
- an event-age alert (the units of `stacks_last_event_timestamps` aren't verified);
- trimming the collected series (M7, below).

## Phase 3a: containerized seed job, done (2026-10-28)

`tools/seed` (Go) in an image built from `postgres:${POSTGRES_VERSION}-alpine`; compose service `seed` (profile `seed`): `docker compose run --rm seed [--dry-run] [--yes] [--keep-downloads] [--chainstate F --pgdump F]`.
- **Guard:** refuses while node, API, postgres or signer is up; their service names resolve on the network only while they run. Exit 1.
- **Confirmation:** a TTY prompt, or `--yes`/`CONFIRM=yes`; without either it exits 2.
- **Selection:** Q24 pair rule, the same as the shell script.
- **Downloads:** both in parallel, single stream each, hashed while written, resumable (a partial file is re-hashed, then a Range request continues it). Retries with backoff; 404/403 fail fast. A `.sha256` sidecar is written next to each verified archive.
- **Local mode:** the names are parsed and the same Q24 rules applied, then verified against the sidecar if present.
- **Build:** chainstate extraction (streamed zstd + tar, path-escape checks, ownership from the data dir) and `pg_restore -j` (own temporary server: initdb with the `.env` user/password, bulk-load settings, renames) run in parallel, each into `<dir>/.seed-staging`. Only after both succeed are they swapped in. The dirs themselves are kept (D48); a failure leaves the old data.
- **Cleanup:** downloads this run created are deleted unless `--keep-downloads`; your own files never are.
- Fixes D01, D17–D19, D43, D48, D49 (no second read), D52, D53 for the compose path. `scripts/seed-chainstate.sh` stays as the fallback.

**Phase 3c (2026-10-28), done** (15 "Phase 3c mainnet seed"):
- **Mainnet seed verified:** a 1 h 01 m download (4 connections, about 100 MiB/s), then with `--defer-index nft_events_asset_identifier_index` a 2 h 00 m restore, and node + API up **39 s** after `up`. The deferred index builds in the background (`build-indexes`), and the stack in `/stacks/temp/sbd-mainnet` was left running for it.
- **Native extraction:** GNU tar with zstd/xz/gzip. The seed image is **the stack's postgres image** (`postgres:<ver>-alpine`): a Debian (glibc) restore server builds text indexes that the Alpine (musl) postgres finds misordered (amcheck failures, API ingestion crashes; 15 "Run 3"). musl extraction is about 40% slower than glibc, but runs in parallel with the longer restore. The owner chose to keep Alpine for the stack.
- **Node-local files:** `peer.sqlite*` and `event_observers.sqlite*` from the archive node are not extracted; the node creates them. This didn't change the burn-height lag after a seed, which is stacks-core pacing sortitions during catch-up.
- **Re-seed over used data:** the restore gets search permission on the 0700 postgres data dir; a failed restore cancels the extraction; `build-indexes` ignores server warnings on stderr.
- **Deferred indexes:** `--defer-index` / `SEED_DEFER_INDEXES`, and the `build-indexes` job (api profile). Verified on testnet and mainnet.
- **Restore tuning:** parallel maintenance workers, `SEED_PG_MAINTENANCE_MEM`, and restore progress (database size and current steps).

**Earlier in Phase 3c:**
- **Segmented downloads:** `SEED_CONNECTIONS` parallel Range requests per file (default 4), still hashed while downloading: chunks are read back in order from the page cache. Resumable through `<file>.parts`, retried per chunk, with a single-stream fallback if the server ignores ranges.
- **Local formats:** `.tar.gz`/`.tgz`, `.tar.xz` and `.tar` archives.
- **Mainnet run:** see 15 "Phase 3c".

**Not done:** a streaming download-to-extract mode that doesn't store the archive (no resume, so of limited value).

## Phase 3b: reset job, done (2026-10-28)

`seed reset` subcommand in the seed image; compose service `reset` (profile `reset`): `docker compose run --rm reset [--yes]`.
- **Guard and confirmation:** the same as seed (refuses while the stack runs; TTY prompt or `--yes`/`CONFIRM=yes`, else exit 2).
- **What it deletes:** it lists, then empties, the chainstate, postgres and signer dirs. It keeps the dirs themselves (D48) and needs no sudo.
- **`manage.sh -a reset`** keeps its own existence and running checks, then runs the job.

Verified live (15 "Phase 3b").

## Event replay dropped (owner, 2026-10-28; Q28)

Archive snapshots replace it as the way to resync. Removed:
- the `event-replay` compose service and profile (postgres is in `api` only);
- `STACKS_EXPORT_EVENTS_FILE` and the event-replay data dir;
- `manage.sh`'s replay guard (4 `docker logs` calls on every start/stop) and the `event_replay` function. `-a import` and `-a export` now exit 1 pointing to `docker compose run --rm seed`.

Docs: `usage.md` and `upgrade.md` now point to re-seeding; `issues.md` covers recovery via seed and BNS v1 on API 9.x. Harness: the replay scenarios are replaced by `import-removed` and `export-removed` (48/48). D09/D37/D38 are closed; contract §A.7 and §A.23 retired.

## Upstream tracking: API index rebuild (2026-10-28)

[upstream-api-index-issue.md](./upstream-api-index-issue.md) tracks the rebuild of the API's hash index `nft_events_asset_identifier_index` on mainnet (**19 h 25 m**, measured): evidence, `pgstathashindex` structure, and a comparison of index options on the same data (a partial B-tree builds in 33 s and queries as fast; `COUNT(*) OVER()`, not the index, makes hot identifiers slow), plus the remaining checklist. **Tracking only; nothing filed upstream yet.** Locally it's mitigated by the seed's `--defer-index` + `build-indexes` job.

## Phase 2: `sbd` CLI core, first increment (2026-10-29)

`tools/sbd`: a Go module, standard library only, that shells out to `docker compose` (Q17) from the checkout (found upwards from the current dir, or `-C`).
- **Model:** each command that changes something is a **plan**: `.env` edits plus `docker compose` argv (R4). `sbd plan <cmd>` / `--dry-run` prints it (`--json` too), and running the command executes exactly those steps. Guards (live state checks) run only on execution, so plans are deterministic.
- **Commands:** `start|up` (`--network`, `--with`, `--without`, `--profiles`, persisted to `NETWORK`/`COMPOSE_PROFILES` in `.env`, R2), `stop|down`, `restart`, `status [--json]` (0 running / 1 not, §A.4), `logs|log [svc] [--tail N] [--no-follow]`, `logs export` (`exported-logs/<container>.log`), `pull|upgrade`, `seed` (arguments passed to the job unchanged), `reset`, `bns`, `config render`, `env get|set`, `doctor`. Global: `-C`, `-H/--host` (DOCKER_HOST), `--dry-run`, `-y/--yes`, `--json`.
- **Contract kept:** `.env` auto-created from `sample.env` and backed up once per command to `.env.save` (§A.11); refuse `start` while running and `seed`/`reset`/`bns` while running (§A.8, the jobs refuse too); signer needs its key for the network (§A.10); no `-t` on `down` (stop grace period). Exit codes per Q1 (2 usage, 1 failure); a docker command's own exit code is passed through (a job's 2 = "needs --yes").
- **Doctor:** Compose ≥ 2.24.0 (fail) / ≥ 2.27.2 (warn), daemon reachable, `.env`, `NETWORK`, `COMPOSE_PROFILES`, signer key, `docker compose config`, and containers running outside `COMPOSE_PROFILES`.
- **Tests:** plan goldens for every command, guards, exit codes, `.env` round trips, status (both `ps --format json` shapes), doctor, and **`TestPlansAreDocumented`**: every compose command a plan runs must appear in `docs/usage.md`, which now leads with a plain-Compose ⇔ `sbd` table. CI runs gofmt/vet/tests in the `static` job.
- **Verified live** (scratch project `sbd-cli`, testnet node+api, via `COMPOSE_PROJECT_NAME`/`COMPOSE_FILE` in its `.env`): doctor, plan, `env set` creating `.env`, start, status/`--json`, refusals (start/reset while running), logs, logs export, stop (10 s), status exit 1, `reset --yes` without a TTY. The seed job refuses even its own `--dry-run` while the stack runs.

**`manage.sh`: no shim (owner, 2026-10-29).** It is transitional (removed in phase 7), so it is not rewritten over `sbd`: its behaviour stays as it is, it prints a deprecation notice on stderr, and the docs mark it deprecated. Harness goldens were re-recorded for the notice only (48 files: the notice line; an empty output's blank line and, in the arm64 prompt scenario, the 30th line move accordingly). Phase 7 removes `manage.sh` and its harness; the scenarios that matter (refusals, exit codes, signer key) are covered by `sbd`'s tests.

**`sbd migrate` (2026-10-29):** brings an old-layout install up to date without moving data; `sbd plan migrate` previews it.
- Adds `COMPOSE_PROFILES` when `.env` lacks it (`node,api`, plus `signer` when the old `Config.toml` had `stacker = true` **and** the network's signer key is set); without it `docker compose up -d` starts only the config job.
- A hand-edited `Config.toml` (or the `Config.toml.legacy*` the config job moved aside): runs **`docker compose run --rm config suggest`**, a new renderer subcommand that writes `Config.overrides.toml.suggested` with the user's own settings, for review (it never writes `Config.overrides.toml`). Left out: upstream network-definition keys (bootstrap node, epochs, balances: the D50 trap), the rendered API/signer observers, and keys rendered from `.env` (BTC peer/ports/credentials, `auth_token`, `prometheus_bind`, `stacker`), which are reported with the variable to set. Keys the new config lacks are listed for checking.
- Reports obsolete `.env` keys (`STACKS_EXPORT_EVENTS_FILE`), an unused `event-replay` data dir, and running containers of the old `compose-files` project.
- Verified end to end on a copy with the `d2e558c` `.env` and a hand-edited `Config.toml`: profiles added, suggestion correct (two user edits and a custom observer; nothing from the network definition), accepted, rendered, and `check-config` passed.

**`sbd doctor` host and disk checks (2026-10-29):** replace `manage.sh`'s arm64 prompt (warnings only, Q12).
- **Resources:** memory and CPUs from `docker info` (8 GB wanted for mainnet with the API, else 4 GB; 2+ CPUs).
- **Storage:** Docker Desktop's bind mounts are slow for chain data (mainnet → use a Linux host); on Windows, a data path outside the WSL filesystem is slow.
- **Disk:** per enabled component (`node` → `STACKS_DATA_DIR`, `api` → `POSTGRES_DATA_DIR`, resolved like compose.yaml), the free space on its filesystem (nearest existing parent) against the estimate + 20% for a component without data, or growth headroom (50 GB mainnet / 5 GB testnet) where data exists; needs on one filesystem add up. Estimates from this repo's seeds: mainnet node 620 GB, API 510 GB; testnet 25 + 25 GB. Skipped for a remote Docker host (`--host`, tcp/ssh `DOCKER_HOST`).
- Free space via `statfs` (unix) or `GetDiskFreeSpaceExW` (Windows); vets/builds for linux, darwin, windows, freebsd. `docs/requirements.md` rewritten with the measured sizes (it said 150 GB).

**Next increments:** release builds (phase 4).

## Phase 6: `sbd init`, done (2026-11-05, revised 2026-11-06)

Owner, 2026-11-05: "init", not "wizard"; still needed. Walked through step by step with the owner on 2026-11-06; the steps and their reasons are in 13 §1. Highlights: preflight warns only; Enter keeps the existing network, else mainnet; node and API always run, signer off by default and explained before its key is asked; a real Bitcoin P2P handshake; no TLS in the proxy (port 80 only); generated secrets listed at the end like Helm notes; an existing chainstate is kept unless the user asks to seed or resync. `--from answers.env`, `--yes`; refuses without a terminal otherwise. Writes only `.env` (then optionally runs seed/reset/start). Dependency: `golang.org/x/term`.

**Follow-ups from the walk-through:**
- ~~**HTTPS on 443 as the default**~~ **Done 2026-11-13 (owner: Caddy, HTTP by default):** the `proxy` profile is Caddy (`caddy:2.11.9-alpine`, service `proxy`). HTTP only unless `PROXY_TLS=1`: then HTTPS on 443 and HTTP redirects, with a Let's Encrypt certificate for `PROXY_DOMAIN` (Caddy obtains and renews it; certificates in the `proxy-data` volume) or a self-signed one without it; `PROXY_ACME_EMAIL` optional. Verified: all three Caddyfiles pass `caddy validate`; HTTP and self-signed modes tested live against a stand-in API (headers, redirect, certificate, healthcheck). **Still to verify: the Let's Encrypt mode with the owner's domain `wileyj.net`** (DNS to the public IP, ports 80/443 forwarded to the host). `NGINX_PROXY_PORT` became `PROXY_HTTP_PORT` (`sbd migrate` says so); the old `nginx` container needs `down --remove-orphans` (docs/upgrade.md).
- ~~**Enforce node + API in the config job**~~ **Done in code 2026-11-13:** the config job rejects a `COMPOSE_PROFILES` without both (the same message as `sbd`); CI's boot matrix is now `mainnet node,api` and `check-config.sh` no longer renders node-only. **When the PR to `refactor-main` is opened** (owner: not yet), switch ruleset 25988043's required check `compose-only boot (mainnet node)` to `compose-only boot (mainnet node,api)` **and drop `manage.sh characterization harness`** (phase 7 removed the job) in the same go, or the PR waits on checks that no longer run: `gh api repos/wileyj/stacks-blockchain-docker/rulesets/25988043` (read), then PUT it back with the context renamed.
- **The default mainnet Bitcoin peer `bitcoin.mainnet.stacks.org:8333` is overloaded** (2026-11-06): it accepts TCP and turns most handshakes away (`est.stacksnodes.org` is the same machine). A real stacks-node still synced from it after 3 "Connection broken" retries (16/32/60 s), about 2.5 min. Owner: many nodes use it (thundering herd); its capacity won't change (cost); `cet`/`sgt.stacksnodes.org` answer but aren't public and may go away, so no alternates are configured. **Decided:** recommend running your own Bitcoin node (sample.env, requirements, README, config); `sbd init` doesn't probe the default peer and handshakes only a peer the user enters.

## Phase 6: `sbd ui` dashboard, done (2026-11-09)

Designed with the owner one decision at a time (13 T6–T8, T4 revised) and built in `tools/sbd/ui.go` (`uidata.go` for the data):
- **Data:** the `docker` CLI only (no Docker SDK): `compose ps` + `inspect` every 5 s and on `docker events`, one `docker stats` stream, `compose logs -f` for the selected service; node and API over HTTP at their published local ports, else (Linux) the container address. Disk IO comes from the container's cgroup `io.stat` (docker stats rounds to three digits, too coarse for a rate; the busiest device counts, since LVM IO shows on both the LV and the disk below).
- **Synced:** latest API block under 5 min old; with `SBD_UI_REFERENCE_API` set, within 15 blocks of that API's tip. No outbound calls by default.
- **Main screen (T9):** PROBLEMS (state checks only, no log matching) and POX (cycle, phase, stacked STX, signatures on the latest block out of the cycle's signers) replaced the log pane; logs open full-screen on `l` and are only followed while open (dashboard CPU then 0.7%).
- **Actions:** `s`/`x`/`r`/`p`/`e` run the CLI itself (`sbd start|stop|restart|pull|logs export`) in the terminal and return on Enter; stop and restart ask y/N. Destructive jobs and settings stay CLI-only (the help screen lists them).
- **Libraries:** Bubble Tea `v2.1.0` and Lip Gloss `v2.0.6` (first built on `v2.0.9`: `v2.0.10`+ needs Go 1.26).
- **Verified (2026-11-09):** against Run A (read-only: views, `x` then `n`, `e`), and start, restart and stop from the dashboard on a scratch testnet project (`sbd-ui-test`, removed). The dashboard used 1.1% CPU (plus 0.3% for its docker children) and 16 MB at 80×24 with the node logging ~20 lines/s, after batching stats per second and log lines per 250 ms. Quitting leaves the stack running and no child processes.

**Follow-ups:**
- **Go toolchain: done (owner, 2026-11-09).** Go 1.25 left upstream support with Go 1.27, so the three modules (`go 1.27.0`), their Dockerfiles, CI and the release workflow moved to Go 1.27 (1.27.3 at the time). All three images build (the seed image runs its tests in the build), and sbd/config vet and tests, compose-config (29/29), check-config and the harness (48/48, as before) pass.
- **`sbd update <node|api|signer|proxy> [--version V]`: done 2026-11-13 (owner: command line, not the dashboard).** Recreates one service with its pinned (or a new) image while the rest runs: pull, `config`, `up -d --no-deps --force-recreate`. Refuses a stopped service and a signer newer than the node; postgres stays `sbd db upgrade`. Tested on a scratch testnet stack: only the API container was replaced, the node's kept running.
- **Signer panel: done 2026-11-13 (T10).** Tested with the owner's dry-run signer's real `/metrics` (`tools/sbd/testdata/signer-metrics.txt`). Dropped: alerts panel (PROBLEMS covers it), replay status. Still parked: Prometheus history, per-service restart, remote access, mouse.

## Phase 5: soak (started 2026-11-03, in progress)

Owner, 2026-11-03: a 7-day Run A, then review; a genesis-sync Run B only until past the heavy blocks (Q26), not for days. Details in 15 "Phase 5".
- **Run A** (`/stacks/temp/sbd-soak-a`, mainnet node,api,monitoring, snapshot 20261103, `2f5cb31`): at the tip since 2026-11-04 01:08; through the prepare phase at lag ≤ 1, 0 restarts. Cycle 147 crossed at the tip. Graceful restart: stop 7 s, ready 14 s, at the tip in 46 s. 15-min API outage: the node held its height (as designed), resumed 162 s after the API returned (the 180 s backoff cap, Q26), at the tip after 184 s. **7-day report done (2026-11-10, 15 "Phase 5"):** 0 restarts all week, API never more than 1 block behind the node, all 56 661 `/new_block` deliveries 200 (max 14.7 s), memory levelled off, within 15 blocks of the public tip 98.0% of the time; 31 stalls (3.6%), the run ending inside the longest, which lasted **6 h 30 m** (21:57 → 04:27 UTC, `StacksTipStalled` firing from 22:33): one missing tenure, 1 786 download attempts, mostly unanswered or "not found", finally served by another Hiro node carrying the seed's key (not publicly reachable now). Found 2026-11-10 (15 "Phase 5"): 29 stalls behind the network over 4 min (4.7 h, 3.0%; longest 40.9 min), stacks-core slow to fetch one missing tenure from its outbound peers (mostly the Hiro seed, whose connections kept dying); not fixable here (owner), data kept in `/stacks/temp/sbd-soak-results/run-a/`; any node behind NAT is exposed, particularly a miner (a stall in its own tenure means no blocks until the tenure changes) unless it peers directly with public, semi-private nodes; host behind NAT with 0 inbound peers, one seed in the template, 3 of the owner's 4 known seeds offline. `StacksTipStalled` (30 min) caught only the longest. And large `/new_block` deliveries got ~7x slower once the deferred hash index went valid (11-05 22:10): to go in the upstream index notes. **Follow-ups:** whether `StacksTipStalled`'s 30 min should come down (most stalls were 4–25 min). **Stall experiments on Run A (owner, 2026-11-11), one change at a time, tracked by `stallwatch.sh` into `stalls.csv`:** (1) a second seed, the owner's LAN node, connected from 19:14 UTC (Run A's peer DB reset to make it dial the seed), for ~24 h; (2) then publish Run A's P2P port 20444, which its soak override had left unpublished (so 0 inbound was partly the setup, not just NAT), for another ~24 h. Plan and data: `/stacks/temp/sbd-soak-results/run-a/stalls-NOTES.md`.
- **Run B** (`/stacks/temp/sbd-soak-b`, genesis sync): stopped after milestone 3 at height 334 506. **Q26 resolved**: genesis block 37.8 s, everything after ≤ 12 s, all 347 105 deliveries 200. Data kept until the owner decides.
- **Fixed from the soak:** `2f5cb31` seed restores without `pg_restore --create` (the 20261103 dump's `ALTER ROLE hirosystems IN DATABASE`); `6f03efc` seed runs `vacuumdb --analyze-only` (12–15 s per `/new_block` without statistics); `build-indexes` warns not to VACUUM/ANALYZE the table during the build (a manual ANALYZE deadlocked the build after 21.8 h; the retry worked as designed); `sbd` hides secret values; API pin 9.7.1 (`980516f`; the snapshots since 20261027 are 9.7.1).
- **Owner, 2026-11-03:** the job images (config, seed, pg-upgrade) are always built locally by compose, for security; they are not published.

## Phase 4: release pipeline and docs (2026-11-02)

Owner, 2026-11-02: "for ci, contents write and packages write is acceptable" (release publishing only; still no branch-pushing or PR-opening jobs).
- **Linux x86-64 only for now (owner, 2026-11-02).** The code builds for darwin, windows, freebsd and arm64 too (checked locally); adding targets later means extending the target list in `release.yaml`, an arm64 runner for the image, and a macOS/Windows test job in CI (drafted and dropped on 2026-11-02).
- **`.github/workflows/release.yaml`**, on a pushed `v*` tag only: `binaries` (vet, test, `linux/amd64` with `-X main.version=<tag>`, `SHA256SUMS`, artifact) → `github-release` (`contents: write`; `gh release create --verify-tag --generate-notes`, prerelease for `vX-suffix`) and `image` (`packages: write`; `ghcr.io/<owner>/sbd:<tag>`, and `:latest` for a final release). GitHub-owned actions only (checkout, setup-go v7.0.0, upload/download-artifact), pinned by SHA; `docker login` with the job token.
- **Image** `tools/sbd/Dockerfile`: `docker:28.5.2-cli` (Compose v2.40.3) + sbd; run with the socket and the checkout at the same path. Verified locally: `version`, `plan`, `doctor` against the host daemon.
- `sbd version` (`dev` unless stamped).
- **First release `v0.1.0-rc.1`** (signed tag on `152c3ea`, 2026-11-02): all three jobs passed. The Release is a prerelease with `sbd-v0.1.0-rc.1-linux-amd64` and `SHA256SUMS` (checksum verified, binary prints `sbd v0.1.0-rc.1`); `ghcr.io/wileyj/sbd:v0.1.0-rc.1` (amd64) pulls anonymously (public) and prints the same; no `latest` for the prerelease. (15 "Phase 4")
- **Tag protection (owner, 2026-11-05: layers 1–4; a `release` environment with a required reviewer may come later).** (1) Ruleset `release tags` (id 26650371) on `refs/tags/v*`: creation, update and deletion restricted, bypass only for the repository admin role. (2) `release.yaml` first fetches the tag object and requires GitHub's `verification.verified: true` for it (a GPG or SSH signature from a key registered on the tagger's GitHub account), so no key lives in the repo and it works unchanged upstream for any maintainer (owner, 2026-11-05: option 1, no allow-list; a first version checked a key file in the repo, which tied releases to the owner's key). A lightweight tag fails (no tag object); checked against `v0.1.0-rc.1`: verified, reason `valid`. (3) Immutable releases enabled. (4) `actions/attest-build-provenance` v4.2.2 on the binary + `SHA256SUMS` (github-release job) and the image (pushed to the registry); `id-token: write` + `attestations: write` on those jobs, approved. **Exercised 2026-11-06 with `v0.1.0-rc2`:** ruleset bypass, GitHub-verified signature, immutable prerelease, and both attestations verified with `gh attestation verify` (15 "Phase 4 release protections").
- **Docs rewrite, two equal paths:** README leads with plain Compose and `sbd` side by side (quickstart table, seed, services), drops the aria2 build guide and the `manage.sh` quickstart; `docs/usage.md` is task-first (table, services, stop, logs, reset, seed, pg-upgrade, BNS, getting `sbd`) with `manage.sh` as a deprecated appendix mapping old commands; `upgrade.md` gains PostgreSQL majors and old-layout installs; `issues.md`, `docker.md`, `config.md`, `monitoring.md` lose the stale `manage.sh`/`sudo`/deleted-script instructions.
- **Decided (owner, 2026-11-03):** the job images (config, seed, pg-upgrade) are not published; compose always builds them locally, for security.

## Phase 3d: `pg-upgrade` job and `sbd db upgrade`, done (2026-11-02)

`seed pg-upgrade` subcommand; image = the `pg-upgrade` stage of `tools/seed/Dockerfile` (`postgres:<ver>-alpine` + Alpine's `postgresql<old>` packages for every older major it ships); compose service `pg-upgrade` (profile `pg-upgrade`): `docker compose run --rm pg-upgrade [--dry-run] [--yes] [--mode auto|clone|copy|link] [--skip-analyze]`, or `sbd db upgrade`. Replaces `scripts/postgres-upgrade.sh` (removed; D26 fixed). Q9 as settled.
- **Method:** `pg_upgrade`. The new cluster is built by the image's own binaries (the stack's official image) and the old server is Alpine's package: musl on both sides, so text indexes stay valid (the Run 3 lesson). Source major from `<data>/PG_VERSION`; same major → no-op; newer → refuse; no package for it (≤15 today) → refuse, suggesting a re-seed.
- **Matching the old cluster:** it starts the old server on a private socket to read the install user, template0's encoding/locale/provider and `data_checksums`, and `initdb`s the new one to match.
- **Staging + swap:** `<data>/.pg-upgrade-staging`, swapped in only after `pg_upgrade` and the statistics rebuild succeed; removed on failure. `pg_hba.conf`/`pg_ident.conf` are carried over; `ALTER SYSTEM` settings are reported, not copied.
- **Modes:** `auto` → `clone` (reflink check) → `copy` if size+10% is free → else refuse and suggest `link` (stated: old cluster unusable once the new one has run).
- **Guards:** stopped stack, `postmaster.pid` (unclean shutdown), data owned by the postgres uid, `--yes`/TTY.
- **`compose.yaml`:** `PGDATA=/var/lib/postgresql/data` on postgres (the 18 images moved the default; an 18 bump would have started empty).
- Verified 16→17 (clone, copy, link) and 17→18 with real clusters (15 "Phase 3d").

## Metrics trimming (M7): done (2026-11-05)

Sized from the Run A soak (5,859 series, 186 samples/s, 42 MB/day; the rules and dashboards used about 6%). Owner, 2026-11-05: trim; app scrape interval 15 s, configurable; postgres (and the other infrastructure jobs) fixed at 30 s; retention 30 days.
- **App jobs** (stacks-node, stacks-api, stacks-signer): every metric kept; scraped every `PROMETHEUS_APP_SCRAPE_INTERVAL` (default `15s`, validated as a Prometheus duration, rendered by the config job on every `up`).
- **Infrastructure jobs** (prometheus, node-exporter, cadvisor, postgres): the global 30 s; exporter flags so they don't collect what nobody uses (node-exporter `--collector.disable-defaults` + cpu, diskstats, filesystem, loadavg, meminfo, netdev, stat; cAdvisor `--enable_metrics=cpu,memory,diskIO,network,oom_event`; postgres-exporter without the per-table, replication, roles, settings, WAL and bgwriter collectors, with `long_running_transactions`), plus a Prometheus `metric_relabel_configs` keep-list per job (`tools/config/monitoring.go`). Postgres keeps size, connections, transactions, deadlocks, locks and the oldest open transaction (nothing used it before).
- **Retention** `PROMETHEUS_RETENTION=30d`.
- **Guards:** `TestKeepListsCoverRulesAndDashboards` fails if a rule or panel uses an infrastructure metric its keep-list drops; the config image's build context now includes the rules and dashboards for it.
- **Verified** on a testnet `node,api,monitoring` scratch stack: all targets up, app jobs every 15 s, the others every 30 s; every dashboard panel and rule query returns data (the only empty ones are the signer, not enabled, and alerts not firing); **860 series** in total (node 55, API 256, infrastructure 546; was about 5,860 on mainnet, 5,392 of them infrastructure). Projection for mainnet: about 0.6 GB for 30 days.

## Exposure model: decided and done (owner, 2026-11-05)

Local by default; exposing a service is an explicit opt-in in `.env`. No host networking.
- **API (3999), node RPC (20443):** `API_BIND_ADDR` / `RPC_BIND_ADDR`, default `127.0.0.1` (**breaking**: they were on all interfaces). `curl localhost:…` works on the host; other machines are refused.
- **Node P2P (20444):** public, for inbound peers.
- **Serving the API to other machines:** the `proxy` profile (nginx `1.30.5-alpine`, pinned now), public on `PROXY_BIND_ADDR` (default `0.0.0.0`), ports 80/443. `PROXY_TLS=1`: HTTPS with `fullchain.pem`/`privkey.pem` from `PROXY_TLS_DIR` (own certificates; ACME possibly later), HTTP redirects, `/status` stays on HTTP for the healthcheck. The config job renders `conf/nginx/proxy.conf`. The API answers the node's `/v2/*` too.
- **Postgres:** reachable only inside the stack; the API is its only client ("nothing good can come from" remote access). `POSTGRES_LOCAL=1` publishes it on `127.0.0.1:POSTGRES_LOCAL_PORT` through a small nginx stream proxy (`postgres-local`, `deploy.replicas`), with no setting for any other address. Replaces `compose.postgres-port.yaml` and `EXPOSE_POSTGRES` (`manage.sh -f postgres` and `EXPOSE_POSTGRES=true` map to it).
- **Metrics, Prometheus, Grafana:** always `127.0.0.1` (`METRICS_BIND_ADDR`, `MONITORING_BIND` removed: never exposed externally); SSH tunnel from elsewhere.
- `sbd doctor` warns when the API or RPC is bound beyond localhost; `sbd migrate` explains the removed settings and the new default.
- **Verified live** (testnet scratch, TLS with a self-signed cert): bindings as above; API/RPC 200 on localhost and refused from the LAN; the proxy over another host address: `/status` ok, HTTP → 301 → HTTPS, API and `/v2/info` 200, our certificate; metrics refused from the LAN; `POSTGRES_LOCAL=1` psql via 127.0.0.1 works and LAN refused, `=0` removes the proxy. On this host the LAN address's 80/443 are taken by an unrelated Traefik (local Kubernetes ingress).

## Fork repo settings and branch model (applied 2026-10-27 on `wileyj/stacks-blockchain-docker`)

**Branches:**
- **`chore/refactor`** is the working branch. Direct signed pushes are allowed, and it has no rules.
- **`refactor-main`** is the **default branch** (created from `e866c5d`). Ruleset `refactor-main protection` (id 25988043, no bypass actors):
  - changes only through a PR, with 0 approvals (single maintainer);
  - the 5 CI jobs must pass (`shellcheck + compose config`, `manage.sh characterization harness`, `check-config on rendered configs`, `compose-only boot (testnet node,api)`, `compose-only boot (mainnet node)`), pinned to the GitHub Actions app (15368);
  - signed commits required;
  - no force-push or deletion.

  Verified: a signed direct push was rejected with GH013 "Changes must be made through a pull request". Work moves to it by PR from `chore/refactor`. The scheduled drift check runs from `refactor-main`.

**Actions:**
- only GitHub-owned actions are allowed (`actions/*`), and every action **must be pinned to a full commit SHA**; `actions/checkout` is pinned at v5.1.0 `fbc6f39…`;
- the workflow token defaults to read-only, and Actions may not create or approve PRs;
- workflows from outside contributors' fork PRs need approval.

**Security:**
- secret scanning and push protection were already on;
- Dependabot **alerts** are on; Dependabot security-update PRs are **off** (consistent with no automated PRs for now).

**Not changed:** merge methods (all allowed); auto-merge (off); no CODEOWNERS yet (add one if a second maintainer joins, together with required reviews and "prevent self-review").

## Resuming on another machine
1. Get `refactor/` there. The owner can commit and push the branch, or copy just the folder: `tar czf refactor.tgz refactor/`, then `scp` it and untar it in the clone (branch `chore/refactor` @ `d2e558c`).
2. Optionally copy the memory files (`MEMORY.md`, `refactor-plan-priorities.md`) into the new machine's `~/.claude/projects/<path-slug>/memory/`.
3. Start the session with: *"Read refactor/HANDOFF.md and follow its rules exactly (edit only refactor/, no commits, scratch `sbd-*` projects only, leave my sample.env edit alone). This is the Linux x64 host. Run P1, P5 and P6 from refactor/live-tests/, record the results in refactor/15-verification-log.md next to the macOS results, update HANDOFF.md, and clean up all sbd-* containers and volumes. Ask me before the optional tests."*

## Phase 7: `manage.sh` removed (2026-11-13)

Owner: now, not after a release ("it's a small set of users"). Removed `manage.sh` and `refactor/harness/` (it only characterised `manage.sh`); CI drops the harness job and `manage.sh` from shellcheck. `scripts/seed-chainstate.sh` stays as a wrapper around the seed job (owner); `scripts/postgres-initdb.sh` and `scripts/setup-bns.sh` stay (compose uses them). `VERBOSE` is gone from `sample.env`. Docs: README and usage (a "From `manage.sh`" command map), requirements, config; upgrade and `sbd migrate` now say how to stop an old-layout stack without it (`docker stop -t 1200 stacks-blockchain`, then `docker compose -p compose-files down`). **At PR time**, the ruleset drops the `manage.sh characterization harness` check (above).
