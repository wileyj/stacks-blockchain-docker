# 12 — Monitoring Plan (HIGH PRIORITY)

> **Implemented 2026-10-27 (phase 2M):** the `monitoring` profile in `compose.yaml`, `conf/monitoring/`, `docs/monitoring.md`. Deviations and verification: [HANDOFF](./HANDOFF.md) "Phase 2M", [15](./15-verification-log.md) "Phase 2M". cAdvisor needs **v0.55+** on Docker 29 with the containerd image store.

**Goal:** a lightweight, opt-in monitoring stack that answers three questions without leaving the host:

1. **Is the chain healthy?** Node synced? Tip advancing? API keeping up? Signer participating?
2. **Are the containers healthy?** CPU, memory, disk I/O and network per container; restarts.
3. **Is the host going to fall over?** Disk space (chainstate plus archives can exceed 1.5TB), host memory, load.

Everything is collected with **Prometheus**. Grafana is an optional view on top. Nothing here changes the existing services' behavior.

## What already exists (baseline from [05](./05-compose-topology.md) / [06](./06-configuration-reference.md))

| Source | Endpoint | State today |
|---|---|---|
| stacks-node | `prometheus_bind = "0.0.0.0:${NODE_METRICS_PORT}"` (9153), `/metrics` | **Already on**, and published on the host on all interfaces |
| stacks-signer | `metrics_endpoint = "0.0.0.0:${SIGNER_METRICS_PORT}"` (9154) | On with `-f signer`, published on the host |
| stacks-node RPC | `/v2/info`, `/v2/neighbors` on 20443 | JSON, not Prometheus |
| API | `/extended/v1/status`, `/extended` on 3999; the API serves Prometheus `/metrics` on a **fixed port 9153** whenever `NODE_ENV=production` (no env var) **[V]** ([15](./15-verification-log.md) #11a) | **Already on** with `sample.env`, not published (scrape over the Docker network) |
| postgres | none | — |
| containers / host | none | — |

Nothing scrapes these today, and there is no retention, dashboards or alerting.

## Proposed components

| Component | Image (pin exact versions at implementation time) | Purpose | Footprint (approx.) |
|---|---|---|---|
| **prometheus** | `prom/prometheus` | Scrape, store (TSDB), evaluate alert rules | ~100–300 MB RAM; disk ≈ samples × retention |
| **cadvisor** | `gcr.io/cadvisor/cadvisor` | Per-container CPU / memory / block I/O / network / restarts | ~50–150 MB RAM after tuning (below) |
| **node-exporter** | `prom/node-exporter` | Host disk free and I/O, memory, load, filesystem of `persistent-data/` | ~20 MB |
| **postgres-exporter** | `prometheuscommunity/postgres-exporter` | DB size, connections, locks, replication-free stats, slow-query indicators | ~20 MB |
| ~~json-exporter~~ | — | **Dropped (M3, 2026-10-25).** The node and API already export tip/burn heights as Prometheus metrics; derived signals are recording rules | — |
| *grafana (optional)* | `grafana/grafana-oss` | Provisioned dashboards | ~100–200 MB |

**Lightweight tuning for cAdvisor** (its main cost):
- `--docker_only=true`
- `--housekeeping_interval=30s`
- `--disable_metrics=` everything except `cpu,memory,diskIO,network`
- `--store_container_labels=false`

**Lighter alternative to consider:** replace Prometheus with VictoriaMetrics single-node, which is Prometheus-compatible, uses less RAM and disk, and runs the same scrape config. Recommendation: **start with Prometheus** because you named it; VictoriaMetrics is a drop-in swap if footprint matters.

## Metrics to collect

### Blockchain

| Signal | Source | Example use |
|---|---|---|
| Stacks tip height, burn block height | node `/metrics`: `stacks_node_stacks_tip_height`, `stacks_node_burn_block_height` **[S]**. API: `stacks_block_height`, `burn_block_height` **[V]** | Tip advancing? |
| **Sync lag** | `/v2/info` `burn_block_height` vs the bitcoin tip seen by the node; node tip vs a public reference (optional) | "Synced" indicator |
| **API lag** | API `/extended/v1/status` `chain_tip.block_height` vs node `stacks_tip_height` | API falling behind the node (the classic "missing parent block" state in `docs/issues.md`) |
| Peer count | node `/metrics` and `/v2/neighbors` | P2P health |
| Mempool size, block processing time | node `stacks_node_mempool_outstanding_txs`, `stacks_node_stx_blocks_processed_total`, `stacks_node_last_block_runtime`; API `mempool_tx_count`, `stacks_event_ingestion_timers` **[S]/[V]** | Performance |
| Signer participation and proposal handling | signer `stacks_signer_block_proposals_received`, `stacks_signer_block_responses_sent{response_type}`, `stacks_signer_block_validation_responses{response_type}`, `stacks_signer_block_validation_latencies_histogram`, `stacks_signer_block_response_latencies_histogram`, `stacks_signer_block_pre_commits_sent`, `stacks_signer_current_reward_cycle`, `stacks_signer_stacks_node_height`, `stacks_signer_stx_balance`, `stacks_signer_nonce`, `stacks_signer_agreement_state_change_reasons{reason}`, `stacks_signer_agreement_state_conflicts{conflict}`, `stacks_signer_node_rpc_call_latencies_histogram` **[V]** (live signer, [15](./15-verification-log.md) P4) | Signer health (signer profile only). Candidate rules: **rejection ratio** `rate(responses_sent{response_type="rejected"}) / rate(responses_sent)`; **no proposals** in N min while the node tip advances; **signer behind node** (`stacks_signer_stacks_node_height` < `stacks_node_stacks_tip_height` − k); **low STX balance** (fees); p95 response/validation latency; a rising `agreement_state_conflicts` rate. Families appear lazily, so alert on `absent()` for `current_reward_cycle` and `stacks_node_height` only |
| Event-observer delivery backlog | **No node metric exists** **[S]**. Use the API's `stacks_last_event_timestamps` (event age) **[V]**, plus a stuck node tip. An exact pending-payload count (from `event_observers.sqlite`) would need the deferred `stacks-status` add-on (M3) ([15](./15-verification-log.md) #5d) | Node → API / signer pipeline; a blocked dispatcher stalls the node (D46) |

### Containers (cAdvisor)

| Signal | Metric |
|---|---|
| CPU | `container_cpu_usage_seconds_total` (rate) |
| Memory | `container_memory_working_set_bytes`, `container_memory_rss`; OOM kills |
| Disk I/O | `container_fs_reads_bytes_total` / `container_fs_writes_bytes_total` (or blkio equivalents, depending on cgroup v1/v2) |
| Network | `container_network_{receive,transmit}_bytes_total` |
| Restarts | `container_start_time_seconds` changes / engine restart count |

### Host and DB

| Signal | Metric |
|---|---|
| Free space on the filesystem holding `persistent-data/` | `node_filesystem_avail_bytes` (**most important host metric**) |
| Host I/O wait, memory, load | node-exporter |
| DB size, connections, deadlocks | postgres-exporter |

## Health checks and status companions ([#153](https://github.com/stx-labs/stacks-blockchain-docker/issues/153))

#153 asks for "lightweight companion containers for node status and health checks". Split into two layers:

| Layer | What | Default |
|---|---|---|
| **Compose healthchecks** (no extra containers) | node: `/v2/info` answers; API: `/extended/v1/status` answers; postgres: `pg_isready`; signer: metrics endpoint answers. Used by `depends_on: condition: service_healthy` and shown in `docker compose ps`, the CLI and the TUI | **Always on** (part of the [14](./14-platform-and-architecture.md) phase 1 compose restructure) |
| **`stacks-status` companion** — **deferred (M3, 2026-10-25)**: not in the first release; possible later add-on. (tiny Go container, a few MB) | Polls node/API/signer and exposes (a) `/healthz` and `/status` JSON with derived signals (synced?, sync %, tip age, API lag, peer count, signer participating) and (b) Prometheus `/metrics` for those derived gauges. Replaces json-exporter, and gives the CLI, the TUI and external load balancers one stable endpoint | Part of `monitoring`; small enough to consider default-on later |

Healthcheck tooling per image **[V]** ([15](./15-verification-log.md) #3):
- stacks-core and stacks-signer have no curl or wget, but have bash, so use `/dev/tcp` (tested).
- The API has `node`, so use `fetch` (tested).
- postgres has `pg_isready` and `wget`.
- nginx has curl and wget.

No companion is needed for basic health. The node's RPC and metrics **don't answer during the initial Bitcoin header sync** (more than 5 minutes on a fresh node) **[V]**, so the node healthcheck needs a long `start_period`, and first boot must not gate on `node: service_healthy`. Owner **[O]**: it takes seconds to minutes depending on sync position, and a genesis sync takes several minutes to emit its first metrics. Size `start_period` (≥ 10 min) and the alert grace period for the genesis case. Measured with a LAN Bitcoin peer **[V]** (15 P1): RPC/metrics came up after 2 min 05 s (macOS arm64) and 4 min 16 s (Linux x64), and on x64 the first outbound peers appeared about 2 min later still, so ≥ 10 min holds with margin.

## Starter alert rules (Prometheus rules file, no Alertmanager required initially)

| Alert | Condition (sketch) |
|---|---|
| `StacksTipStalled` | node tip height unchanged for more than 30 min (mainnet) |
| `StacksNodeBehindBurnchain` | burn height not advancing, or the node lagging behind a reference |
| `ApiBehindNode` | node tip − API tip > N blocks for 10 min |
| `ContainerRestarting` | more than 2 restarts in 15 min for any stack container |
| `ContainerMemoryHigh` | working set > 90% of its limit or of host memory |
| `DiskSpaceLow` | `persistent-data` filesystem < 10% or < 100 GB free |
| `ScrapeTargetDown` | `up == 0` for any target for 5 min |
| `NoPeers` | peer count == 0 for 10 min |

Alertmanager (email, Slack, etc.) is a later, optional add-on.

## How it fits the repo

Integrating through the existing mechanisms keeps it opt-in and leaves the contract in [11 §A](./11-refactor-guidance.md) unchanged.

| Piece | Location | Notes |
|---|---|---|
| Compose overlay | `compose-files/extra-services/monitoring.yaml` | Today's mechanism: auto-discovered as flag `monitoring`: `./manage.sh -n mainnet -a start -f monitoring` (or `-f signer,monitoring`), profile `stacks-blockchain`. **After the rewrite** it is the `monitoring` profile in the root project: add `monitoring` to `COMPOSE_PROFILES` in `.env` and run `docker compose up -d`, with no CLI needed ([14 §11](./14-platform-and-architecture.md)); `sbd start --with monitoring` does the same |
| Prometheus config | `conf/monitoring/prometheus.yml`, `conf/monitoring/rules/*.yml` | Scrape by service DNS on `${DOCKER_NETWORK}`: `stacks-blockchain:9153`, `stacks-signer:9154`, `cadvisor:8080`, `node-exporter:9100`, `postgres-exporter:9187`, **`stacks-blockchain-api:9153`** (API metrics, already on). Scraping the signer when it isn't running just shows `up=0`; either accept that or template the targets |
| Recording rules | `conf/monitoring/rules/stacks.yml` | `stacks:api_lag = stacks_node_stacks_tip_height - on() stacks_block_height`; tip-stall (`changes(stacks_node_stacks_tip_height[15m]) == 0`, gated on `stacks_node_neighbors_outbound > 0`, which was 0 for 1.5–2 min after RPC-up on x64 [V]); event age from `stacks_last_event_timestamps`. Grace period at first boot, because the node serves no metrics during header sync |
| Grafana provisioning | `conf/monitoring/grafana/{datasources,dashboards}/` | Pre-built "Stacks node", "Containers" and "Host" dashboards |
| Data | `persistent-data/monitoring/{prometheus,grafana}` | **Shared across networks** (labels carry `network`). `reset` does not touch it. Retention via `--storage.tsdb.retention.time=${PROMETHEUS_RETENTION:-15d}` |
| New `.env` vars | `sample.env` | `PROMETHEUS_PORT=9090`, `GRAFANA_PORT=3000`, `PROMETHEUS_RETENTION=15d`, `MONITORING_BIND=127.0.0.1`, `GRAFANA_ADMIN_PASSWORD=` (required if Grafana is enabled), image version pins |
| External labels | prometheus.yml | `network=${NETWORK}`; render it with Compose `configs: content:` (simple file, no conditionals needed) so it works without the CLI |

### Requirements this places on the refactor

| Requirement | Why | Related |
|---|---|---|
| **Monitoring must not see the full `.env`** | Don't repeat D06: give monitoring containers explicit `environment:` only, never `env_file` | D06, Q5 |
| **Bind monitoring ports to `127.0.0.1` by default** | Prometheus and Grafana must not be public | D07, Q5 |
| **Flag persistence** | Today `restart` without `-f monitoring` tears monitoring down and loses continuity. Solved by `COMPOSE_PROFILES` in `.env` (Q3/Q22) | D14, Q3 |
| **Port templating** | Scrape targets must follow `NODE_METRICS_PORT` / `SIGNER_METRICS_PORT` | D16, Q6 |
| **Unpublish internal metrics ports** (optional) | Once Prometheus scrapes over the Docker network, 9153/9154 no longer need host publishing | D07 |
| **`logs export` coverage** | Include the monitoring containers, or exclude them deliberately | D44 |
| **Ordered stop** | The monitoring services can stop in any order but should keep running until the node has stopped, to capture shutdown metrics | §A.6 |

### Platform caveats

- **cAdvisor needs host mounts** (`/`, `/var/run`, `/sys`, `/var/lib/docker`, `/dev/disk`) and is effectively privileged. Document this. Allow `MONITORING_CONTAINER_METRICS=false` to skip it.
- **Docker Desktop (macOS)**: cAdvisor and node-exporter see the Linux VM, not the Mac host. Disk and memory figures describe the VM. Acceptable, but document it.
- cgroup v1 vs v2 changes some cAdvisor metric names. Verified on cgroup v2 (Docker Desktop) **[V]**: `container_cpu_usage_seconds_total`, `container_memory_working_set_bytes`, `container_fs_reads_bytes_total`/`container_fs_writes_bytes_total`, `container_blkio_device_usage_total`, `container_network_*`, `container_pressure_*`, labeled by compose service via `--whitelisted_container_labels`. Tuned cAdvisor used about 14 MiB and under 1% CPU ([15](./15-verification-log.md) #7). Also verified on **native Linux x64 cgroup v2** (cAdvisor v0.52.1, P5) **[V]**: the same names are present (67 `container_*` families), and it used about 27 MiB idle ([15](./15-verification-log.md) "P5 results").

## Acceptance criteria

1. `./manage.sh -n mainnet -a start -f monitoring` (after the rewrite: `docker compose up -d` with `monitoring` in `COMPOSE_PROFILES`, no CLI) brings up the stack plus the monitoring services; `stop` / `docker compose down` brings everything down in the correct order.
2. All scrape targets show `up == 1` on mainnet and testnet (signer target only with `-f signer`).
3. A dashboard (or the TUI, [13](./13-tui-plan.md)) shows tip height, sync lag, API lag, peers, and per-container CPU/memory/IO, plus host disk free.
4. Idle overhead of the monitoring set (without Grafana) stays under about 400 MB RAM and about 2% CPU on a 2-vCPU host. Deferred to the phase 5 stress test; indicative: cAdvisor about 14 MiB idle **[V]**.
5. No secret from `.env` appears in any monitoring container's environment (assert this in the compose-config snapshot test).
6. Harness goldens are unchanged for every scenario that doesn't use `-f monitoring`; new scenarios cover the flag.

## Open decisions

| # | Question | Recommendation |
|---|---|---|
| M1 | Prometheus or VictoriaMetrics | **Settled by the owner (2026-10-25):** Prometheus, with retention set by `PROMETHEUS_RETENTION` (default 15d). |
| M2 | Ship Grafana by default inside the flag, or as a separate flag | **Settled by the owner (2026-10-25):** Grafana ships inside the `monitoring` profile with provisioned dashboards (Stacks node, Containers, Host), bound to 127.0.0.1 with a required admin password. `MONITORING_GRAFANA=false` disables it. |
| M3 | Use json-exporter or write a small purpose-built "stacks-exporter" | **Settled by the owner (2026-10-25):** Prometheus scrapes node :9153, API :9153 and signer :9154 **directly**. API lag, tip stall and event age are recording/alert rules. **No json-exporter and no helper container in the first release.** `stacks-status` is a possible later add-on for sync % against the Bitcoin tip, the exact event backlog and a `/healthz` URL. |
| M4 | Should monitoring be on by default once stable? | **Settled by the owner (2026-10-25):** Opt-in. Healthchecks are always on; users add `monitoring` to `COMPOSE_PROFILES`. Revisit after the #153 stress test measures overhead. |
| M5 | Alertmanager / notifications | **Settled by the owner (2026-10-25):** Later phase. The first release ships Prometheus alert rules, visible in Prometheus and Grafana. Notification channels (Alertmanager or Grafana alerting) come after the stress test has tuned the thresholds. |
| M6 | Enable the API's own Prometheus endpoint | **Settled by the owner (2026-10-25):** Nothing to decide: the API already serves `/metrics` on :9153 whenever `NODE_ENV=production` [V]. Add it as a scrape target. |
| M7 | How much to collect | **Deferred by the owner (2026-10-27):** for now every series each exporter offers is stored, which is fine during development but too broad for a release. Before the first release with monitoring, trim to application-level metrics (node, API, signer) plus core system metrics, mainly storage, with possibly memory/CPU, via `metric_relabel_configs` keep-lists and exporter collector flags. Every series used by the rules and dashboards stays. Details in HANDOFF. |
