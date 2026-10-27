# Live tests (P1–P6)

These are reproducible versions of the live checks in [15](../15-verification-log.md). They never touch the repo's `.env`, `conf/` or `persistent-data/`. Each run uses its own Compose project (`sbd-p*`) with named volumes. Remove it afterwards: `docker compose -p <project> down -v`.

| File | Test | Notes |
|---|---|---|
| `p1-compose.yaml` | **P1/P2:** mainnet follower (core 4.0.4) + API 9.7.0 + postgres 17 from genesis | Set `BTC_HOST`/`BTC_P2P_PORT`/`BTC_RPC_PORT`; the default is the LAN test node `192.168.8.252:8333/8332`. No BTC RPC credentials (P1 showed a follower doesn't need them). Ports are on 127.0.0.1: node RPC 30443, node metrics 29153, API 33999, API metrics 39153 |
| `p5-sha-cadvisor.sh` | **P5:** SHA-256 throughput (coreutils, OpenSSL, perl shasum, busybox, coreutils in Alpine, Go) and cAdvisor v0.52.1 metric names and overhead | Linux x64. Needs about 2.2 GB in the work dir, and pulls `alpine`, `golang:1.25` and cAdvisor |
| `p6-testnet-smoke.sh` | **P6:** testnet boot using upstream `main` `sample/conf/testnet-follower-conf.toml` (Q27) | Pass = `check-config` OK and the node stays up with no panic or restart for N seconds. It does not wait for sync, because Hiro's regtest BTC peer is slow |

P1 on the host:

```bash
cd refactor/live-tests
docker compose -f p1-compose.yaml up -d          # time from here
until curl -sf 127.0.0.1:29153/metrics >/dev/null; do sleep 10; done; date -u
curl -s 127.0.0.1:29153/metrics | grep '^# TYPE'  # metric families at RPC-up; repeat after a few minutes
curl -s 127.0.0.1:30443/v2/info; curl -s 127.0.0.1:33999/extended
docker compose -f p1-compose.yaml logs stacks-blockchain | grep -E 'burnchains/|ERRO|panic'   # expect no bitcoind RPC errors
docker compose -f p1-compose.yaml down -v
```

P2 (optional repeat): `docker compose -f p1-compose.yaml stop stacks-blockchain-api`, watch `stacks_node_stacks_tip_height` freeze and the `Event dispatcher: connection or request failed … backoff` lines. Then `start` it again and time how long the node takes to resume. The Mac numbers are in 15 (P2) / D55.
