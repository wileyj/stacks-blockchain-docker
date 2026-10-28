# Monitoring

An opt-in Prometheus + Grafana stack that answers three questions without leaving the host: is the
chain healthy, are the containers healthy, and is the host about to run out of disk.

## Enable it

Set an admin password for Grafana and add `monitoring` to the profiles in `.env`:

```bash
GRAFANA_ADMIN_PASSWORD=<a strong password>
COMPOSE_PROFILES=node,api,monitoring
```

```bash
docker compose up -d
# or: ./manage.sh -n mainnet -a start -f monitoring
```

| Service | URL (from the host) | |
|---|---|---|
| Grafana | http://127.0.0.1:3000 (`GRAFANA_PORT`) | user `admin`, password `GRAFANA_ADMIN_PASSWORD` |
| Prometheus | http://127.0.0.1:9090 (`PROMETHEUS_PORT`) | targets, rules and alerts |

Both listen on `MONITORING_BIND` (default `127.0.0.1`). To reach them from another machine, prefer an
SSH tunnel (`ssh -L 3000:127.0.0.1:3000 <host>`) over binding them to a public address.

## What's collected

| Source | What |
|---|---|
| stacks-node `:9153` | tip and burn height, peers, blocks processed, RPC latency |
| API `:9153` (on with `NODE_ENV=production`) | API tip, mempool, event ingestion |
| signer `:9154` (signer profile) | proposals, responses, validation latency, reward cycle, balance |
| postgres-exporter | database size, connections, locks |
| cAdvisor | per-container CPU, memory, disk and network I/O |
| node-exporter | host disk space, memory, load, I/O |

Scrape targets follow `COMPOSE_PROFILES`: the `config` job renders `conf/monitoring/prometheus.yml`, so a
disabled service never shows as a down target. Prometheus keeps `PROMETHEUS_RETENTION` (default 15 days)
of data in the `prometheus-data` volume; Grafana's state is in `grafana-data`. `docker compose down -v`
removes both.

## Dashboards

Grafana opens with three provisioned dashboards in the **Stacks** folder: **Stacks node** (tips, API and
signer lag, peers, block rate, scrape targets, firing alerts), **Stacks containers** and **Stacks host**.
They are read-only; save a copy to customize one.

The node serves no metrics during its initial Bitcoin header sync (several minutes, longer on testnet),
so the chain panels stay empty until then.

## Alerts

`conf/monitoring/rules/stacks.yml` defines recording rules (`stacks:api_lag`, `stacks:signer_lag`, ...)
and alerts, visible under Prometheus → Alerts and on the Stacks node dashboard:

| Alert | Fires when |
|---|---|
| `StacksTipStalled` | no new Stacks block for 35 min while the node has peers |
| `StacksNoPeers` | no outbound peers for 15 min |
| `StacksApiBehindNode` / `StacksSignerBehindNode` | API more than 10 blocks behind for 10 min / signer more than 5 for 15 min |
| `StacksSignerRejecting` | the signer rejects more than half of the proposals for 30 min |
| `StacksTargetDown` | a scrape target is down for 15 min |
| `ContainerRestarting`, `ContainerMemoryHigh` | a stack container restarts repeatedly / uses over 90% of host memory |
| `HostDiskSpaceLow` | a filesystem has less than 10% free |

No notifications are sent yet (no Alertmanager). The thresholds are starting points.

## Options

| Variable | Default | |
|---|---|---|
| `MONITORING_GRAFANA` | `1` | `0` runs without Grafana (Prometheus only) |
| `MONITORING_CONTAINER_METRICS` | `1` | `0` skips cAdvisor, which needs privileged mode and host mounts |
| `PROMETHEUS_RETENTION` | `15d` | how long Prometheus keeps data |
| `*_VERSION` | see `sample.env` | image pins for Prometheus, Grafana, cAdvisor and the exporters |

The switches take `0` or `1` (not `true`/`false`).

## Notes

- cAdvisor runs privileged with read-only host mounts (`/`, `/sys`, `/var/run`, `/var/lib/docker`).
- On Docker Desktop (macOS, Windows) cAdvisor and node-exporter see Docker's Linux VM: disk and memory
  figures describe the VM, not your computer.
- postgres-exporter gets the API's database credentials; no other monitoring container receives a
  secret from `.env`.
