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

**Implemented 2026-11-05** (`tools/sbd/init.go`), as below; the original plan is in git history.

| Step | Asks / does | Validation | Writes |
|---|---|---|---|
| Preflight | The `sbd doctor` checks; stops on a failure (Compose, engine, compose project) | — | — |
| Network | mainnet / testnet | re-asked until valid | `NETWORK` |
| Services | API (and postgres), signer, proxy (only with the API), monitoring | — | `COMPOSE_PROFILES` |
| Signer | Private key, masked on a terminal | 64 hex chars, optionally `01`; re-asked | `SIGNER_PRIVATE_KEY` / `TESTNET_SIGNER_PRIVATE_KEY` |
| Auth token | Generated when it is the sample's `1234` or empty | — | `AUTH_TOKEN` |
| Bitcoin | Host and P2P port (defaults from `.env`/`sample.env`) | TCP connect test, a warning only | `BTC_*` / `TBTC_*` |
| Data location | `DATA_ROOT` | the `doctor` disk check for the chosen services | `DATA_ROOT` |
| Proxy | HTTPS?, and the certificate directory | warns if `fullchain.pem`/`privkey.pem` are missing | `PROXY_TLS`, `PROXY_TLS_DIR` |
| Generated secrets | Grafana admin password (monitoring); `PG_PASSWORD` only when no database exists yet | — | `GRAFANA_ADMIN_PASSWORD`, `PG_PASSWORD` |
| Review | Every change, secrets as `<hidden>`; confirm, then write (`.env.save` backup) | — | `.env` |
| Bootstrap and start | seed now / genesis / later; optionally start (`--start`) | the commands' own guards | runs `sbd seed` / `sbd start` |

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
| Services, state, uptime, restarts | Docker Engine API (`/containers/json`, inspect) | same |
| CPU / memory / IO | Prometheus (cAdvisor) | Docker stats stream (`/containers/{id}/stats`) |
| Chain: tip, burn height, peers | node `/v2/info`, `/v2/neighbors`; Prometheus for history | direct HTTP polling |
| API lag | API `/extended/v1/status` vs node tip | same |
| Signer health | signer `/metrics` | hidden |
| Host disk/mem/load | Prometheus (node-exporter) | local `statfs` / `/proc` (Linux) |
| Sparklines / history | Prometheus range queries | in-memory ring buffer since the TUI started |
| Logs | `docker logs --follow` per selected service, with a level filter | same |
| Replay status | core `replay status` (same logic as `check_event_replay`) | same |

Actions call the **core** (start/stop/restart/pull/logs export/reset) and show its progress. Destructive actions (`stop`, `reset`, `import`) need a typed confirmation. The ordered stop's 20-minute node timeout must show as progress, not a frozen screen.

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
| T4 | **Settled by the owner (2026-10-25):** **Actions from the start.** The first dashboard release includes start/stop/restart/pull/logs export alongside status. Each action runs the same core/compose command as the CLI. Destructive actions (stop, reset, import) need a typed confirmation, and a long stop shows progress. |
| T5 | **Settled by the owner (2026-10-25):** Yes, as an optional `sbd init` step: seed from the Hiro archive, seed from a local archive, or sync from genesis. `sbd init` runs the containerized seed job (no host root) with progress, after showing the size (~400 GB mainnet) and the Q24 pin/archive version check. |
