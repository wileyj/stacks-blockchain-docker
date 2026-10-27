# 15 — Verification Log (2026-10-19)

This log records how each **[U]** item from docs 06–14 was resolved. Tags follow the [README](./README.md) legend: **[V]** means it was run, **[S]** means it was read in upstream source at the pinned tag.

**Environment:**
- Docker Desktop 28.3.2 on macOS, Apple M4 Pro (arm64), cgroup v2.
- Compose v2.39.1, plus official v2.20.2, v2.23.3, v2.24.7, v2.26.1, v2.27.1 and v2.27.2 binaries downloaded with verified checksums.
- **Linux x64 host (P1/P5/P6, 2026-10-26):** Debian, kernel 7.0.10 amd64, Intel Core i9-13900H (20 threads, `sha_ni`), 62 GiB RAM, Samsung 9100 PRO NVMe. Docker Engine 29.5.3 (overlayfs, cgroup v2), Compose v5.1.4. Heavy scratch data went to the 3 TB `/stacks/temp` LV (bind-backed named volumes); Docker's root is on the 92 GB root LV.

**Images tested:**
- stacks-core and stacks-signer 4.0.1 (also stacks-core 4.0.5)
- stacks-blockchain-api 9.0.1
- postgres:17-alpine
- nginx:alpine

**Upstream sources read:**
- `stacks-network/stacks-core` @ `4.0.1`
- `hirosystems/stacks-blockchain-api` @ `v9.0.1`
- docker/compose and compose-spec/compose-go release notes

**Constraints:**
- Every experiment ran in scratch Compose projects (`sbd-verify*`, `sbd-mocknet`) with named volumes. Nothing touched the repo's `.env`, `conf/` or `persistent-data/`.
- All scratch containers, volumes and networks were removed afterwards.

## Summary

| # | Question | Result | Tag | Changes to the plan |
|---|---|---|---|---|
| 1 | Does `up -d` re-run an exited `config` dependency? | **Yes, on every `up -d`**, including when nothing changed. It also re-runs after `down`/`up` and with `--force-recreate config` | [V] | Config edits take effect on the next `up`. Dropped the "force-recreate" caveat in 14 §11 |
| 2 | Does `docker compose run <svc>` work when the service's profile is inactive? | Yes (2.20.2 through 2.39) | [V] | Jobs launched by hand work as written in 14 §11 |
| 2b | `--profile X` vs `COMPOSE_PROFILES` | The command-line `--profile` **replaces** the `.env` set when selecting services, but `up -d --profile seed` did **not** stop running services from other profiles | [V] | Non-destructive, as expected |
| 2c | What happens to a service removed from `COMPOSE_PROFILES`? | **It keeps running.** Neither `up -d` nor `up -d --remove-orphans` stops it, and **plain `down` leaves it running**. `docker compose --profile '*' down` stops everything | [V] | **New gotcha.** The documented stop becomes `docker compose --profile '*' down`; `sbd stop` does the same. Added to 14 §2.1 and §11 |
| 3 | Do the images have `curl`/`wget` for healthchecks? | stacks-core and stacks-signer (Debian 13): **neither**, but bash is present. API (Debian 12): neither, but `node` is present. postgres: `wget`, `nc`, `pg_isready`. nginx: `curl` and `wget` | [V] | Healthchecks need no companion. Node and signer use bash `/dev/tcp`, the API uses `node -e fetch(...)`, postgres uses `pg_isready`. Node and API checks both ran successfully |
| 4 | Are the images multi-arch? | Every pinned image and every monitoring candidate has linux/amd64 + linux/arm64 (prometheus, cadvisor, node-exporter, postgres-exporter, grafana, json-exporter) | [V] | Q18 tier-1 arm64 holds |
| 5a | Does the node stall when the API is down? | **Yes, by default.** `event_dispatcher_blocking` defaults to `true`: "an event observer that is slow to respond might stall the node". Each payload is retried forever with exponential backoff (capped at 3× `timeout_ms`) unless the observer has `disable_retries = true`. Pending payloads are persisted in `<working_dir>/event_observers.sqlite` and redelivered at startup **before normal operation resumes**. Payloads for an observer that is no longer configured are dropped at startup | [S] (`stackslib/src/config/mod.rs` `event_dispatcher_blocking`; `stacks-node/src/event_dispatcher/worker.rs` `make_http_request`; `event_dispatcher.rs` `process_pending_payloads`) | Confirms that D46 is **High**. Node-only mode **must** omit the API observer, and removing an observer is safe. An external API is a liveness dependency of the node. Options to document: `event_dispatcher_blocking = false` + `event_dispatcher_queue_size`, and `disable_retries` (the API then loses events, so not for the API) |
| 5b | Does mocknet work on the pinned node? | **No.** The repo's `conf/mocknet/Config.toml.sample` passes `check-config` on 4.0.1, but the node **panics at startup**: "FATAL: Epoch 3.0 must not start at a reward cycle boundary". The image's own `stacks-node mocknet` panics the same way. **4.0.5 removed the `mocknet` subcommand** and rejects the sample (`unknown field commit_anchor_block_within`) | [V] | **New defect D51 (High)**: mocknet is broken today, and it was going to be the CI smoke test (11 §E, 14 §7). Q-new: replace mocknet with a different local devnet, or drop it |
| 5c | Are the testnet and mainnet configs valid? | `stacks-node check-config`: mainnet **valid** on 4.0.1 and 4.0.5. **Testnet invalid on 4.0.1 and 4.0.5**: `unknown field always_use_affirmation_maps` (`conf/testnet/Config.toml.sample:6`). Signer samples for mainnet and testnet are valid (`stacks-signer check-config`) | [V] | **New defect D50 (High)**: testnet can't start with the pinned version. `check-config` is a ready-made validator for the `config` renderer job and for CI |
| 5d | Node Prometheus metric names | `stacks_node_stacks_tip_height`, `stacks_node_burn_block_height`, `stacks_node_neighbors_inbound/outbound`, `stacks_node_mempool_outstanding_txs`, `stacks_node_stx_blocks_processed_total`, `stacks_node_rpc_call_latencies_histogram`, `stacks_node_last_block_*`, `stacks_node_errors_emitted_total`, `stacks_node_warning_emitted_total` and others. **No event-dispatcher queue or delivery metric exists** | [S] (`stackslib/src/monitoring/prometheus.rs`) | Doc 12 updated. Observer backlog has to come from `stacks-status` (row count in `event_observers.sqlite`, or log parsing) |
| 5e | Signer metric names | `stacks_signer_block_proposals_received`, `stacks_signer_block_responses_sent`, `stacks_signer_block_validation_latencies_histogram`, `stacks_signer_current_reward_cycle`, `stacks_signer_stacks_node_height`, `stacks_signer_stx_balance` and others | [S] (`stacks-signer/src/monitoring/prometheus.rs`) | Doc 12 updated |
| 5f | When do node RPC and metrics start answering? | **Not during the initial Bitcoin header sync.** 20443 and 9153 refused connections for more than 5 minutes on a fresh mainnet node while headers were syncing (95% after about 2 minutes, then "Connection broken; retrying in 60 sec" against `bitcoin.mainnet.stacks.org`) | [V] | The node healthcheck needs a long `start_period`. Nothing should gate on `node: service_healthy` at first boot. The seed job's running-check can't rely on an RPC probe alone |
| 6 | Is there a lock file in the data dirs (for the seed job's running-guard)? | Node: **none**, only SQLite `-wal`/`-shm` files, which outlive crashes, plus `event_observers.sqlite`. Postgres: `postmaster.pid` (standard; stale after a crash) | [V] / [S] | The job guard combines (a) a TCP probe of postgres:5432 and the node's P2P/RPC ports on the compose network, (b) `postmaster.pid` plus a probe, and (c) the documented `down` before every job. The CLI also checks container state |
| 7 | cAdvisor on cgroup v2 / Docker Desktop | Works with the documented mounts. Series carry the `com.docker.compose.service` / `project` labels (via `--whitelisted_container_labels`). On cgroup v2: `container_cpu_usage_seconds_total`, `container_memory_working_set_bytes`, `container_fs_reads_bytes_total`/`writes`, `container_blkio_device_usage_total`, `container_network_*`, `container_pressure_*` (PSI). Tuned flags gave **about 14 MiB RAM and under 1% CPU**, with about 440 series for 3 containers | [V] (indicative) | Doc 12 dashboards target these names. Overhead numbers to be confirmed in the stress test |
| 8 | Is the archive dump custom-format? | **Yes**: the first bytes are `PGDMP` (custom format), so `pg_restore -j` applies. The chainstate starts with the zstd magic `28 b5 2f fd`. Current sizes: **dump 105 GB, chainstate 277 GB** (mainnet, 2026-10-19) | [V] | 14 §9.2 confirmed |
| 8b | Do the seed archive URLs exist for the pinned versions? | **No.** `…-pg-17-9.0.1-latest.dump` and `mainnet-stacks-blockchain-4.0.1-latest.tar.zst` return `NoSuchKey`. The archive now publishes **`stacks-blockchain-api-pg-17-latest.dump`** and **`<net>-stacks-blockchain-latest.tar.zst`**, plus dated files (`…-17-9.6.0-20261019.dump`, `…-4.0.4-20261019.tar.zst`). The `.sha256` names the real version. **HEAD returns 403** (pre-signed GET only); the size is available from `Content-Range` on a 1-byte ranged GET | [V] | **New defect D52 (High)**: seed with the default pins can't download. The rewrite should read the `.sha256` to learn the archive version and check it against the pinned images. New Q: should the API/node versions follow what the archive publishes? D18 "size via HEAD" always yields 0 (D53) |
| 9 | SHA-256 throughput (2 GiB, warm cache) | macOS: `shasum` 566 MB/s, Homebrew `sha256sum` 540 MB/s, OpenSSL 2.43 GB/s, Go `crypto/sha256` **2.67 GB/s**. Alpine container (arm64, SHA2 extensions): busybox `sha256sum` **338 MB/s**, coreutils `sha256sum` 2.94 GB/s, OpenSSL 2.77 GB/s | [V] | Implementation matters up to 8×, and busybox is the slow case. At 2.7 GB/s, hashing 382 GB costs about 2.4 min of CPU, so the cost of #132 is mostly **re-reading 382 GB from disk**. Hash-while-download removes that entirely. **Linux x64 (P5):** coreutils 1.54 GB/s, OpenSSL 1.46 GB/s, Go 1.53 GB/s, perl `shasum` 343 MB/s; Alpine busybox **184 MB/s**, Alpine coreutils 868 MB/s. On that host's NVMe a cold read runs at 4.7 GB/s, so there **hashing (CPU) is the bottleneck, not the disk** (see P5 results) |
| 10 | `STACKS_EVENT_OBSERVER` env var | Exists. It adds **one** observer with `events_keys = ["*"]`, a **fixed `timeout_ms` of 1000** and retries on. It is **in addition to** the TOML observers (stored in a set; the 1000 ms timeout differs from the TOML API observer's 60000, so both are kept) | [S] (`stackslib/src/config/mod.rs:1019`) | Usable for "API profile attaches itself without rendering", but the 1 s timeout is much tighter than the 60 s used today. **D54 (Low)**: uncommenting `# STACKS_EVENT_OBSERVER` in `sample.env` while the TOML observer is present makes the node send every event to the API **twice** |
| 11a | API Prometheus endpoint | There is no env var: the API serves `/metrics` on a **fixed port 9153** whenever `NODE_ENV=production`, which `sample.env` sets. Live metrics include `stacks_block_height`, `burn_block_height`, `mempool_tx_count`, `stacks_event_ingestion_timers`, `stacks_last_event_timestamps`, `http_request_duration_seconds`, `chain_tip_cache_*` and websocket/socket.io series | [V] + [S] (`src/index.ts:162-183`) | **M6 is already on by default**; just scrape `stacks-blockchain-api:9153`. API lag can be computed natively in Prometheus |
| 11b | Is `BNS_IMPORT_DIR` still used in API 9.x? | **Yes.** `handleBnsImport` runs when block height 1 arrives (genesis sync only), if `BNS_IMPORT_DIR` is set and the import hasn't been recorded | [S] (`src/event-stream/event-server.ts:875`, `src/import-v1/index.ts:554`) | **D45 revised**: BNS v1 data still matters for a genesis sync with full BNS history. It is irrelevant after a seed (already imported) or an event replay. Q8 now needs an owner call |
| 11c | `GIT_TAG`, `V2_POX_MIN_AMOUNT_USTX` | **Not referenced** anywhere in API 9.0.1 | [S] | Remove from `sample.env` |
| 11d | Does `import-events --wipe-db` wipe before validating the file? | **No.** It throws `File does not exist` before touching the DB. Live test: 52 tables before and after | [V] + [S] (`src/event-replay/event-replay.ts:84`) | **D09 lowered**: no data-loss path. The remaining harm is needless downtime, because `manage.sh` has already stopped the services |
| 12 | Minimum Compose version | `configs.content` is **rejected by 2.20.2** ("Additional property content is not allowed") and works on 2.23.3. `depends_on.required` arrived in 2.20.2. Explicit `run` of a profiled service has worked since 2.22.0. `COMPOSE_PROFILES` from `.env` worked on every version tested, though 2.27.2's notes mention a fix in that area. `--profile '*'` works on 2.20.2 and later | [V] + release notes | **Minimum 2.24.0** (2.24.0 also fixed config mount targets); **recommend ≥ 2.27.2**. CI tests the minimum (2.24.7) |
| 13 | D45 legacy features | See 11b. The API 5.0.1 guard is obsolete; BNS v1 is still functional | [S] | Q8 |

## Owner confirmations (2026-10-25)

| Topic | Owner statement **[O]** | Effect on the plan |
|---|---|---|
| Default Bitcoin node | **Stays `bitcoin.mainnet.stacks.org`.** The owner has a local Bitcoin node for testing, which needs an RPC user and password | No change to the defaults. Live tests in Part 2 below use the local node through the existing `BTC_HOST` / `BTC_RPC_USER` / `BTC_RPC_PASS` / `BTC_RPC_PORT` variables (`TBTC_*` for testnet), set in a scratch `.env` only. The connection drops seen against the default host (5f) were not treated as a defect |
| Node behavior with the API down | **Blocked indefinitely.** The node keeps retrying with exponential backoff until the API is back | Matches 5a. Backoff cap, **settled by P2 [V]**: 3 × the observer's `timeout_ms`, so **900 s on mainnet** (`300_000`) and 180 s on testnet (`60_000`). The owner's "up to 60 s" and the earlier "180 s" figure were both wrong for the mainnet sample. Either way it is a liveness dependency (D46) |
| Time from first boot to RPC/metrics | **Several seconds to several minutes, depending on where the node is in its sync.** A genesis sync takes several minutes to emit its first metrics | The node healthcheck `start_period` is sized for the genesis case (≥ 10 min). Nothing gates on `node: service_healthy` at first boot. Monitoring alerts get the same grace period |
| Duplicate event delivery (D54) | Not sure whether the node sends duplicates, but **the API should not write an event it has already processed** | D54 lowered to **Info**: no data corruption expected. The node still makes two blocking deliveries per event, so the advice stands: don't uncomment `STACKS_EVENT_OBSERVER` alongside the TOML observer. Optionally check in Part 2 |
| Older archive into newer pins (Q24), 2026-10-26 | **Restoring an older archive into newer versions within the same major is a known-good path.** No restore test is needed (the testnet 4.0.1/9.0.2 → 4.0.4/9.7.0 and local mainnet 9.6.0 → 9.7.0 restore checks were dropped) | Q24's "older within a major → warn, the node/API migrate on first start" rule stands on the owner's confirmation **[O]**. The CI boot test still covers the pinned images |
| Test hosts | **macOS arm64 (this machine) is the main test host, plus a Linux x64 machine** | Linux x64 adds the x86 SHA-256 and native-Linux cAdvisor measurements. Windows stays CI-only (build and dry-run goldens) unless a Windows host becomes available |

**Archive inventory, 2026-10-26 [V]** (server-rendered listing at `https://archive.hiro.so/<net>/<component>/`; corrects the earlier "only `-latest`" claim):
- **Retention:** a **rolling 30-day window** of dated, versioned files (2026-09-27 to 2026-10-26), each with a `.sha256`.
- **Mainnet:**
  - chainstate: `mainnet-stacks-blockchain-4.0.4-<YYYYMMDD>.tar.zst` × 30, plus `mainnet-stacks-blockchain-latest.tar.zst`. There is **no** `mainnet-stacks-blockchain-4.0.4-latest`.
  - API: `stacks-blockchain-api-pg-17-<ver>-<YYYYMMDD>.dump` for 9.3.0 (11 days), 9.5.0 (5), 9.6.0 (9) and 9.7.0 (3), plus `stacks-blockchain-api-pg-17-latest.dump`. There is **no** `…-9.7.0-latest`.
- **Testnet:**
  - chainstate: `testnet-stacks-blockchain-4.0.1-<date>` × 30, plus **both** `testnet-stacks-blockchain-4.0.1-latest.tar.zst` and `testnet-stacks-blockchain-latest.tar.zst`.
  - API: `…-pg-17-9.0.2-<date>` × 30, plus **both** `…-pg-17-9.0.2-latest.dump` and `…-pg-17-latest.dump`.
- So the script's `<ver>-latest` naming exists on testnet only. On mainnet, a pinned version is reachable **only by dated name**, and only while it is inside the 30-day window. The 404s for 4.0.1/9.0.1 came from both causes: no mainnet `<ver>-latest` alias, and versions older than the window.
- **No index file:** there is no JSON/XML index. Discovery is either the HTML listing or probing dated names backwards from today with 1-byte ranged GETs (D53).

### Archive vs pins, re-checked 2026-10-25 [V]

`sample.env` pins were changed in the working tree to core/signer 4.0.4 and API 9.7.0. `POSTGRES_VERSION` stays **17**; an earlier note here said 18, which was wrong (corrected 2026-10-26 from `git diff`). So the mainnet pins now match the mainnet archive exactly. The archive's `-latest` files on 2026-10-25:

| Network | API dump | Chainstate |
|---|---|---|
| mainnet | `…-pg-17-9.7.0-20261025.dump` (**no pg18 dump**) | `mainnet-stacks-blockchain-4.0.4-20261025.tar.zst` |
| testnet | `…-pg-17-9.0.2-20261025.dump` (no pg18 dump) | `testnet-stacks-blockchain-4.0.1-20261025.tar.zst` |

Consequences under Q24 ("refuse on mismatch"):
1. If a user sets `POSTGRES_VERSION=18`, there is no dump to fetch on either network. A pg17 custom-format dump *can* be restored into a pg18 server (`pg_restore` reads older archive versions), so the version check could allow "dump major ≤ server major" **[S]**, to be tested.
2. Mainnet and testnet archives are on **different versions** (API 9.7.0 vs 9.0.2, core 4.0.4 vs 4.0.1). One global pin set can't match both, so pins (or at least the seed check) would need to be per network. These are open follow-ups to Q24.

### Part 2 test plan

**Local test Bitcoin node [O]:** `192.168.8.252`, P2P `8333`, RPC `8332`. It is on the LAN of both test hosts. **Its RPC requires auth [O, 2026-10-26]**: unauthenticated calls and the sample `stacks`/`foundation` pair both get `401 Unauthorized` **[V]**. This corrects the 2026-10-25 note that no credentials were needed. Both ports were reachable from the macOS host on 2026-10-25 **[V]**. Off the LAN, the Mac reaches it through an SSH tunnel via the `ztnuc` host: `ssh -fN -L 127.0.0.1:28333:192.168.8.252:8333 -L 127.0.0.1:28332:192.168.8.252:8332 ztnuc`. Containers then use `host.docker.internal:28333`, which was reachable from a container **[V]**. Tests set `BTC_HOST=192.168.8.252` (`peer_host`) in a scratch `.env`/config only. The repo default stays `bitcoin.mainnet.stacks.org`. P1 confirms whether a follower works with the `username`/`password` keys omitted.

| # | Test | Host | Needs |
|---|---|---|---|
| P1 | Node past header sync: scrape `:9153`, confirm the `stacks_node_*` names, time to first metrics; follower with bitcoind RPC failing (401), using P2P only | macOS arm64 (then Linux x64) | Local BTC node `192.168.8.252` — **done on both hosts 2026-10-26** |
| P2 | Stop the API on a node that is processing blocks: observe the stall, measure the actual backoff cap, time the recovery after the API restarts | macOS arm64 | P1 — **done 2026-10-26 on both hosts** |
| P3 | Optional: `STACKS_EVENT_OBSERVER` plus the TOML observer against a counting listener (D54) | macOS arm64 | P1 — **done on Linux x64 2026-10-26** (counted at the API's ingestion metric) |
| P4 | Signer metric names with a testnet signer | macOS arm64 | Testnet node and a signer key — **done 2026-10-26 from the owner's live signer** (the local attempt was blocked by the testnet BTC peer) |
| P5 | SHA-256 throughput (coreutils, OpenSSL, Go, busybox) and cAdvisor on native Linux | Linux x64 | — — **done 2026-10-26** |
| P6 | Phase-0 smoke test (testnet boot) on both hosts | both | Fixed testnet config (D50) — **macOS and Linux x64 done 2026-10-26** (`live-tests/p6-testnet-smoke.sh`) |

### P1 results — macOS arm64, 2026-10-26 [V]

Setup:
- Scratch project `sbd-p1`, named volumes.
- `stacks-core:4.0.4`, `stacks-blockchain-api:9.7.0` (`NODE_ENV=production`), `postgres:17-alpine`, all synced from genesis.
- BTC peer: the local test node, reached through an SSH tunnel via `ztnuc` (`peer_host = "host.docker.internal"`, `peer_port = 28333`, `rpc_port = 28332`).
- `[burnchain]` has **no `username`/`password`**, and the test node's RPC rejects anonymous calls (401).

| Check | Result |
|---|---|
| `check-config` with `username`/`password` omitted | Passes |
| Follower with failing bitcoind RPC | **Works.** No bitcoind RPC calls or errors appeared in the logs. Headers and burn blocks come over P2P (`stacks_node_btc_blocks_received_total`). A follower needs **no** RPC credentials. Only miners/signers that submit BTC transactions would. |
| Bitcoin header sync (973 261 headers) | About 60 s from container start with a LAN peer, versus more than 5 min, with dropped connections, against `bitcoin.mainnet.stacks.org` (5f) |
| Time to first RPC (`/v2/info`) and `/metrics` | **2 min 05 s** after `up`: header sync, then MARF genesis (335 331 insertions), then p2p/RPC/metrics bind |
| Time to first Stacks block | First outbound peers appeared within about 30 s of RPC coming up; tip ≈ 100 blocks after another 30 s, then about 100 blocks per 30 s in the genesis era |
| API 9.7.0 | `/extended` → `status: ready` (checked after the node's RPC was up: **corrected 2026-10-26**, the API serves HTTP only once the node's RPC answers; see "Phase 1 compose"); ingested along with the node (API height = node tip = 342 at check time). `/metrics` on 9153 serves 61 families |

Node metric families seen **[V]**:
- Present at RPC-up (11 families): `stacks_node_stacks_tip_height`, `stacks_node_burn_block_height`, `stacks_node_neighbors_inbound`, `stacks_node_neighbors_outbound`, `stacks_node_btc_blocks_received_total`, `stacks_node_bandwidth_inbound`, `stacks_node_bandwidth_outbound`, `stacks_node_rpc_bandwidth_inbound`, `stacks_node_rpc_bandwidth_outbound`, `stacks_node_rpc_requests_total`, `stacks_node_rpc_call_latencies_histogram`.
- Added once blocks were being processed (21 in total): `stacks_node_stx_blocks_processed_total`, `stacks_node_transactions_received_total`, `stacks_node_message_count`, `stacks_node_last_block_{read_count,read_length,runtime,transaction_count,write_count,write_length}`, `stacks_contract_calls_processed`.
- **Families appear lazily.** For example, `stacks_node_mempool_outstanding_txs` is not exported until its code path runs. Dashboards and alert rules must therefore treat a missing series as "not yet" rather than as an error (`absent()` alerts only on the core gauges).
- `stacks_node_burn_block_height` stayed at **666051**, the burn height of the Stacks genesis, while the Stacks tip advanced. It tracks the processed sortition height, not the Bitcoin header tip, so it can't serve as a "BTC headers synced" signal.

Consequences:
- **Q16/Q21 (config renderer):** `BTC_RPC_USER`/`BTC_RPC_PASS` should be optional for followers. Render the keys only when set, or when a signer/miner profile needs them.
- **Healthcheck `start_period` ≥ 10 min** (owner, [O]) is comfortable with a fast peer. Keep it for slow or default peers.
- **doc 12 (monitoring):** the "tip not advancing" alert must be gated on `stacks_node_neighbors_outbound > 0` and the boot phase, since the first 2–3 min have no series at all.
- **Linux x64 repeat:** done, see the next section.

### P1 results — Linux x64, 2026-10-26 [V]

Setup:
- `refactor/live-tests/p1-compose.yaml` as committed: project `sbd-p1`, `stacks-core:4.0.4`, `stacks-blockchain-api:9.7.0` and `postgres:17-alpine` (amd64), all from genesis.
- BTC peer: the LAN node `192.168.8.252:8333/8332` directly, with no tunnel and **no RPC credentials**.
- The only addition was a scratch override that backs the two named volumes with bind dirs under `/stacks/temp/sbd-p1/`, so the data lands on the 3 TB LV.
- Timings come from a script that polled `/metrics` every 5 s and sampled every 30 s for 15 min, plus the node's own log timestamps.

| Check | Linux x64 | macOS arm64 |
|---|---|---|
| BTC RPC with credentials omitted | **No bitcoind RPC calls or errors** (0 `bitcoind`/`Unauthorized` lines), 0 `ERRO`, 0 panics, 0 restarts | Same |
| Bitcoin header sync (973 282 headers, LAN peer) | **1 min 45 s** (20:00:02 → 20:01:47) | About 60 s |
| Silent phase after `New canonical Bitcoin chain found`, before `Syncing Bitcoin blocks` | **1 min 46 s**, with no log output except the 1 s `boot_nakamoto` poll line | Not separately measured |
| MARF genesis (335 331 insertions) | About 3 s | — |
| From genesis to the Prometheus bind | 38 s | — |
| **Time to first `/metrics` and `/v2/info`** | **4 min 16 s** after `up` (both in the same 5 s poll) | 2 min 05 s |
| First outbound peers / first Stacks block | Between +5:46 and +6:17: `neighbors_outbound` 0 → 34 and tip 0 → 27, about **1.5–2 min after RPC-up** | About 30 s after RPC-up |
| Genesis-era block rate | About **81 blocks per 30 s** (tip 27 → 1 321 between +6:17 and +14:17), with 37–38 outbound peers | About 100 per 30 s |
| API 9.7.0 | `/extended` was `status: ready` throughout and stayed level with the node (API height = node tip at every sample; 1 439 at the end). `/metrics` on 9153: **61 families** | Same (61) |
| Disk after 15 min | Chainstate 664 MB, Postgres 2.2 GB | — |

Node metric families **[V]**: **identical to macOS**. The same 11 families were present at RPC-up, and the same 10 were added once blocks were processing (21 in total; see the macOS list above). `stacks_node_burn_block_height` again stayed at **666051** throughout.

Log noise seen in genesis sync, all `WARN` or `INFO`, none `ERRO`:
- about 6 000 `Invalid block commit: missed target` lines (historic block-commit validation);
- `REJECTED(<height>) leader` lines;
- `Could not process pushed Nakamoto block` (peers push current blocks the node can't use yet);
- `failed to learn public IP`.

Dashboards and log-based alerts must not treat any of these as errors during the initial sync.

Consequences:
- **Time to RPC varies by about 2× across hosts with the same LAN peer**, here 4 min 16 s versus 2 min 05 s. Most of the gap is the header download (1:45 vs about 1:00) and a silent burnchain phase of almost 2 min that **logs nothing**. A user watching `docker logs` sees the node "hang" there. The `start_period` ≥ 10 min decision (owner, [O]) still covers both hosts with margin. The CLI/TUI status (doc 13) should label this phase, for example "processing Bitcoin headers", rather than call it stuck.
- **The "tip not advancing" gate in doc 12 also needs `neighbors_outbound > 0`.** On this host it was 0 for about 1.5–2 min after RPC-up, so 2–3 min of boot grace was not enough; use ≥ 10 min from container start.
- **Q16/Q21 confirmed on amd64:** BTC RPC credentials are optional for a follower.

### Non-root stacks-node, 2026-10-26 [V]

Scratch project `sbd-nr`, `stacks-core:4.0.4`, mainnet, BTC peer via the `ztnuc` tunnel. The project has been removed.

| Check | Result |
|---|---|
| Image default user | `Config.User` is empty, so uid 0. `Cmd = ["/bin/stacks-node","mainnet"]`, no entrypoint. `/root` is `drwx------ root root`; `/bin/stacks-node` is `0755` |
| `--user 1000:1000` with the repo's path `/root/stacks-blockchain/data` | **Fails.** Panic at `stackslib/src/config/mod.rs:1157`: `Failed to create working directory at /root/stacks-blockchain/data`. A fresh named volume at that path is also root-owned |
| `--user 1000:1000`, `working_dir = "/data"`, volume chowned by a one-shot init job | **Works.** Headers synced; `/v2/info` and `/metrics` served; peers connected. Everything under `/data` (`event_observers.sqlite`, `mainnet/{burnchain,chainstate,atlas.sqlite}`) is owned by 1000:1000 |

Conclusion: the node doesn't need root. Only the image default and the `/root/...` data path tie it to root. Tracked as TODO-1 in [11 §F](./11-refactor-guidance.md#f-todos-owner-requested-deferred).

### P2 results — macOS arm64, 2026-10-26 [V]

Same `sbd-p1` stack as P1, with mainnet sample observer settings (`timeout_ms = 300_000`, `event_dispatcher_blocking` default `true`). Source: stacks-core 4.0.4 `stacks-node/src/event_dispatcher/worker.rs:342-391` **[S]**. The backoff starts at 100 ms, then `min(2 × backoff + jitter(0–100 ms), 3 × timeout)`, retrying forever.

| Time (UTC) | Event |
|---|---|
| 17:07:59 | `docker compose stop stacks-blockchain-api`; node tip = API height = 600 |
| 17:08:01 → 17:13:12 | Retries with backoff 2.4, 4.9, 9.8, 19.6, 39.2, 78.4, 156.8, 313.7 s (attempts 4–11). Error: `failed to lookup address information` (the stopped container's DNS name is gone) |
| 17:07:59 → 17:18:26 | **Tip frozen at 600.** The coordinator is blocked on delivery; the node does not process new blocks |
| 17:16:01 | `docker compose start stacks-blockchain-api`; API serving by 17:16:11 |
| ≈17:18:26 | The node's 313.7 s sleep ends; retry succeeds |
| 17:18:42 | Tip 737, API 743: caught up, no events lost |

Results:
- The stall is real and indefinite.
- The cap follows the formula: the next sleeps would have been about 627 s, then 900 s.
- **Recovery lagged the API by 2 min 15 s** here. In the worst case it lags by the full cap (15 min on mainnet). This is filed as **D55** in [09](./09-defects-and-risks.md), with fix options.
- The pending payload survived and was delivered once (no loss).

### P2 results — Linux x64, 2026-10-26 [V]

Same `sbd-p1` stack as the Linux P1 run (mainnet sample observer `timeout_ms = 300_000`), at tip 3 405. The API was stopped for 8 min, as on macOS.

| Time (UTC) | Event |
|---|---|
| 20:28:58 | `docker compose stop stacks-blockchain-api`; node tip = API height = 3 405 |
| 20:29:55 | First failed delivery (`failed to lookup address information: Name or service not known`), 57 s after the stop. The tip was already frozen at 3 405 in between |
| 20:29:55 → 20:36:13 | Attempts 0–11, with logged backoffs of 0.1, 0.29, 0.67, 1.4, 2.9, 5.9, 11.8, 23.6, 47.3, 94.7, 189.5 and **379.0 s** |
| 20:28:58 → 20:42:39 | **Tip frozen at 3 405** (32 samples, 15 s apart) |
| 20:36:59 / 20:37:03 | `docker compose start stacks-blockchain-api` / API serving (height 3 405) |
| ≈20:42:32 | The 379 s sleep ends and the retry succeeds |
| 20:42:39 | Node tip moves (3 419) |
| 20:44:39 | Tip 4 509, API 4 511. Catch-up ran at about 140 blocks per 15 s, because blocks downloaded during the stall were processed back to back. API = tip at every sample, so no events were lost |

Results:
- **The same behavior as macOS, on amd64.** The backoff follows `2 × backoff + jitter`, and the jitter differs per run (here 379 s at attempt 11 vs 313.7 s on macOS).
- **Recovery lagged the API by 5 min 29 s** (20:37:03 → ≈20:42:32), against 2 min 15 s on macOS. The lag depends only on where in the backoff sequence the API returns. It is bounded by the cap (900 s with the mainnet sample), which is why D55/Q26's `timeout_ms = 60_000` (cap 180 s) matters.
- The pending payload was delivered once, and the API stayed consistent.

### P3 results — Linux x64, 2026-10-26 [V]

D54 check on the same `sbd-p1` stack, right after P2:
- A scratch override added `STACKS_EVENT_OBSERVER=stacks-blockchain-api:3700` to the node, next to the TOML observer (`timeout_ms = 300_000`), and only the node was recreated (`up -d --no-deps`).
- Deliveries were counted with the API's `stacks_event_ingestion_timers_count{event=…}`, which counts one per `/new_block` or `/new_burn_block` request handled.

| Window | Tip advance | `block` ingests | `burn_block` ingests | `raw_event` | Ratio block/tip |
|---|---|---|---|---|---|
| Baseline, TOML observer only (20:45:19 → 20:48:19) | 423 | 461 | 485 | 945 | **1.09** |
| TOML + `STACKS_EVENT_OBSERVER` (20:50:22 → 20:54:22) | 493 | 1 070 | 1 154 | 2 248 | **2.17** |

Results:
- **The node delivers every event twice.** At startup the node logged `Registering event observer at: stacks-blockchain-api:3700` for both observers. This confirms the [S] reading (#10) that the observers are not deduplicated by endpoint.
- **The API writes nothing twice:**
  - `blocks`: 6 340 rows, 6 340 distinct `index_block_hash`;
  - `txs`: 55 314 rows, 55 314 distinct `(tx_id, index_block_hash)`;
  - `burn_blocks`: 7 090 rows, 7 090 distinct hashes;
  - no height has more than one canonical block;
  - non-canonical (fork) blocks per height were the same before (0.097) and during (0.085) the window;
  - no `error`-level API log lines.
- The owner's expectation **[O]** holds, so D54 stays **Info**. The cost is a second blocking HTTP round-trip per event. The block rate dipped from 2.35 to 2.05 per second; that is indicative only, as genesis-era block rates vary.
- **Not tested:** the 1 s timeout of the env observer under slow `/new_block` handling (see D54).

### P4 attempt — macOS arm64, 2026-10-26 (blocked) [V]

Setup:
- Scratch project `sbd-p4`: `stacks-core:4.0.4` testnet node plus `stacks-signer:4.0.4`, named volumes, no API.
- Node config: upstream `sample/conf/testnet-signer.toml` (Q27), with `working_dir`, observer host `stacks-signer:30000` and `auth_token` changed.
- Signer key: generated with `refactor/key.js`.

Results:
- **D50 chain, confirmed step by step:** the repo's testnet sample fails as described in D50 (two obsolete keys, then the Epoch 4.0 panic). The upstream sample passes `check-config` and boots.
- **`refactor/key.js` output:** a 64-hex private key plus a compressed public key. `stacks-signer check-config` derives the same compressed public key and address from the 64-hex key and from the `…01` 66-hex form, so either works.
- **`key.js` bug:** `genKeyPair()` sits outside the `while` loop. A 63-hex key (about 1 in 16) therefore makes the loop spin forever without drawing a new key.
- **Blocked:** testnet Bitcoin (`bitcoin.regtest.hiro.so:18444`) delivered 6 000 of 31 691 headers in about 7 min, then stalled (`WouldBlock` read errors), then refused connections (`Failed to connect to peer … retrying in 60 sec`). After **2 h** the node had still not served RPC.
- **Signer with no reachable node:** the signer starts, `/metrics` on 9154 serves **only** `stacks_signer_node_rpc_call_latencies_histogram`, and the run loop retries `GET /v2/pox` forever (`Failed to initialize signer runloop`). Like the node, the signer exports its metric families lazily.
- **Signer metric names:** taken instead from the owner's live signer (see P4 results below).

Consequences:
- **Testnet depends on a single public Bitcoin regtest peer**, and users cannot substitute their own peer for that chain. The P6 smoke test (testnet boot) is therefore flaky by design. The CI boot test should only assert "config loads, node starts, no panic within N s", not RPC-up or sync progress. The real sync test belongs to mainnet with a configurable BTC peer.
- The node's healthcheck and the docs must expect testnet RPC to stay down for a long time while the Bitcoin peer is slow.

### Testnet seed archive vs the testnet reset, 2026-10-26 [V]

- `testnet-stacks-blockchain-latest.sha256` → `testnet-stacks-blockchain-4.0.1-20261026.tar.zst`, 6 825 640 578 bytes (from a ranged GET).
- I streamed the archive and extracted only `krypton/headers.sqlite` (the third tar entry, 9.8 MB). It holds 31 501 rows, max height **31 500**. That matches the **reset** testnet (31 691 headers on 2026-10-26); the pre-reset chain was past 159 350. So the testnet archive is **current and usable**, even though the repo's testnet config is not (D50).
- The archive is produced by a **4.0.1** node, while upstream `main` samples need 4.0.x with Epoch 4.0. Restoring it into a 4.0.4 node is the normal upgrade path. Under Q24's "refuse on mismatch", though, a 4.0.4 pin would refuse this archive. That bears on the open Q24 follow-up about per-network pins.
- Every file in the archive is owned by **uid/gid 1001**. That matters for the non-root TODO (11 §F): the seed job must re-own extracted files to the node's runtime uid, whichever uid that ends up being.

### P4 results — owner's live signer, 2026-10-26 [V]

The owner ran the commands from the P4 notes against a signer they operate and pasted the output. The signer version was not given. That signer exports **13 families**: `stacks_signer_agreement_state_change_reasons` (counter, label `reason`), `stacks_signer_agreement_state_conflicts` (counter, `conflict`), `stacks_signer_block_pre_commits_sent` (counter), `stacks_signer_block_proposals_received` (counter), `stacks_signer_block_response_latencies_histogram` (histogram), `stacks_signer_block_responses_sent` (counter, `response_type` accepted/rejected), `stacks_signer_block_validation_latencies_histogram` (histogram), `stacks_signer_block_validation_responses` (counter, `response_type`), `stacks_signer_current_reward_cycle` (gauge), `stacks_signer_node_rpc_call_latencies_histogram` (histogram, labelled by RPC path), `stacks_signer_nonce` (gauge), `stacks_signer_stacks_node_height` (gauge), `stacks_signer_stx_balance` (gauge).

- These match the source-derived names in 5e. They add `agreement_state_*`, `block_pre_commits_sent`, `block_response_latencies_histogram`, `block_validation_responses` and `nonce`.
- **Series counts in that output:**
  - `node_rpc_call_latencies_histogram`: 10 label sets (one per node RPC path), 120 buckets.
  - `block_response_latencies` and `block_validation_latencies`: one series each, with 13 and 10 buckets.
  - `agreement_state_change_reasons`: 4 of its 6 `reason` values had occurred.
  - `agreement_state_conflicts`: 2 of its 3 `conflict` values had occurred.
- As on the node, families and label values appear lazily (the P4 attempt above saw only one family while the node was unreachable).
- **P4 is closed.** The P4 attempt above is still useful as the "signer, no reachable node" baseline.

### P5 results — Linux x64, 2026-10-26 [V]

`refactor/live-tests/p5-sha-cadvisor.sh /stacks/temp/sbd-p5` on the Linux x64 host (i9-13900H with `sha_ni`). The input was a 2 GiB random file, hashed with a warm page cache.

| Tool | Linux x64 | macOS arm64 (#9) |
|---|---|---|
| coreutils `sha256sum` (Debian host) | 1 537 MB/s | 540 MB/s (Homebrew) |
| `openssl dgst -sha256` (host) | 1 456 MB/s | 2.43 GB/s |
| perl `shasum -a 256` (host) | 343 MB/s | 566 MB/s |
| busybox `sha256sum` (alpine:3.20) | **184 MB/s** | 338 MB/s |
| coreutils `sha256sum` (alpine:3.20) | 868 MB/s | 2.94 GB/s |
| Go `crypto/sha256` (built with golang:1.25) | **1 532 MB/s** | 2.67 GB/s |
| `openssl speed -evp sha256`, 16 KiB blocks | 1.83 GB/s | — |
| Cold sequential read, `/stacks/temp` NVMe (`dd iflag=direct`, 10 GiB of the mainnet archive) | **4.7 GB/s** | — |

- **Implementation still matters about 8×.** busybox, which is the default in Alpine, is the slow case on both architectures (184 vs 1 532 MB/s here). Alpine's coreutils build is about 40% slower than Debian's on x86.
- **SHA-NI x86 is slower than ARMv8 SHA2 on the M4 Pro:** about 1.5 GB/s, against 2.7–2.9 GB/s. Go and OpenSSL both use the SHA extensions here. A single digest is still sequential (09, #132).
- **This corrects the earlier "the cost is mostly disk" conclusion (#9, 14 §9.2) for fast hosts.** The 276 GB chainstate plus the 104 GB dump (380 GB) hash in about **4.1 min** at 1.53 GB/s, but a re-read from this NVMe takes only about 1.4 min. On this class of host the hash is CPU-bound. On a slow disk or network volume the re-read still dominates. Either way, **hash-while-download** removes both the re-read and the separate hashing pass: the digest runs at 1.5 GB/s, faster than any download. Fast hashing also needs the tool choice to avoid busybox in the seed image.

cAdvisor v0.52.1 on native Linux (cgroup v2: `stat -f /sys/fs/cgroup` → `cgroup2fs`). It ran with `--docker_only=true --housekeeping_interval=15s` and the script's `--disable_metrics` list, and cAdvisor was the only running container:
- **67 `container_*` families**, including every name doc 12 targets: `container_cpu_usage_seconds_total`, `container_memory_working_set_bytes`, `container_fs_reads_bytes_total`/`container_fs_writes_bytes_total`, `container_blkio_device_usage_total`, `container_network_*` and `container_pressure_*` (PSI). It also has per-device `container_fs_*` I/O counters (`fs_io_time_*`, `fs_read_seconds_total`, `fs_reads_merged_total`, `fs_sector_*`) and `container_fs_usage_bytes`/`inodes_*`. The macOS run didn't record a full family list, so no diff was made. cAdvisor also exports `container_network_advance_tcp_stats_total`, because the script's `--disable_metrics` list omits `advtcp`. Use the full list from the reproduction notes below.
- **Overhead:** 26.9 MiB RSS and 0.00% CPU in a single `docker stats` sample. That is about 2× the 14 MiB on Docker Desktop, which is still well inside doc 12's budget. The real figure under load is still deferred to the phase 5 stress test.

### P6 — macOS arm64, 2026-10-26 [V]

`refactor/live-tests/p6-testnet-smoke.sh 4.0.4 90`, using upstream `main` `testnet-follower-conf.toml` with only `working_dir` added: `check-config` → `Loaded config!`; the node was **running after 90 s with 0 restarts and 0 panics** → **P6 PASS**. The repo's own testnet sample fails the same test (D50). 
### P6 — Linux x64, 2026-10-26 [V]

`refactor/live-tests/p6-testnet-smoke.sh 4.0.4 180` on the Linux x64 host (`stacks-core:4.0.4` amd64 image). The upstream `main` `sample/conf/testnet-follower-conf.toml` was last changed in `6d794b10f1a3` (2026-08-10). Results:
- `check-config` → `Loaded config!`.
- After **180 s** the node was `running` with **0 restarts and 0 panics** → **P6 PASS**. Wall time was 3 min 05 s.
- The log had no `Syncing Bitcoin headers` line within 180 s, so the node was still connecting to Hiro's regtest BTC peer. That is consistent with the P4 attempt, and it is why the test doesn't assert sync.

P6 now passes on both test hosts, so the Q27 config source boots on arm64 and amd64.

### Seed hotfix end to end — testnet, Linux x64, 2026-10-26 [V]

Phase 0 `scripts/seed-chainstate.sh`, run as `sudo … --yes` in a scratch repo copy on `/stacks/temp` with `NETWORK=testnet`, default pins (core 4.0.4, API 9.7.0, pg17) and `SEED_DOWNLOAD_DIR` on the LV. Afterwards the seeded data was booted as project `sbd-seed` (renamed containers). Everything was removed afterwards.

| Step | Result |
|---|---|
| Selection | `20261026`: core **4.0.1**, API **9.0.2**, pg17, with the "older than the pins" warning (as expected under Q24) |
| Download + verify | 2.66 GB dump + 6.36 GB chainstate, both SHA-256 matched. Download, verify and extract took about 5.5 min |
| Run 1 | **Failed at `pg_restore`**: 90 × `role "hirosystems" does not exist` (the dump's object owners). `pg_restore` exits 1 on these, so the script stopped before its rename steps. **This bug predates Phase 0:** the original script runs the identical command. A second pre-existing bug: without a tty, `who am i` is empty, so the data was chowned to **root** |
| Fix | `pg_restore --no-owner --no-privileges`; `CURRENT_USER=${SUDO_USER:-…}` |
| Run 2 | **Success.** Both existing files were found at full size, verified and **reused** (and kept at the end, since this run didn't download them). Restore: 0 errors. Chown to the invoking user. Schema/DB renames OK. About 4 min |
| Boot of the seeded data | No restarts or panics. The API applied **27 pending migrations** (9.0.2 → 9.7.0) in about 10 s. Node 4.0.4 migrated its sortition/chainstate schema. Node and API were both at tip **848 711** and the API was `ready`; burn height 31 866 (past the snapshot's 31 500). So an older archive on newer pins within a major works, matching the owner's [O] |

Not covered: the running check against live containers (it would need the fixed names `postgres`/`stacks-blockchain`), and a fresh resume of a partial download.

Test note: Compose's `ports: !reset [...]` **ignores the given value** and leaves no ports; use `!override [...]` to replace them. The CI boot script only uses `!reset []`, which is correct.

### Phase 1 root `compose.yaml` — Linux x64, 2026-10-27 [V]

Plain `docker compose` from a repo copy (`.github/ci/compose-boot.sh`, project `sbd-compose` with renamed containers), using the `config` renderer job (`tools/config`).

| Run | Result |
|---|---|
| testnet `node,api`, 180 s | PASS: `config` + `config-check` exit 0; postgres healthy; API and node running, still in their start period (slow testnet BTC peer); `down` 3 s |
| mainnet `node,api,signer,proxy` with the LAN BTC peer, 420 s | First run: node health stayed `starting`, and `down` took **1212 s**. After the fixes below, **all five services healthy** and `down` took **14 s** |

Findings:
- **API 9.7.0 opens its event port (3700) at once but serves HTTP (3999) only after the node's RPC answers.** It retries `/v2/info` every 60 s. So the node must not wait for the API to be *healthy*; that deadlocked `up` until the API's healthcheck gave up (about 3 min) and Compose started the node anyway, since the dependency was optional. Fix: the node depends on the API with `service_started`, and the API healthcheck has the node's 600 s start period. This also corrects P1's "ready before the node was up".
- **stacks-node 4.0.4 answers `GET /v2/info HTTP/1.0` without a `Host` header with `400`.** The healthcheck in the reproduction notes (verified on 4.0.1) never passed on 4.0.4. Fixed with HTTP/1.1 plus `Host`.
- **Shutdown order with a signer:** `down` stops dependents first. With the signer depending on the node, the signer stopped first, and the node then retried deliveries to the missing signer observer until it was killed at `stop_grace_period` (1200 s). This is the D46 mechanism applied to shutdown. Fix: **every observer outlives the node.** The node depends on the API and on the signer (`service_started`, `required: false`), so they start first and stop last. The signer retries until the node's RPC answers (P4). A graceful SIGTERM of a syncing mainnet node took 4 s.
- `scripts/postgres-initdb.sh` needs `PG_USER`/`PG_DATABASE`/`PG_SCHEMA`. These came from `env_file: .env` before; they are now in the postgres `environment:` allow-list.
- `config-check` and `signer-config-check` (pinned images) passed for every profile set, and the renderer's own tests run during its image build.

### Upstream template sync, tier 0 — Linux x64, 2026-10-27 [V]

`tools/sync-upstream-conf.sh` vendored `sample/conf/{mainnet,testnet}-follower-conf.toml` from stacks-core `main` @ `26a964b` (the testnet file is unchanged since `097641bd`) and regenerated both `Config.toml.sample` with `config template`.

| Check | Result |
|---|---|
| Rendered configs before vs after the regeneration, every network × `node` / `node,api` / `node,api,signer` (parsed TOML) | **Identical** except testnet `node.miner = false` is no longer written; `false` is stacks-node's default, so behavior is unchanged |
| `check-config` with the pinned images on every rendered config | All valid |
| `--check` against current `main` | `in sync`, exit 0, repo untouched |
| `--check` against an older upstream commit (`9261045`) | Exit 1: `DRIFT` on `upstream/testnet-follower-conf.toml`, `upstream/SOURCE` and both samples, with the diff printed; repo untouched |
| Consistency test (committed sample == generated from committed upstream + deployment) | Passes; it runs inside the config image build, so a hand-edited sample fails CI |
| Compose-only boot, testnet `node,api` | PASS |

### Phase 2M monitoring profile — Linux x64, 2026-10-27 [V]

`compose-boot.sh mainnet node,api,signer,monitoring 420` with the LAN BTC peer. Images: Prometheus v3.15.0, Grafana 13.0.2, cAdvisor v0.55.1, node-exporter v1.12.1, postgres-exporter v0.20.1.

| Check | Result |
|---|---|
| Health after 420 s | All 9 services running, 0 restarts; postgres, API, node, signer, Prometheus, Grafana and cAdvisor **healthy**; no panics |
| Scrape targets (rendered from `COMPOSE_PROFILES` by the config job) | 7/7 **up**: stacks-node, stacks-api, stacks-signer, postgres, cadvisor, node-exporter, prometheus |
| Rules | 13 rules healthy, 0 evaluation errors; `promtool check rules` passes, and the unit tests (tip-stall with and without peers, API lag value and alert, the 15 m down-target grace) pass |
| Dashboards | All 3 provisioned in folder *Stacks*. **Every panel query returns data** (CPU/memory/net by service: 9 series), except "Firing alerts" (none firing). One query was wrong in the first run ("Load per CPU", vector label mismatch) and is fixed with `scalar()` |
| Grafana | Datasource health "Successfully queried the Prometheus API"; anonymous access → 401. Without `GRAFANA_ADMIN_PASSWORD` it exits with the message, 3 tries (`on-failure:3`) |
| Secrets (doc 12 acceptance 5) | `compose-config.sh` asserts no `.env` secret in the monitoring containers' environment (postgres-exporter DB credentials and Grafana's own password allowed) |
| Idle overhead | Prometheus 72 MiB, cAdvisor 27 MiB, postgres-exporter 20 MiB, node-exporter 17 MiB: **≈137 MiB without Grafana** (target < 400 MB), Grafana 160 MiB, all < 1% CPU. 5 640 head series (everything is collected for now; M7 defers trimming) |

Findings:
- **cAdvisor v0.52.1 sees no containers on Docker 29 with the containerd image store** (`storage=overlayfs`). Its Docker factory registers, but `DockerVersion` stays empty and it exports only the root cgroup (`id="/"`). **v0.55.1 works**: per-container series with the compose project/service labels, 19.7 MiB. **Correction to P5:** its cAdvisor result on this host counted metric *families*, which all exist for the root cgroup alone; it never showed per-container series.
- `StacksSignerBehindNode` went pending during the genesis sync. The signer's `stacks_signer_stacks_node_height` moves in batches (99 → 333 → 554) while the node climbs steadily, so the lag swings between about 130 and negative values. Its `for:` is now 15 m.
- Compose `${VAR:?}` fails the **whole** project even when the service's profile is inactive, so the Grafana password check lives in Grafana's entrypoint. `deploy.replicas: 0|1` works as an on/off switch, but only for numbers (`false` is rejected).

## Deferred, with reasons

| Item | Why it can't be settled now | Where it will be |
|---|---|---|
| API worst-case `/new_block` handling time vs the Q26 `timeout_ms = 60_000` | It must stay well under 60 s, otherwise deliveries time out and get retried. That needs a full mainnet sync, including genesis and the largest blocks; P1 only covered the first ~700 blocks. Measure it from API request logs/metrics | Phase 5 stress test |
| Monitoring idle overhead under mainnet load (doc 12 acceptance criterion 4) | Needs a synced node; a 14 MiB cAdvisor at idle is only an indication | Phase 5 stress test |
| How long the node stays blocked, and how block processing behaves, with the API down under real load | The semantics are confirmed in source (5a). Real stall durations need a synced node | Phase 5 stress test |
| How long a fresh node takes before RPC/metrics come up (5f) | Depends on the Bitcoin peer; the default `bitcoin.mainnet.stacks.org` dropped connections during this test | Phase 5; also sets the healthcheck `start_period` |

## Reproduction notes

- **Healthcheck commands that worked:**
  - node (bash, no curl): `bash -c 'exec 3<>/dev/tcp/127.0.0.1/20443 && printf "GET /v2/info HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n" >&3 && head -1 <&3 | grep -q " 200 "'`. **Corrected 2026-10-26:** stacks-node 4.0.4 answers the earlier HTTP/1.0 request without `Host` with `400 Bad Request`
  - API: `node -e "fetch('http://127.0.0.1:3999/extended/v1/status').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"`
  - postgres: `pg_isready -U postgres`
- **Config validation:** `stacks-node check-config --config <file>` and `stacks-signer check-config --config <file>` exit non-zero on unknown fields, which makes them suitable for the `config` job and CI.
- **cAdvisor flags used:**
  - `--docker_only=true --housekeeping_interval=10s --store_container_labels=false`
  - `--whitelisted_container_labels=com.docker.compose.service,com.docker.compose.project`
  - `--disable_metrics=percpu,sched,tcp,udp,disk,hugetlb,referenced_memory,cpu_topology,resctrl,advtcp,memory_numa,process` (keep `diskIO` for IO metrics)
- **Bind mounts from the session scratch dir are refused** on this Docker Desktop ("path is not shared from the host"). This is the same class of problem the storage-backend decision (Q16) addresses: named volumes worked everywhere.
