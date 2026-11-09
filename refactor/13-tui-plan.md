# 13 — TUI Plan (optional, exploratory)

**Goal:** a terminal UI with two jobs. *(Owner, 2026-11-05: the setup is called `sbd init`, not a "wizard", and it is still needed.)*

1. **`sbd init`:** first-time setup that produces a valid `.env` and config, replacing "copy `sample.env` and hand-edit".
2. **Live dashboard:** real-time view of services, chain status and resource use while the stack runs, with safe access to the common actions.

The TUI is a **client**. It must never reimplement lifecycle logic (ordered stop, replay guard, signer toggle, config rendering). Those stay in the core described in [11](./11-refactor-guidance.md), so the CLI and the TUI can never disagree.

**The TUI is optional, like the CLI** (owner requirement; [14 §2.1](./14-platform-and-architecture.md)). Nobody has to install or run it to operate the stack:
- **Nothing exclusive to it.** Everything `sbd init` does can be done by editing `.env`, and every dashboard action has a plain `docker compose` equivalent ([14 §11](./14-platform-and-architecture.md)).
- **Its output is the hand-editable files.** `sbd init` writes only `.env` and `conf/<net>/*.overrides.toml`, the same files a manual user edits. It keeps no private state.
- **It attaches to whatever is running.** The dashboard finds the stack by its compose project name and labels, so it works on a stack started with `docker compose up -d`. Quitting the TUI never stops the stack.

## 1. `sbd init`

**Implemented 2026-11-05, revised 2026-11-06** with the owner, step by step (`tools/sbd/init.go`):

| Step | Asks / does | Why |
|---|---|---|
| Preflight | The `sbd doctor` checks, as **warnings only** | Writing `.env` doesn't depend on Docker working; the user still learns what to fix |
| Network | mainnet / testnet; Enter keeps the existing one, else **mainnet** | One network per host; a re-run must not switch it silently |
| Services | **node and API always** (owner: "at a minimum, the api and stacks-node"); signer (**off by default**), proxy, monitoring optional | `COMPOSE_PROFILES`; `sbd` refuses a set without node and api |
| Signer (only if on) | Explains what it implies (node config change, liveness dependency, on-chain registration, key custody, uptime), confirms, then the key masked and format-checked | The signer needs specific config and a private key: only asked when wanted |
| Auth token | Generated when it is the sample's `1234` or empty | The node/signer shared secret and the node's privileged RPC guard; `1234` is public |
| Bitcoin | Host and **P2P** port, recommending the user's own node. **The sample's default peer isn't checked** (owner: it may not answer the handshake while busy; accept that). Any other peer gets a real **Bitcoin P2P handshake** (`version` with the network's magic): answered (agent and height), **busy** (closed or reset during the handshake: accepted, the node retries), wrong (another network, no answer, e.g. the RPC port: re-asked with a hint) or unreachable (re-asked); up to 3 tries | Only P2P matters for a follower; a TCP connect accepts anything listening, including the RPC port or a node on another network |
| Data location | `DATA_ROOT`, then the disk check | Mainnet needs about 1.15 TB; signer data lives there too and belongs in backups |
| Proxy | HTTP on port 80 only; **no TLS** (owner: certificates are out of scope; terminate HTTPS in front, e.g. a CDN proxy) | Follow-up: HTTPS on 443 should become the default |
| Generated secrets | Grafana admin password (monitoring); `PG_PASSWORD` only when no database exists yet | Generated **silently**, listed at the end |
| Review and write | Every change, secrets as `<hidden>`; confirm; `.env` written with a backup | Nothing written without confirmation |
| Bootstrap | Chainstate exists: **keep** (default), or delete it and seed / resync from genesis. None: **seed** (default) or genesis. Running a seed or a reset then needs **its own yes** (default no): the choice alone never starts a 360 GB download or deletes data | A re-run (to add a signer, change the BTC peer) must keep existing data |
| Start | Optional; then **notes like a Helm chart's**: how to read each generated secret (`sbd env get`, or `grep` for plain Compose), where the API, RPC, proxy and Grafana are, what's next | Secrets never printed on the way |

It works non-interactively from an answers file (`sbd init --from answers.env`, KEY=VALUE as in `.env`; `SBD_INIT_BOOTSTRAP=seed|genesis|later` answers the bootstrap step) and with `--yes` (defaults, generated secrets); without a terminal it requires one of them. It writes only `.env`, so it never becomes the only way to configure the node.

## 2. Live dashboard

```
┌ stacks-blockchain-docker ─ mainnet ─ signer,proxy,monitoring ─────────── 14:02:11 ┐
│ SERVICES            state    up      cpu   mem      io r/w        restarts        │
│ stacks-blockchain   running  3d4h    61%   3.1 GiB  42/118 MB/s   0               │
│ stacks-blockchain-api running 3d4h   12%   1.2 GiB  1/3 MB/s      0               │
│ postgres            running  3d4h    9%    2.4 GiB  5/22 MB/s     0               │
│ stacks-signer       running  3d4h    1%    90 MiB   0/0           0               │
├ CHAIN ──────────────────────────────┬ HOST ───────────────────────────────────────┤
│ stacks tip   182,431  (+1 / 4m)     │ disk persistent-data  612 GiB free (31%)    │
│ burn height  921,077  synced ✔      │ load 1.8  mem 9.6/16 GiB                    │
│ api tip      182,431  lag 0         │ ▁▂▃▅▆▇▆▅ node cpu (1h)                       │
│ peers        14                     │                                             │
├ LOGS (stacks-blockchain, filter: WARN+) ──────────────────────────────────────────┤
│ …                                                                                 │
└ [s]tart [x]stop [r]estart [l]ogs [e]xport logs [p]ull [?]help [q]uit ─────────────┘
```

| Panel | Data source | Fallback if monitoring is off |
|---|---|---|
| Services, state, uptime, restarts | `docker compose ps --format json` (5 s) + `docker events` stream | same |
| Disk IO rates | the container's cgroup `io.stat` (docker stats rounds to three digits) | same; `-` where there is no cgroup v2 (macOS, Windows) |
| CPU / memory / IO | Prometheus (cAdvisor) | `docker stats --format json` stream |
| Chain: tip, burn height, peers | node `/v2/info`, `/v2/neighbors`; Prometheus for history | direct HTTP polling |
| Synced / behind | age of the latest Stacks block (`block_time`): synced under 5 min, else "latest block is 3h12m old"; burn block age flagged past 2 h | same |
| Blocks behind (optional) | a public API's tip, only when `SBD_UI_REFERENCE_API` is set in `.env`; within 15 blocks counts as synced | same |
| API lag | API `/extended/v1/status` vs node tip | same |
| Signer health | signer `/metrics` | hidden |
| Host disk/mem/load | Prometheus (node-exporter) | local `statfs` / `/proc` (Linux) |
| Sparklines / history | Prometheus range queries | in-memory ring buffer since the TUI started |
| Logs | `docker logs --follow` per selected service, with a level filter | same |
| Replay status | core `replay status` (same logic as `check_event_replay`) | same |

Actions call the **core** (start/stop/restart/pull, log view and export) and show its progress. Start, pull and logs need no confirmation; stop and restart ask y/N (nothing is lost, but the node goes offline). Destructive or long jobs (`reset`, `seed`, `db upgrade`, `bns`, `migrate`, `init`) and settings changes (`env set`, including turning signer/proxy/monitoring on or off) stay CLI-only: the help screen shows the exact command. The ordered stop's node timeout (`STACKS_SHUTDOWN_TIMEOUT`) must show as progress, not a frozen screen.

## Technology options

| Option | Pros | Cons |
|---|---|---|
| **Go + Bubble Tea / Lip Gloss** | Single static binary, cross-platform, good Docker SDK, easy concurrency for live streams | New language in the repo; needs a build/release pipeline |
| **Python + Textual** | Fast to build, rich widgets, `docker` SDK | Python runtime + dependencies on the host (or run it as a container) |
| **Rust + Ratatui** | Single binary, efficient | Slowest to build out |
| **Bash + `gum` / `whiptail`** | No new language; good enough for the **`sbd init`** setup | Not viable for a live multi-panel dashboard |
| **Containerized TUI** (any of the above in an image, run with `docker run -it` and the docker socket) | No host install beyond Docker | Needs the socket mount (root-equivalent); TTY handling |

**Recommendation (revised after the cross-platform decision, [14](./14-platform-and-architecture.md)):** build the TUI **into the same Go binary as the CLI**, using Bubble Tea + Lip Gloss.
- `sbd init` is the guided setup; `sbd ui` is the dashboard.
- Bubble Tea runs in Windows Terminal, macOS Terminal/iTerm and Linux terminals, so one implementation covers every Tier-1 OS.
- The earlier "bash + gum setup first" step is dropped: it would add a bash dependency the rewrite is removing.
- Build `sbd init` first (it needs only the core's render/validate), and the dashboard after the `--json` status and the monitoring profile exist. It reads Prometheus when monitoring is on, and polls node/API directly when it is off (`stacks-status` is deferred, M3).

## Prerequisites in the core (add to the refactor scope)

| Need | Why | Ties to |
|---|---|---|
| **Machine-readable output** for `status`, `config show`, `replay status`, `version` (`--json`) | The TUI must not scrape human text | new |
| Correct exit codes | The TUI must know when an action failed | D04, Q1 |
| Non-interactive mode (`--yes`) | The TUI owns the confirmation UX | D05, Q12 |
| No `.env` mutation at runtime; config rendered by one command | `sbd init` writes `.env` once; the core renders deterministically | D03, Q6 |
| Persisted flags / profile state | The dashboard has to know what "the stack" is. It reads `COMPOSE_PROFILES` from `.env` and the running containers, never a TUI-only file | D14, Q3, Q22 |
| Network-aware status | Shows the right network; supports Q7 if multi-instance arrives | D22 |
| Streaming progress events for long actions (stop with a 1200s timeout, seed, replay) | Progress bars instead of a hang | new |
| Monitoring stack ([12](./12-monitoring-plan.md)) | Richest data source; the TUI degrades gracefully without it | M2 |

## Acceptance criteria

1. `init` on a clean checkout produces a `.env` and configs that pass `start` with no manual edits, on mainnet and testnet.
2. `init` refuses default credentials for exposed services unless the user explicitly overrides.
3. The dashboard refreshes at least once per second for container stats, and every 5–10 s for chain data, using under 50 MB RAM and under 2% CPU itself.
4. Every TUI action produces the same docker command sequence as the CLI, which in turn matches the documented compose command (check with the harness stub).
5. Works over SSH in an 80×24 terminal; degrades to a single-column layout.
6. The dashboard shows correct status for a stack started by hand with `docker compose up -d`, and `q` leaves it running.

## Open decisions

| # | Question |
|---|---|
| T1 | **Settled by the owner (2026-10-25):** Same repo, same binary: part of the `sbd` CLI (`sbd init`, `sbd ui`), released and versioned with the compose files it drives. |
| T2 | **Settled by the owner (2026-10-25):** GitHub Releases with signed binaries and checksums for linux/darwin/windows × amd64/arm64, plus a ghcr.io image (also used by the `config` renderer and for no-install runs). Package managers (Homebrew, winget) come later. The language is decided under Q25. |
| T3 | **Settled by the owner (2026-10-25):** `sbd init` and `sbd ui` only. The `manage.sh` shim gains no new commands (it is removed in phase 7). |
| T4 | **Settled by the owner (2026-10-25):** **Actions from the start.** The first dashboard release includes start/stop/restart/pull/logs export alongside status. Each action runs the same core/compose command as the CLI. Destructive actions (stop, reset, import) need a typed confirmation, and a long stop shows progress. **Revised by the owner (2026-11-09):** the dashboard keeps start/stop/restart/pull/logs only; stop and restart ask y/N; reset, seed, db upgrade, bns, migrate, init and `env set` are CLI-only, so no typed confirmation is needed in the first version. |
| T5 | **Settled by the owner (2026-10-25):** Yes, as an optional `sbd init` step: seed from the Hiro archive, seed from a local archive, or sync from genesis. `sbd init` runs the containerized seed job (no host root) with progress, after showing the size (~400 GB mainnet) and the Q24 pin/archive version check. |
| T6 | **Settled by the owner (2026-11-09):** The dashboard runs on the node host (local Docker socket, localhost ports); remote access later. It makes no outbound calls by default: "synced" comes from the age of the latest Stacks block (Run A at tip: 29 s; Nakamoto blocks every few seconds), not from the peers (`/v2/neighbors` reports no heights). A public API comparison is opt-in through `.env` (`SBD_UI_REFERENCE_API`) and allows a 15-block tolerance, since a node is rarely at exactly the public tip. Without monitoring, history is an in-memory ring buffer. |
| T7 | **Settled by the owner (2026-11-09):** Bubble Tea v2 + Lip Gloss v2 (`charm.land/...`; v1 is unmaintained), pinned to releases at least two weeks old; no Bubbles. Built with `bubbletea/v2 v2.1.0` and `lipgloss/v2 v2.0.6`, after the repo moved to Go 1.27 (Bubble Tea v2.0.10+ requires Go 1.26). Docker data comes from the `docker` CLI, not the Docker SDK: `docker compose ps --format json` every 5 s plus a `docker events` stream, one `docker stats --format json` stream, `docker compose logs -f`. Same daemon/context resolution as `sbd start`, no SDK dependency tree, no socket or named-pipe handling. |
| T9 | **Settled by the owner (2026-11-09), revising T8's log pane:** the node and API logs are chatty and rarely useful (Run A, 24 h: 214k lines, 287 WARN, all peer/network noise, and 1 transient ERRO), so the main screen shows **PROBLEMS** and **POX** instead, and logs open full-screen on `l` (followed only while open). PROBLEMS comes from state only, never from log messages, which change between releases: containers (exited, restarting, unhealthy, restarted in the last 15 min), node/API not answering, sync (block age or reference), API lag > 15 blocks (catches failed event delivery), burn block > 2 h, no peers. POX: cycle and phase, next cycle ETA, stacked STX, and signatures on the latest block vs the cycle's signer count (`/v3/stacker_set/<cycle>`, fetched once per cycle); signing weight would need public-key recovery from each signature, so it isn't shown. |
| T8 | **Settled by the owner (2026-11-09):** First version: services (state, uptime, health, restarts, CPU/mem/IO), chain (tip and block age, burn height and age, API tip and lag, peers, optional "N behind"), host (data-dir free space, load, memory), one log pane (`l` cycles services, WARN+ toggle, `e` exports), whole-stack start/stop/restart/pull, sparklines (node CPU, tip height) from an in-memory buffer (1 h at 5 s) even when monitoring is on, 80×24 with a single-column fallback; without a terminal it exits and points to `sbd status --json`. Fixed refresh (1 s stats, 5 s chain), no `--refresh`. Later: Prometheus history, signer panel, replay status, per-service restart, remote access, alerts panel, mouse. |
