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
- **Decisions:** all settled, Q1–Q27, M1–M6 and T1–T5. See the README [Decisions log](./README.md#decisions-log) and [11 §B](./11-refactor-guidance.md). Deferred TODOs are in [11 §F](./11-refactor-guidance.md).
- **Verification:** [15](./15-verification-log.md) records every check.
- **Harness:** `refactor/harness/run.sh --check` should report 50/50 PASS against `d2e558c` behavior. On Linux, GNU sed is native.

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
| **TODO-1:** non-root node | Feasible: uid 1000 with `working_dir = "/data"` works. With `/root/...` it panics. Deferred to implementation | 11 §F |
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

## Resuming on another machine
1. Get `refactor/` there. The owner can commit and push the branch, or copy just the folder: `tar czf refactor.tgz refactor/`, then `scp` it and untar it in the clone (branch `chore/refactor` @ `d2e558c`).
2. Optionally copy the memory files (`MEMORY.md`, `refactor-plan-priorities.md`) into the new machine's `~/.claude/projects/<path-slug>/memory/`.
3. Start the session with: *"Read refactor/HANDOFF.md and follow its rules exactly (edit only refactor/, no commits, scratch `sbd-*` projects only, leave my sample.env edit alone). This is the Linux x64 host. Run P1, P5 and P6 from refactor/live-tests/, record the results in refactor/15-verification-log.md next to the macOS results, update HANDOFF.md, and clean up all sbd-* containers and volumes. Ask me before the optional tests."*
