# 11 — Refactor Guidance

This document separates **what must survive** a refactor from **what is up for decision**, and suggests a sequence. It does not prescribe an implementation.

## A. Behavioral contract (preserve unless deliberately changed)

These are the externally visible behaviors that operators, docs or external tooling likely depend on.

### CLI surface
1. `-n/--network`, `-a/--action`, `-f/--flags` (comma list, case-insensitive), `-h/--help`.
2. The action names and aliases: `start|up`, `stop|down`, `restart`, `logs|log`, `logs export`, `pull|upgrade`, `status`, `reset`, `import`, `export`, `bns`.
3. Standalone verbs: `./manage.sh logs`, `status`, `pull`/`upgrade`, `bns`.
4. `status` exit semantics: 0 = running, 1 = not running. Scripts may poll this.
5. Networks and flags are discovered from `compose-files/networks/` and `compose-files/extra-services/`, so dropping a YAML in adds a network or flag.

### Lifecycle safety
6. **Ordered shutdown** on mainnet/testnet: node first, with `STACKS_SHUTDOWN_TIMEOUT` (default 1200s), then API, then postgres, then `down`. This guards against corrupt chainstate.
7. ~~**Replay guard:** refuse start/stop while an API import/export is running.~~ **Retired 2026-10-28 (Q28):** event replay was dropped.
8. **Refuse `start` if already running; refuse `reset`/`bns` while running.**
9. **`reset` asks for confirmation** and deletes only `persistent-data/<network>`.
10. The signer start requires a non-empty private key and fails before anything is written.

### Configuration semantics
11. `.env` is auto-created from `sample.env`; a `.env.save` backup is taken on start.
12. `Config.toml`/`Signer.toml` are created from `*.sample` when missing, and user edits to other keys are preserved.
13. Signer on ⇔ the signer `[[events_observer]]` block **and** `stacker = true` are active in `Config.toml`.
14. The env→TOML key mapping in [06](./06-configuration-reference.md) (burnchain host/ports/creds, `auth_token` = signer `auth_password`, signer endpoint port, metrics ports, signer `node_host`, private key per network).
15. Testnet uses the `TBTC_*` and `TESTNET_SIGNER_PRIVATE_KEY` variables.
16. Chain IDs: mainnet `0x00000001`, testnet `0x80000000`, delivered to the API as `STACKS_CHAIN_ID`. (Mocknet `2147483648` is **dropped**, Q4/Q23.)

### Layout and naming
17. Host paths (the defaults to preserve; the rewrite makes per-component locations configurable and mount-safe, Q20 / [#152](https://github.com/stx-labs/stacks-blockchain-docker/issues/152)):
    - `persistent-data/<net>/{stacks-blockchain,postgres,stacks-signer}` (`event-replay` dropped, Q28)
    - `persistent-data/bns-data`
    - `exported-logs/<svc>.log`
    - `conf/<net>/…`
18. In-container paths: `/src/stacks-blockchain/Config.toml`, `/root/stacks-blockchain/data`, `/src/stacks-signer/Signer.toml`, `/root/stacks-signer/data/signer.sqlite`, `/var/lib/postgresql/data`, `/tmp/event-replay`, `/bns-data`.
19. Container names (`stacks-blockchain`, `stacks-blockchain-api`, `postgres`, `stacks-signer`, `nginx`). External monitoring and `docker logs` habits depend on them. If they change, document it as a breaking change.
20. Docker network name from `DOCKER_NETWORK`.
21. Profiles: `stacks-blockchain` (normal), `event-replay` (postgres + API only), `bns`. *(Now `node`/`api`/`signer`/`proxy`/`monitoring` plus the `bns` and `seed` jobs; `event-replay` dropped, Q28.)*

### Helper workflows
22. Seed: the Hiro archive URL scheme, checksum verification, chainstate extraction, `pg_restore --create`, then rename DB/schema to `PG_DATABASE`/`PG_SCHEMA`, and finally chown to the invoking user.
23. ~~Event replay: `export-events --file`, `import-events --file --wipe-db --force`, API restart policy `no` during replay.~~ **Retired 2026-10-28 (Q28):** archive snapshots (the seed job) replace event replay.
24. nginx `/status` → `ok` and the `/` proxy with WebSocket upgrade.

### Entry points (new, owner requirement 2026-10-19)
25. **The CLI and the TUI are optional.** Every item above that isn't CLI syntax (6–24) must also hold when the stack is run with plain `docker compose` from the repo root, with no helper binary installed. Rendering, ordering, health gating and the safety guards therefore live in Compose and in job containers, not in the CLI. Rules R1–R7 and the command reference are in [14 §2.1 and §11](./14-platform-and-architecture.md).

## B. Decisions needed before or while refactoring

| # | Question | Context | Options |
|---|---|---|---|
| Q1 | Exit codes on errors? | D04 | **Settled by the owner (2026-10-19):** Non-zero exit codes on errors: usage/validation errors exit 2, operational failures exit 1, `status` keeps 0 = running / 1 = not running. Changelog note; the `manage.sh` shim maps the same way. |
| Q2 | Support macOS as a host? | D02, D18, D39 | **Settled by the owner: yes, plus Windows and other Unix.** See [14](./14-platform-and-architecture.md): no host GNU tools; render in the CLI |
| Q3 | Persist the chosen flags? | D14 | **Resolved by Q22:** persist as `COMPOSE_PROFILES` in `.env`, which Compose reads natively, so manual and CLI use share one source of truth. No private `.state` file |
| Q4 | How to keep mocknet vars away from the API? | D03, D08, D37 | **Settled by the owner (2026-10-19):** **Mocknet is disabled; only mainnet and testnet are supported.** Network differences reach containers through per-service Compose `environment:` with interpolated values; nothing modifies `.env` at runtime. |
| Q5 | Secret handling | D06, D07 | **Settled by the owner (2026-10-19):** Per-service `environment:` allow-lists (no `env_file: .env`). The signer key reaches only the signer, via rendered `Signer.toml`. Optional ports (postgres, metrics, Prometheus/Grafana) bind 127.0.0.1 by default. Warn on, or refuse, default credentials when a service is exposed. |
| Q6 | Config rendering model | D15, D16, D35 | **Settled by the owner (2026-10-19):** Render from versioned templates on every `up` (the `config` job), merge `conf/<net>/*.overrides.toml` for user edits, escape values, then validate with `stacks-node check-config` / `stacks-signer check-config`. Generated files are not hand-edited. |
| Q7 | Multiple networks per host? | D22 | **Settled by the owner (2026-10-25):** One stack per host, with fixed container names kept (contract §A.19). Status, reset, seed and job guards are network-aware and name the network in every message. |
| Q8 | Legacy features | D30, D45 | **Settled by the owner (2026-10-25):** Remove the API 5.0.1 guard, `check_api` and the `private-testnet` branch. Keep BNS v1 as an optional `bns` job (useful only for a mainnet genesis sync), off by default and with a correct exit status (fixes D10–D12). |
| Q9 | `postgres-upgrade.sh` | D26 | **Settled by the owner (2026-10-25):** A containerized `pg-upgrade` job for any network that detects the source version from `PG_VERSION`. It upgrades into a staging dir and swaps only after success. Run it with `docker compose run --rm pg-upgrade` or `sbd db upgrade`. It replaces `postgres-upgrade.sh`. |
| Q10 | Seed safety | D01, D17, D18, D19 | **Settled by the owner (2026-10-25):** Phase-0 hotfix to the existing `seed-chainstate.sh`: check for running services before any delete, confirm (with `--yes` for automation), empty contents instead of removing mount points, use the archive's `-latest` names and read the real version from `.sha256`, and pass `curl --fail`. The containerized rewrite still follows in phase 3. |
| Q11 | Implementation language | D47 | **Owner: full refactor, bash not required.** See Q15; recommendation: Go single binary ([14 §3](./14-platform-and-architecture.md)). The behavioral contract above applies either way |
| Q12 | Non-interactive mode | D05 | **Settled by the owner (2026-10-25):** Prompts run interactively on a TTY. Without a TTY, a destructive action exits 2 with "re-run with --yes" (`CONFIRM=yes` for compose jobs). Warnings, such as arm64 or storage advice, never block. |
| Q13 | Monitoring stack shape | [12](./12-monitoring-plan.md) M1–M6 | **Settled by the owner (2026-10-25):** Settled via M1–M4 in doc 12: Prometheus; Grafana inside `monitoring` (can be disabled); direct scrape plus rules, no json-exporter; `stacks-status` deferred. |
| Q14 | TUI scope and delivery | [13](./13-tui-plan.md) T1–T5 | **Settled by the owner (2026-10-25):** Settled via T1–T5 in doc 13: same repo and binary (`sbd init`, `sbd ui`), released on GitHub Releases plus ghcr, dashboard actions from the first release, optional seed step in the wizard. |
| Q15 | CLI language | [14 §3](./14-platform-and-architecture.md) | **Settled by the owner (2026-10-25):** **Go**, re-confirmed under Q25. It is used for the `sbd` CLI/TUI (Bubble Tea), the `config` renderer image and the seed/pg-upgrade job images. |
| Q16 | Default storage backend per OS | [14 §5](./14-platform-and-architecture.md), D39 | **Settled by the owner (2026-10-25):** **Bind mounts are the default on every OS** (Linux servers are the primary target). The storage location is **configurable**: a data root plus per-component paths, so a dedicated mount on a separate disk works by design. Named volumes are an opt-in for macOS/Windows, gated by a Docker Desktop disk-size preflight (mainnet ≈ 400 GB). The seed job gains a **load-local-archive** mode (tar.xz/zst/gz, streamed into staging on the target filesystem, verified, then swapped in) for occasional multi-hundred-GB loads. |
| Q17 | Shell out to `docker compose` vs library | [14 §3](./14-platform-and-architecture.md) | **Settled by the owner (2026-10-25):** Shell out to `docker compose` with argv arrays, the exact commands `sbd plan` prints. The Docker Engine API is used only for read-only status, stats and logs. |
| Q18 | Supported-platform tiers | [14 §5](./14-platform-and-architecture.md) | **Settled by the owner (2026-10-25):** Tier 1 (CI-tested): Linux amd64/arm64, which is primary and includes the live smoke test; macOS arm64; Windows 11 + Docker Desktop/WSL2 (build and dry-run tests). Tier 2 (documented): other Unix as a client of a remote Docker host. Podman is best effort. |
| Q19 | Default mode / API coupling | D46, [#153](https://github.com/stx-labs/stacks-blockchain-docker/issues/153) | **Settled by the owner (2026-10-25):** `COMPOSE_PROFILES=node,api` by default. Also supported: node-only (no API observer rendered at all) and an external API/DB with configurable endpoints. The docs state that any configured API is a liveness dependency of the node, because event delivery blocks by default. |
| Q20 | Data locations | D48, [#152](https://github.com/stx-labs/stacks-blockchain-docker/issues/152) | **Settled by the owner (2026-10-25):** Settled together with Q16. Per-component data paths are configurable (e.g. `STACKS_DATA_DIR`, `POSTGRES_DATA_DIR`, `SIGNER_DATA_DIR` under a configurable data root). Destructive operations empty a directory's contents and never remove a mount point (#152). |
| Q21 | Seed rewrite scope | D49, [#132](https://github.com/stx-labs/stacks-blockchain-docker/issues/132) | **Settled by the owner (2026-10-25):** The full spec in 14 §9: hash-while-download with both archives fetched concurrently; resumable ranged downloads; streamed zstd/xz extraction into staging; `pg_restore -j`; mount-safe swap; load-local-archive mode; progress output. Sources: the Hiro archive or a local file. |
| Q23 | Local dev/CI network now that mocknet is broken (D51) | [15](./15-verification-log.md) #5b | **Settled by the owner (2026-10-19):** Resolved by Q4: mocknet is dropped (D51 closes with it). The CI smoke test is `check-config` on every rendered config plus a short testnet follower boot (container up, healthcheck reaches burnchain sync), with no full sync. |
| Q24 | Image pins vs the seed archive (D52) | [15](./15-verification-log.md) #8b, [09 D52](./09-defects-and-risks.md) | **Re-settled by the owner (2026-10-26):** **The snapshot date is what matters. Versions must only be compatible, within the same major.** Seed downloads a **snapshot pair**, `<net>-stacks-blockchain-<core>-<D>.tar.zst` **and** `stacks-blockchain-api-pg-<pg>-<api>-<D>.dump`, with **the same date D for both**. It verifies each file against its own dated `.sha256`. **Dates are never mixed** (e.g. today's chainstate with yesterday's dump). `-latest` is a symlink to the newest snapshot of *each* archive separately, so it is never downloaded; its `.sha256` lines are only read to report what is available. **Selection:** the newest D in the 30-day window where both files exist and the versions are compatible with the pins: - archive core: same major as `STACKS_BLOCKCHAIN_VERSION` and ≤ it; - archive API: same major as `STACKS_BLOCKCHAIN_API_VERSION` and ≤ it; - dump Postgres major ≤ `POSTGRES_VERSION`. An **exact match is preferred only when it is the same date**; the newest date always wins. When the archive versions are older than the pins, seed prints a **warning**, not an error: the node and API migrate the data on first start (owner **[O]** 2026-10-26: a known-good path, no restore test needed). **Refused (exit 1):** - a different major (hard fork, e.g. stacks-node 5, or another breaking change); - archive versions newer than the pins (an older binary can't be relied on to read a newer schema). **Confirmed by the owner 2026-10-26 [O].** Newer pairs are never selected. Whenever the window holds pairs newer than the pins, seed prints a **warning**, because the user may simply have older pins. It names the newest snapshot (date plus core/API/Postgres) and suggests bumping the pins, then continues with the newest pair ≤ pins; it exits 1 only if no such pair exists; - no same-date pair at all. The error names the reason, the latest snapshot (date plus core/API/Postgres) and the newest available pairs. For example: `seed: no compatible mainnet snapshot for core 5.0.0 / API 9.7.0 / pg17 (archive is core 4.x). Latest snapshot 20261026: core 4.0.4, API 9.7.0, pg17.` **Per-network pins are not needed:** one network per install (Q7). Testnet with the default pins 4.0.4/9.7.0 picks today's 4.0.1/9.0.2 pair with a warning. The same rules apply to the load-local-archive mode (Q16/Q21), using the versions and date in the file names. Checked 2026-10-26: mainnet 4.0.4/9.7.0/pg17 → 20261026 (exact). Mainnet 4.0.4/9.6.0 pins → 20261023 (the newest pair ≤ pins; 9.7.0 is newer, so refused). Testnet 4.0.4/9.7.0 → 20261026 (4.0.1/9.0.2, warning). Mainnet 9.6.0 pairs skip 20261016 and 20261018, where one of the two files is missing; that is why pairing is required. *(Superseded 2026-10-25 wording: "refuse on mismatch" against the `-latest` `.sha256`; that assumed the archive kept only `-latest`, which was wrong.)* |
| Q25 | CLI language, revisited: Go or Rust? | Owner question 2026-10-19 | **Settled by the owner (2026-10-25):** Go over Rust. Reasons: `compose-go` and the official Docker SDK keep the CLI identical to plain `docker compose` (R4); cross-compiling every Tier-1 target is trivial; easier contributor ramp-up. Rust was judged viable; the comparison is in 14 §3. |
| Q26 | Slow recovery after an API outage (D55) | [09 D55](./09-defects-and-risks.md), [15](./15-verification-log.md) P2 | **Settled by the owner (2026-10-26):** **(a)**: render a smaller API observer `timeout_ms`, and document the choice. A node restart (b) was rejected: when the API is up but failing, e.g. it can't reach Postgres, no container stops or starts, so nothing would trigger a restart, and the node backs off on `Failed POST` (non-200) exactly as on a connection error (`worker.rs:356-366`). The renderer sets `timeout_ms = 60_000` for the API observer on both networks, so the backoff is capped at 180 s. It can be overridden with `API_OBSERVER_TIMEOUT_MS` in `.env`, and the runbook and the monitoring alert text explain the worst-case delay. **Before release:** validate that the API's worst-case `/new_block` handling stays well under 60 s during a mainnet sync, including genesis and the largest blocks, in the phase 5 stress test |
| Q27 | Source of the per-network node/signer config (D50) | [09 D50](./09-defects-and-risks.md), [15](./15-verification-log.md) P4 | **Settled by the owner (2026-10-26):** The network definition comes from upstream **stacks-core `main` `sample/conf/`**: `testnet-follower-conf.toml` / `testnet-signer.toml` for testnet, `mainnet-follower-conf.toml` / `mainnet-signer.toml` for mainnet, and `signer/mainnet-signer-conf.toml` for the signer binary. That covers the bootstrap node, epochs, `[[ustx_balance]]`, `pox_5_*` and mode. The repo layers its deployment settings on top: `working_dir`, BTC `peer_host`/ports and optional RPC credentials, observers (from `COMPOSE_PROFILES`, with the Q26 `timeout_ms`), `auth_token`/`auth_password`, binds and `prometheus_bind`; then the user's overrides file. **Recommended mechanism:** vendor the files into the repo, e.g. `conf/upstream/<file>` with the source commit SHA in a header, using a sync script, run by hand. **Implemented 2026-10-27 (tier 0):** `conf/upstream/` (verbatim upstream follower samples + `SOURCE` commit), `conf/<net>/Config.deployment.toml` (repo layer), generated `Config.toml.sample` (`config template`), `tools/sync-upstream-conf.sh [--check]`, and the read-only scheduled `upstream-drift.yaml`. *(Owner, 2026-10-27: a scheduled CI job that opens PRs is **deferred**; by default no CI job gets write/create-branch permission. A read-only CI check that only reports drift from upstream is acceptable.)* Don't fetch at runtime: that breaks offline/air-gapped installs and makes the rendered config non-reproducible. **Guard:** `main` can get ahead of the pinned image (a new key or epoch), so CI runs `check-config` **and** a short boot of the rendered config against the pinned image. If `main` is rejected, the sync PR fails and the pins are bumped together with the samples. Checked 2026-10-26: `main` equals the 4.0.4 tag for every node sample, and the signer sample differs only by a trailing blank line. |
| Q22 | Must the CLI/TUI be required? | Owner, [14 §2.1](./14-platform-and-architecture.md) | **Settled by the owner: no.** Plain `docker compose` is a first-class path; the CLI/TUI only add convenience (preflight, wizard, confirmations, progress, `--json`) |

## C. Target structure

> **Superseded by [14 — Cross-Platform Architecture](./14-platform-and-architecture.md).** The owner's goal (Linux, Unix, macOS, Windows) and the full-refactor decision rule out a bash-only design. Doc 14 recommends compose-first + a Go CLI + containerized jobs. The bash layout below is kept only as a **fallback**, in case Q15 lands on bash. The mechanical principles (argv arrays instead of `eval`, load `.env` once, scoped options, `--yes`, real exit codes, explicit project name) apply to any language.

### C.1 Fallback: structure if staying in bash

```
manage.sh                 # thin entry: parse args → dispatch table
lib/
  log.sh                  # logger, levels, VERBOSE, correct timestamps
  args.sh                 # option parsing (no global IFS/noglob leaks), validation, exit codes
  env.sh                  # load .env once (explicit export list), no re-sourcing
  compose.sh              # build compose argv as an ARRAY (no eval); file set per action/profile
  state.sh                # is_running(network), replay_status(), container checks
  config.sh               # render Config.toml / Signer.toml (template → file), signer toggle
  actions/{start,stop,restart,logs,pull,status,reset,replay,bns}.sh
scripts/                  # helpers, sharing lib/log.sh and lib/env.sh
```

Key mechanical changes that make most of the defects go away:
- Build compose commands as bash **arrays** and call them directly (fixes D28, D29).
- Load `.env` **once**; never re-source it mid-run (fixes D03).
- Keep options scoped: no global `IFS`/`set -f` (fixes D13).
- Make `confirm` honor `--yes` and fail on non-TTY (fixes D05).
- Make `usage` take an exit code (fixes D04).
- Pass `profile` explicitly to `set_flags` (removes the dynamic-scope coupling).
- Pass `--project-name` explicitly, even if it stays `compose-files`, so the name no longer depends on a directory name.

## D. Suggested sequencing

### D.1 Full-rewrite sequencing (current recommendation)

| Phase | Goal | Items |
|---|---|---|
| 0 | Safety net on the current code | CI: harness `--check`, `shellcheck`, `docker compose config` for every combination. **Hotfix D01 + D48 + D52** in the existing seed (Q10): running check before delete, `--yes` confirmation, empty contents instead of removing roots, `-latest` archive names with the version read from `.sha256`, and `curl --fail`. D01 and D48 destroy data today, and D52 makes seed unusable with the default pins |
| 1 | Compose-first restructure (**usable with no CLI at the end of this phase**) | Root `compose.yaml` with explicit `name:`; `COMPOSE_PROFILES` in `sample.env`; healthchecks; `stop_grace_period`; `depends_on … required: false`; profiles `node`/`api`/`signer`/`proxy`/`monitoring` + job profiles; the one-shot **`config` renderer service** (first piece of the Go codebase, shipped as an image); per-service `environment:` instead of `env_file`; relative paths; `.gitattributes`; configurable data paths/volumes; **remove mocknet** (`networks/mocknet.yaml`, `conf/mocknet/`) and fix the testnet sample (D50) (D46, D47, D48, D06, D21, D50, D51). CI: compose-only mocknet smoke test with no CLI (R7). Until phase 2, `manage.sh` can keep working as a thin wrapper over the new compose project |
| 2 | Go CLI core (optional wrapper) | Commands with parity to the contract in §A, each mapping to a documented compose command (R4), `--dry-run`/`plan`, `--json`, `--yes`, real exit codes, preflight, `.env` editing, `migrate`. Assert CLI ⇔ compose equivalence. *(Owner, 2026-10-29: no `manage.sh` shim; it stays unchanged with a deprecation notice until phase 7.)* |
| 2M | **Monitoring (high priority)** | [12](./12-monitoring-plan.md) on top of the phase-1 profiles; built-in Grafana and health/status companion containers per #153 |
| 3 | Containerized jobs | `seed` rewrite (#132/#152: D01, D17–D19, D42, D43, D48, D49), `reset`, `event-import`/`event-export`, `db upgrade` (D26), permission fixes. Each one runs with `docker compose run --rm <job>` and carries its own running-check and `--yes` guard (R5) |
| 4 | Cross-OS release | Release binaries (linux/darwin/windows × amd64/arm64), image on ghcr.io, OS-matrix CI, docs rewrite with **two equal paths**, plain Compose and `sbd` ([10](./10-documentation-drift.md), [14 §11](./14-platform-and-architecture.md)) |
| 5 | Stress / soak (#153) | Long mainnet runs with monitoring; tune alerts, resource defaults and the shutdown timeout |
| 6 | TUI (optional) | `sbd init` wizard (with an optional seed step, T5), then the `sbd ui` dashboard with actions from its first release (T4) ([13](./13-tui-plan.md)); it works against a stack started by hand |
| 7 | Cleanup | Remove the `manage.sh` shim and old scripts; legacy features per Q8 |

### D.2 Incremental (bash-patch) sequencing, kept for reference in case Q15 = bash

| Phase | Goal | Items |
|---|---|---|
| 0 | Safety net | Keep `refactor/harness` passing; add `shellcheck` and `docker compose config` validation for every network/flag combination to CI |
| 1 | Stop the bleeding (no contract change) | D01 (seed ordering + confirm), D03/D08 (no `.env` mutation), D05 (`--yes`, non-TTY), D09 (network-correct path, abort on missing file for import), D02 (preflight for GNU sed, or remove the `m` flag) |
| **1M** | **Monitoring (high priority, additive)** | [12](./12-monitoring-plan.md): `extra-services/monitoring.yaml` with Prometheus, cAdvisor, node-exporter, postgres-exporter, Grafana (M2) and recording rules over the node/API metrics (no json-exporter, M3), localhost binds, no `env_file`. Additive, so it can ship alongside phase 1. Best paired with D14/Q3 (flag persistence) so a restart doesn't drop monitoring |
| 2 | Contract-visible fixes | D04 exit codes, D10 bns completion, D24 idempotent stop, D14 flag persistence (per Q3); **add `--json` output for status/config/replay** (TUI prerequisite) |
| 3 | Config model | Q6 rendering, D15, D16, D35, D33 (port templating also feeds the monitoring scrape targets) |
| 4 | Security posture | Q5, D06, D07, D27 pin images (including the monitoring images) |
| 5 | Cleanup | D30 dead code, Q8 legacy, Q9 upgrade script, [10](./10-documentation-drift.md) docs refresh |
| 6 | TUI (optional) | [13](./13-tui-plan.md): 6a init wizard (bash + gum) once phases 1–3 land; 6b live dashboard (Go/Bubble Tea) on top of the `--json` core and the monitoring data |

## E. Test strategy

1. **Characterization first.** `refactor/harness/run.sh --check` must pass before and after each purely structural change. When a change is *intended* to alter behavior, re-record with `--record` and review the golden diff in the PR. The diff is the behavior-change log.
2. **Compose model snapshots.** Regenerate `reference/compose-resolved/` with `docker compose config` and diff. This catches accidental changes to ports, volumes, env or profiles.
3. **Static checks.** `shellcheck -x manage.sh scripts/*.sh`; `docker compose -f … config -q` for every network × flag × profile combination; **`stacks-node check-config` and `stacks-signer check-config` on every sample against the pinned images** (would have caught D50).
4. **Live smoke test** (CI or manual). Mocknet is dropped (Q4/Q23; it panics on the pinned node, D51), so the smoke test is a testnet smoke test (`docker compose up -d` → postgres and API healthy, node running and logging burnchain sync for several minutes without exiting → `docker compose --profile '*' down`). The node's `/v2/info` isn't required, because it stays silent during header sync. Plus `check-config` on every rendered config. Old form, for the record: `-n mocknet -a start` → poll `localhost:20443/v2/info` and `localhost:3999/extended/v1/status` → `-a stop`. mocknet needs no external data. After the rewrite, run it twice: once with plain `docker compose` and no CLI on the runner, once through `sbd`.
5. **Platform matrix:** Linux (GNU userland) and macOS (BSD userland, arm64) for `manage.sh`. Linux only for seed, unless Q2 says otherwise.
6. **Destructive paths** (`reset`, seed, `import --wipe-db`): run with a fixture `persistent-data/` in a temp dir and assert exactly which paths are removed.

## F. TODOs (owner-requested, deferred)

- **TODO (owner, 2026-10-27): postgres exposure and the network model.** Revisit the optional postgres port (`-f postgres` / `EXPOSE_POSTGRES`, `compose.postgres-port.yaml`): drop it, or move the stack to Docker host networking instead of a project network. Trade-offs are listed in HANDOFF. Decide before the first release.

| # | TODO | Added | Evidence |
|---|---|---|---|
| TODO-1 | **Run the stacks-node container as non-root.** Change the image or compose setup so the node runs as non-root by default. Also check the API and signer images. | 2026-10-26 (owner) | [15](./15-verification-log.md#non-root-stacks-node-2026-10-26-v) |

TODO-1 details:
- **Current state [V]:** `stacks-core:4.0.4` has no `USER`, so it runs as uid 0, and `/root` is mode `0700`. The repo puts the node's data at `/root/stacks-blockchain/data`, so a non-root user can't reach it.
- **What already works [V]:** uid 1000 with `working_dir = "/data"` and a volume owned by uid 1000 boots, syncs headers and serves RPC and metrics. All files are created as 1000:1000. Nothing in the node itself needs root, and every port is above 1024.
- **Needed changes:**
  1. Image: add a `USER` (for example a fixed `stacks` uid/gid) and a neutral data dir (`/data` or `/var/lib/stacks`). Upstream is stacks-core's Dockerfile; otherwise use a thin derived image or just compose `user:`.
  2. Rendered config: `working_dir` and the mount target move off `/root/...` (doc 06; Q6 renderer).
  3. Ownership migration: existing bind-mounted `persistent-data/<net>/stacks-blockchain` is root-owned. Add a one-shot `chown` init job, or a check in the renderer/preflight. A non-root node on a root-owned dir panics: `Failed to create working directory` (config/mod.rs:1157).
  4. Seed job (Q21) and pg-upgrade job: extracted files must end up owned by the node's uid/gid. Mount-safe deletes (Q20) are unaffected.
  5. Configurable `PUID`/`PGID` in `.env` so bind mounts match the host user on Linux. Docker Desktop (macOS/Windows) mostly hides ownership on bind mounts, but named volumes still need the init/chown step.
- **Fits with:** §A hardening, Q5 (127.0.0.1 binds), and Q16 (bind mounts by default).

