# Refactor Baseline: stacks-blockchain-docker

This folder documents everything the repository does today, so a later refactor can keep each behavior or change it on purpose.

| | |
|---|---|
| Snapshot | branch `chore/refactor` @ `d2e558c` (clean tree), 2026-10-16 |
| Scope | All 35 tracked files (`manage.sh`, 4 helper scripts, 11 compose files, 6 TOML samples, nginx conf, `sample.env`, docs, repo meta) |
| Rules for this pass | No existing file was modified. No commits or pushes. Everything new lives under `./refactor/` |

## How the findings were produced

1. **Static review.** Every tracked file was read line by line.
2. **Characterization runs.** `manage.sh` was run in a throwaway copy of the repo with `docker` and `uname` stubbed out. Every docker command it issued, its exit code and every file it touched were recorded. See [`harness/`](./harness/README.md). The 50 recorded scenarios live in [`harness/golden/`](./harness/golden/).
3. **Targeted experiments.** These were run on scratch copies only:
   - BSD vs GNU `sed` against the real `Config.toml.sample` files
   - `SCRIPTPATH` resolution in the helper scripts
   - `docker compose config`, used to resolve the merged compose model for each network/profile combination ([`reference/compose-resolved/`](./reference/compose-resolved/))

### Verification legend (used throughout)

| Tag | Meaning |
|---|---|
| **[V]** | Verified by running it (harness, sed experiment, or `docker compose config`) |
| **[S]** | Derived from reading the code; high confidence, not run |
| **[U]** | Unverified; needs a live Docker stack, network access, or confirmation against upstream images/APIs |
| **[O]** | Confirmed by the repo owner from operating experience (recorded in [15](./15-verification-log.md#owner-confirmations-2026-10-25)) |

## Document map

| # | Document | What it covers |
|---|---|---|
| — | [Handoff](./HANDOFF.md) | **Start here when resuming:** current state, rules, open follow-ups, next tests per host |
| 01 | [System overview](./01-system-overview.md) | Purpose, components, file inventory, runtime topology, host dependencies |
| 02 | [CLI reference](./02-cli-reference.md) | Every `manage.sh` option, action and standalone verb, parsing semantics, exit-code matrix |
| 03 | [manage.sh internals](./03-manage-sh-internals.md) | Execution phases, globals, function-by-function reference, call graph, shell mechanics relied upon |
| 04 | [Action workflows](./04-action-workflows.md) | Step-by-step flow of each action with the exact docker commands and file effects |
| 05 | [Compose topology](./05-compose-topology.md) | Every compose file and service, merge matrix, profiles, ports, volumes, env injection |
| 06 | [Configuration reference](./06-configuration-reference.md) | Every env var (source, consumer, default), TOML keys and which ones the script rewrites, chain IDs, couplings |
| 07 | [Auxiliary scripts](./07-auxiliary-scripts.md) | `seed-chainstate.sh`, `postgres-upgrade.sh`, `postgres-initdb.sh`, `setup-bns.sh` |
| 08 | [Filesystem & state](./08-filesystem-and-state.md) | Every generated file/dir, ownership, lifecycle, persistence per network, Docker-side state |
| 09 | [Defects & risks](./09-defects-and-risks.md) | 55 findings with severity, location, evidence and impact |
| 10 | [Documentation drift](./10-documentation-drift.md) | Where `README.md`/`docs/*` disagree with actual behavior |
| 11 | [Refactor guidance](./11-refactor-guidance.md) | Behavioral contract to preserve, open decisions, suggested target structure, test strategy |
| 12 | [Monitoring plan](./12-monitoring-plan.md) | **High priority, new feature.** Prometheus-based chain, container (CPU/mem/IO) and host monitoring as an opt-in `-f monitoring` flag |
| 13 | [TUI plan](./13-tui-plan.md) | **Optional, new feature.** Init wizard plus live dashboard as a client of the refactored core |
| 14 | [Cross-platform architecture](./14-platform-and-architecture.md) | **Full-refactor design.** Compose-first + optional Go CLI + containerized jobs for Linux/Unix/macOS/Windows; plain-`docker compose` operation (§2.1, §11); removing the API dependency; seed/data-path redesign |
| 15 | [Verification log](./15-verification-log.md) | How each previously unverified [U] item was resolved (2026-10-19): live Docker tests, upstream source at the pinned tags, Compose version tests |

## Upstream issues incorporated

| Issue | Topic | Where |
|---|---|---|
| [#153](https://github.com/stx-labs/stacks-blockchain-docker/issues/153) | Make this the default way to self-host node + API; hardcoded API dependency; built-in Grafana and health companions | D46, D47; [12](./12-monitoring-plan.md), [14](./14-platform-and-architecture.md) |
| [#152](https://github.com/stx-labs/stacks-blockchain-docker/issues/152) | seed-chainstate fails with "device busy" on mounted data dirs | D48; [14 §9.1](./14-platform-and-architecture.md) |
| [#132](https://github.com/stx-labs/stacks-blockchain-docker/issues/132) | Slow SHA-256 verification in seed | D49; [14 §9.2](./14-platform-and-architecture.md) |

**Owner direction (2026-10-16):** the repo must run on as many OSes as possible (Linux, Unix, macOS, Windows). Bash is not required, and `manage.sh` is to be fully refactored. The recommended direction is in [14](./14-platform-and-architecture.md); the sequencing is in [11 §D.1](./11-refactor-guidance.md).

**Owner direction (2026-10-19):** the controller CLI and the TUI are **optional**. A user must be able to run the containers with plain `docker compose` commands. This is contract item [11 §A.25](./11-refactor-guidance.md) and Q22; the design rules and the compose-only command reference are in [14 §2.1 and §11](./14-platform-and-architecture.md).
| — | [harness/](./harness/README.md) | Characterization harness and golden outputs |
| — | [live-tests/](./live-tests/README.md) | Reproducible live tests P1/P2 (compose), P5 (SHA-256 + cAdvisor), P6 (testnet smoke) |
| — | [reference/compose-resolved/](./reference/compose-resolved/) | Fully resolved compose models (`docker compose config`) per scenario |

## Headline findings (details in 09)

- **Data loss risk:** `scripts/seed-chainstate.sh` runs `rm -rf persistent-data/$NETWORK` *before* it checks whether services are running, and without asking for confirmation. **[S]**
- **macOS breakage:** on stock macOS, BSD `/usr/bin/sed` rejects the `s///m` flag that `update_configs` uses. `start`, `restart`, `import`, `export` and `bns` all abort unless GNU sed comes first in `PATH`. **[V]**
- **`.env` corruption on mocknet:** `-n mocknet -a start` permanently comments out `STACKS_EXPORT_EVENTS_FILE` in `.env` whenever `.env` has `NETWORK=mainnet` (the default). A mid-run `source .env` resets `NETWORK`, so the revert branch is skipped. **[V]**
- **Exit codes:** every usage/validation error exits **0**. **[V]**
- **Hangs:** any `confirm` prompt (the Apple Silicon warning, `reset`) loops forever when stdin is not a TTY. **[V]**
- **Secrets:** all of `.env`, including `SIGNER_PRIVATE_KEY` and the BTC RPC password, is injected into *every* container's environment. **[V]**
- **Flags are not persisted:** restarting without `-f signer` silently disables the signer observer in `Config.toml` and tears down the signer. **[V]**
- **The API is hard-wired in:** the node `depends_on` the API, and every config always sends events to it. There is no node-only or external-API mode (#153). **[V]**
- **Mounted data dirs break seed and reset:** `rm -rf` hits `Device or resource busy` on mount points, *after* the contents are already gone (#152).
- **Pinned versions are broken outside mainnet (2026-10-19) [V]:** the testnet config is rejected by stacks-core 4.0.1 (D50), mocknet panics at startup and was removed in 4.0.5 (D51), and the seed archive URLs for the pinned versions no longer exist (D52).
- **A down API stalls the node** by default (event dispatcher is blocking, retries forever) **[S]**, which makes D46 a liveness issue, not just a startup-order one.
- **Seed is fully serial** (#132): parallel SHA-256 over one stream isn't possible, but hashing while downloading, concurrent verification, staged streaming extraction and `pg_restore -j` all are.

## Decisions log

Owner decisions on the open questions, in the order they were settled. Details are in the linked docs.

| Date | # | Decision | Where |
|---|---|---|---|
| 2026-10-19 | Q1 | Non-zero exit codes on errors: usage/validation errors exit 2, operational failures exit 1, `status` keeps 0 = running / 1 = not running. Changelog note; the `manage.sh` shim maps the same way. | [11](./11-refactor-guidance.md) |
| 2026-10-19 | Q4 | **Mocknet is disabled; only mainnet and testnet are supported.** Network differences reach containers through per-service Compose `environment:` with interpolated values; nothing modifies `.env` at runtime. | [11](./11-refactor-guidance.md) |
| 2026-10-19 | Q5 | Per-service `environment:` allow-lists (no `env_file: .env`). The signer key reaches only the signer, via rendered `Signer.toml`. Optional ports (postgres, metrics, Prometheus/Grafana) bind 127.0.0.1 by default. Warn on, or refuse, default credentials when a service is exposed. | [11](./11-refactor-guidance.md) |
| 2026-10-19 | Q6 | Render from versioned templates on every `up` (the `config` job), merge `conf/<net>/*.overrides.toml` for user edits, escape values, then validate with `stacks-node check-config` / `stacks-signer check-config`. Generated files are not hand-edited. | [11](./11-refactor-guidance.md) |
| 2026-10-19 | Q23 | Resolved by Q4: mocknet is dropped (D51 closes with it). The CI smoke test is `check-config` on every rendered config plus a short testnet follower boot (container up, healthcheck reaches burnchain sync), with no full sync. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q7 | One stack per host, with fixed container names kept (contract §A.19). Status, reset, seed and job guards are network-aware and name the network in every message. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q8 | Remove the API 5.0.1 guard, `check_api` and the `private-testnet` branch. Keep BNS v1 as an optional `bns` job (useful only for a mainnet genesis sync), off by default and with a correct exit status (fixes D10–D12). | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q9 | A containerized `pg-upgrade` job for any network that detects the source version from `PG_VERSION`. It upgrades into a staging dir and swaps only after success. Run it with `docker compose run --rm pg-upgrade` or `sbd db upgrade`. It replaces `postgres-upgrade.sh`. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q10 | Phase-0 hotfix to the existing `seed-chainstate.sh`: check for running services before any delete, confirm (with `--yes` for automation), empty contents instead of removing mount points, use the archive's `-latest` names and read the real version from `.sha256`, and pass `curl --fail`. The containerized rewrite still follows in phase 3. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q12 | Prompts run interactively on a TTY. Without a TTY, a destructive action exits 2 with "re-run with --yes" (`CONFIRM=yes` for compose jobs). Warnings, such as arm64 or storage advice, never block. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q16 | **Bind mounts are the default on every OS** (Linux servers are the primary target). The storage location is **configurable**: a data root plus per-component paths, so a dedicated mount on a separate disk works by design. Named volumes are an opt-in for macOS/Windows, gated by a Docker Desktop disk-size preflight (mainnet ≈ 400 GB). The seed job gains a **load-local-archive** mode (tar.xz/zst/gz, streamed into staging on the target filesystem, verified, then swapped in) for occasional multi-hundred-GB loads. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q20 | Settled together with Q16. Per-component data paths are configurable (e.g. `STACKS_DATA_DIR`, `POSTGRES_DATA_DIR`, `SIGNER_DATA_DIR` under a configurable data root). Destructive operations empty a directory's contents and never remove a mount point (#152). | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q17 | Shell out to `docker compose` with argv arrays, the exact commands `sbd plan` prints. The Docker Engine API is used only for read-only status, stats and logs. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q18 | Tier 1 (CI-tested): Linux amd64/arm64, which is primary and includes the live smoke test; macOS arm64; Windows 11 + Docker Desktop/WSL2 (build and dry-run tests). Tier 2 (documented): other Unix as a client of a remote Docker host. Podman is best effort. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q19 | `COMPOSE_PROFILES=node,api` by default. Also supported: node-only (no API observer rendered at all) and an external API/DB with configurable endpoints. The docs state that any configured API is a liveness dependency of the node, because event delivery blocks by default. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q21 | The full spec in 14 §9: hash-while-download with both archives fetched concurrently; resumable ranged downloads; streamed zstd/xz extraction into staging; `pg_restore -j`; mount-safe swap; load-local-archive mode; progress output. Sources: the Hiro archive or a local file. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q24 | **Refuse on mismatch.** Seed reads the archive's real versions from the `.sha256` line and aborts (exit 1) unless they match the pinned `STACKS_BLOCKCHAIN_VERSION` / `STACKS_BLOCKCHAIN_API_VERSION` / `POSTGRES_VERSION`. The error names both sets of versions so the user can update `.env`. This applies to the phase-0 hotfix (Q10) too. Because the archive keeps only the current `-latest` set, seeding effectively requires pins that match the archive. **Correction (2026-10-26):** the archive keeps a **rolling 30-day window of dated, versioned files**, not only `-latest` ([09 D52](./09-defects-and-risks.md), [15](./15-verification-log.md)). "Refuse on mismatch" therefore means: resolve the pinned versions to the newest dated file within the window, and refuse only if none exists. The per-network and older-version follow-ups are **reopened** with this in mind. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | M1 | Prometheus, with retention set by `PROMETHEUS_RETENTION` (default 15d). | [12](./12-monitoring-plan.md) |
| 2026-10-25 | M2 | Grafana ships inside the `monitoring` profile with provisioned dashboards (Stacks node, Containers, Host), bound to 127.0.0.1 with a required admin password. `MONITORING_GRAFANA=false` disables it. | [12](./12-monitoring-plan.md) |
| 2026-10-25 | M3 | Prometheus scrapes node :9153, API :9153 and signer :9154 **directly**. API lag, tip stall and event age are recording/alert rules. **No json-exporter and no helper container in the first release.** `stacks-status` is a possible later add-on for sync % against the Bitcoin tip, the exact event backlog and a `/healthz` URL. | [12](./12-monitoring-plan.md) |
| 2026-10-25 | M4 | Opt-in. Healthchecks are always on; users add `monitoring` to `COMPOSE_PROFILES`. Revisit after the #153 stress test measures overhead. | [12](./12-monitoring-plan.md) |
| 2026-10-25 | M6 | Nothing to decide: the API already serves `/metrics` on :9153 whenever `NODE_ENV=production` [V]. Add it as a scrape target. | [12](./12-monitoring-plan.md) |
| 2026-10-25 | Q13 | Settled via M1–M4 in doc 12: Prometheus; Grafana inside `monitoring` (can be disabled); direct scrape plus rules, no json-exporter; `stacks-status` deferred. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | M5 | Later phase. The first release ships Prometheus alert rules, visible in Prometheus and Grafana. Notification channels (Alertmanager or Grafana alerting) come after the stress test has tuned the thresholds. | [12](./12-monitoring-plan.md) |
| 2026-10-25 | T1 | Same repo, same binary: part of the `sbd` CLI (`sbd init`, `sbd ui`), released and versioned with the compose files it drives. | [13](./13-tui-plan.md) |
| 2026-10-25 | T2 | GitHub Releases with signed binaries and checksums for linux/darwin/windows × amd64/arm64, plus a ghcr.io image (also used by the `config` renderer and for no-install runs). Package managers (Homebrew, winget) come later. The language is decided under Q25. | [13](./13-tui-plan.md) |
| 2026-10-25 | T3 | `sbd init` and `sbd ui` only. The `manage.sh` shim gains no new commands (it is removed in phase 7). | [13](./13-tui-plan.md) |
| 2026-10-25 | T4 | **Actions from the start.** The first dashboard release includes start/stop/restart/pull/logs export alongside status. Each action runs the same core/compose command as the CLI. Destructive actions (stop, reset, import) need a typed confirmation, and a long stop shows progress. | [13](./13-tui-plan.md) |
| 2026-10-25 | T5 | Yes, as an optional wizard step: seed from the Hiro archive, seed from a local archive, or sync from genesis. The wizard runs the containerized seed job (no host root) with progress, after showing the size (~400 GB mainnet) and the Q24 pin/archive version check. | [13](./13-tui-plan.md) |
| 2026-10-25 | Q14 | Settled via T1–T5 in doc 13: same repo and binary (`sbd init`, `sbd ui`), released on GitHub Releases plus ghcr, dashboard actions from the first release, optional seed step in the wizard. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q15 | **Go**, re-confirmed under Q25. It is used for the `sbd` CLI/TUI (Bubble Tea), the `config` renderer image and the seed/pg-upgrade job images. | [11](./11-refactor-guidance.md) |
| 2026-10-25 | Q25 | Go over Rust. Reasons: `compose-go` and the official Docker SDK keep the CLI identical to plain `docker compose` (R4); cross-compiling every Tier-1 target is trivial; easier contributor ramp-up. Rust was judged viable; the comparison is in 14 §3. | [11](./11-refactor-guidance.md) |
| 2026-10-26 | Q26 | D55 fix: **(a) a smaller API observer `timeout_ms`** (60 000 ms on both networks, so a 180 s backoff cap; overridable via `API_OBSERVER_TIMEOUT_MS`), and document it. A node restart was rejected because an API that is up but failing (e.g. lost its DB connection) triggers no container event. Validate the API's worst-case `/new_block` time in the phase 5 stress test | [11](./11-refactor-guidance.md), [09](./09-defects-and-risks.md) |
| 2026-10-26 | Q27 | Network config (bootstrap node, epochs, balances, `pox_5_*`) comes from upstream stacks-core `main` `sample/conf/` (e.g. `testnet-follower-conf.toml`); repo deployment settings and user overrides are layered on top. Recommended: vendor with the source SHA plus a CI sync PR; CI runs `check-config` and a short boot against the pinned image | [11](./11-refactor-guidance.md), [09](./09-defects-and-risks.md) |
| 2026-10-26 | Q24 (re-settled) | **The snapshot date matters most.** Seed downloads a chainstate + pg-dump **pair with the same date**: the newest date where both exist and the versions are compatible (same major, ≤ pins; dump pg major ≤ server). It never mixes dates and never downloads `-latest` (a per-archive symlink). Older versions get a warning (migrated on start). It refuses a different major, newer versions or no pair, with exit 1, the reason and the latest snapshot's versions. No per-network pins | [11](./11-refactor-guidance.md), [09 D52](./09-defects-and-risks.md) |
| 2026-10-26 | Q24 (newer archives) | **Refuse archives newer than the pins** (owner confirmed the safety rule). Seed still uses the newest same-date pair ≤ pins, and **warns** when newer snapshots exist (the user may have older pins), naming the newest snapshot and suggesting a pin bump. Exit 1 only when no compatible pair exists |
