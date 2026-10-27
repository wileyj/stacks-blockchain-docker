# 14 — Cross-Platform Architecture (full refactor)

## Inputs

- **Design goal (repo owner):** runnable on as many operating systems as possible (Linux, other Unix, macOS, Windows). Bash is not a requirement. `manage.sh` is to be **fully refactored**, not patched.
- **Owner requirement (2026-10-19): the controller CLI and the TUI are optional.** A user must be able to run the stack with plain `docker compose` commands if they choose. See §2.1 and §11.
- **[stx-labs/stacks-blockchain-docker#153](https://github.com/stx-labs/stacks-blockchain-docker/issues/153)**, *"Make stacks-blockchain-docker the default way to self-host a node + API"*:
  - Goal: make this repo the "no-brainer option for anyone hosting a Stacks node and API".
  - Known problem: "known issues (e.g., the **hardcoded API dependency**) make it brittle for anything but the default path".
  - Plan: stress-test it internally and with users under real workloads.
  - Then "add first-class observability: evaluate shipping it with **built-in Grafana** and **lightweight companion containers for node status and health checks**".

## 1. Why the current design can't reach the goal

Host-side portability problems found in this analysis (details in [09](./09-defects-and-risks.md)):

| Host dependency today | Breaks on | Ref |
|---|---|---|
| bash, aliases, `read -n`, `[[ ]]`, arrays | Windows (native), minimal Unix images without bash | — |
| GNU sed (`s///m`) | macOS, BSDs | D02 |
| GNU coreutils / gawk (`numfmt`, `sha256sum`, `IGNORECASE`), `tar` + zstd, `aria2c`/`curl` | macOS, Windows, BSDs | D18 |
| `sudo`/root, `chown`, `who am i`, `id -u` | Windows; rootless Docker setups | D11, D17 |
| `uname -m` arm64 heuristic | Windows; also the wrong signal (see §5) | D39 |
| Absolute `${SCRIPTPATH}` bind paths built by the script (24 occurrences in compose) | Windows path syntax, paths with spaces | D29 |
| No `.gitattributes`; every file is LF today | Windows checkout with `core.autocrlf=true` → CRLF in `.env`, scripts, TOML | D47 |
| Bind mounts for chainstate and DB | macOS/Windows Docker Desktop file-sharing I/O (the real cause of the "M1 not recommended" warning) | D39, §5 |
| Host `docker logs \| grep` heuristics, `eval` strings | Quoting differences across shells | D29, D38 |

The only dependency that is actually common to every target OS is **Docker Engine/Desktop with Compose v2**. The architecture should rely on that and nothing else on the host.

## 2. Guiding principle: **compose-first, thin cross-platform CLI, work in containers**

1. **Put as much behavior as possible into Compose itself.** It runs identically on every OS. Verified on Compose v2.39 **[V]**:
   - **Ordered stop** → `stop_grace_period: 1200s` on the node, plus `depends_on` chains (`down` stops dependents first: node → API → postgres). This replaces `ordered_stop` and `STACKS_SHUTDOWN_TIMEOUT` plumbing.
   - **Optional API (fixes the #153 "hardcoded API dependency")** → `depends_on: { stacks-blockchain-api: { condition: service_healthy, required: false } }`. Without `required: false`, a dependency on a service in an inactive profile makes the whole project invalid **[V]**.
   - **Startup ordering and health** → `healthcheck:` on every service + `condition: service_healthy`. This also covers the "health check" companion from #153.
   - **Config rendering** → `configs: { x: { content: | … ${VAR} … } }` renders TOML from env inside Compose, with no host `sed` **[V]**. Limits: no conditionals (so it can't add or remove the signer/API observer blocks) and **no escaping** (a `"` in a value still corrupts TOML) **[V]**. It is fine for simple files (nginx, Prometheus). `Config.toml`/`Signer.toml` are rendered by a **one-shot `config` service inside Compose** (§2.1), not by the CLI, so rendering happens the same way whether or not the CLI is used.
   - **Modes** → profiles: `node`, `api`, `signer`, `proxy`, `monitoring`, plus job profiles `event-replay`, `seed`, `maintenance`. The enabled set is stored in `.env` as **`COMPOSE_PROFILES`**, which Compose reads natively **[V]**. This is also the flag persistence that D14 is missing.
2. **An optional cross-platform CLI binary** replaces `manage.sh` as the *guided* entry point. It validates input, runs preflight checks, edits `.env`, selects profiles, calls `docker compose`, and reports state. It is also the TUI ([13](./13-tui-plan.md)). It adds convenience and safety prompts, but **no behavior that only it can perform** (§2.1).
3. **Move every heavy or OS-specific job into containers**, so the host needs nothing beyond Docker:
   - chainstate seeding (download, checksum, zstd extract, `pg_restore`, DB rename)
   - BNS fetch (already containerized today)
   - Postgres major upgrade (`pg_upgrade` or dump/restore)
   - log export, file ownership fixes

### 2.1 The CLI and TUI are optional (owner requirement)

Plain `docker compose` is a **first-class, supported way to run the stack**, not an "advanced mode". The CLI and the TUI sit on top of it. Design rules that follow from this:

| # | Rule | Consequence |
|---|---|---|
| R1 | **The repo is a working Compose project on its own.** A root `compose.yaml` with an explicit top-level `name:` means `cp sample.env .env && docker compose up -d` from the repo root starts the default `node+api` stack | No `-f` file juggling required. Network overlays, if still needed, are selected with `COMPOSE_FILE` in `.env`, which Compose also reads natively |
| R2 | **All state lives in files a human can edit**: `.env` (incl. `NETWORK`, `COMPOSE_PROFILES`, versions, ports, data paths) and `conf/<net>/*.overrides.toml`. The CLI keeps **no private state file** | A stack started by hand can be managed by the CLI and vice versa. Supersedes the "state file" idea for D14/Q3 |
| R3 | **Anything that must happen for correctness runs inside Compose**, not in the CLI: config rendering, ordering, health gating, graceful stop, the safety checks in jobs | The behavioral contract ([11 §A](./11-refactor-guidance.md)) holds with or without the CLI |
| R4 | **Every CLI command is a documented compose command.** `sbd plan <cmd>` / `--dry-run` prints the exact argv. The compose-only reference (§11) and the CLI are generated from, or tested against, the same table | Users can graduate from the CLI to compose and back. The harness parity tests check both |
| R5 | **Destructive jobs guard themselves.** `seed`, `reset`, `event-replay` and `db upgrade` refuse to run while the node/API is up, and require `--yes` (or `CONFIRM=yes`) when not on a TTY. The guard lives in the job container, not in the CLI | Running `docker compose run --rm seed` by hand is as safe as `sbd seed` |
| R6 | **What the CLI may add** on top: preflight (Compose version, disk, ports, arch/storage advice), `.env` validation with friendly errors, the wizard, typed confirmations, progress bars, `--json`, `migrate`, the TUI | Skipping the CLI costs convenience, never correctness |
| R7 | **CI proves it:** a compose-only testnet smoke test runs with **no CLI binary present** (`docker compose up -d` → API healthy, node running → `docker compose --profile '*' down`) | Regressions that make the CLI mandatory fail the build |

**How config rendering works without the CLI.** A one-shot `config` service with **no profile** (so it is always part of the project) renders `Config.toml` and `Signer.toml` from the templates, `.env` values and the user's override files, using a real TOML library. It is a small Go program from the same codebase as the CLI, published as an image. It reads the enabled profiles from `${COMPOSE_PROFILES}` to decide which `[[events_observer]]` blocks to emit, and fails with a clear message on invalid values. Services that need config declare `depends_on: { config: { condition: service_completed_successfully } }`. The CLI's `sbd config render` runs this same container, so there is exactly one renderer.

Checked on Compose v2.39 with a live daemon, and on older official binaries back to 2.20.2 ([15](./15-verification-log.md) #1, #2, #12):
- `COMPOSE_PROFILES=node,api,signer` in `.env` activates those profiles and is visible to interpolation (`${COMPOSE_PROFILES}`) for the `config` service **[V]**.
- **Gotcha:** `--profile X` on the command line **replaces** the `.env` profile set rather than adding to it (`--profile seed` → only `config` and `seed`) **[V]**. For one-shot jobs that is fine. For long-running services, the documented way to enable something is to edit `COMPOSE_PROFILES`; the renderer always reads the `.env` value, so observers can't drift from it. `sbd doctor` should warn if the running containers don't match `COMPOSE_PROFILES`.
- **Gotcha:** if `.env` is missing, no profile is active and only `config` starts **[V]**. The `config` job should fail fast with "copy sample.env to .env" so the user isn't left with a silent no-op.
- `docker compose run <svc>` on a service whose profile is inactive works and enables that profile for the run **[V]**. This is how jobs are launched by hand (§11).
- `up -d` **re-runs the exited `config` job on every start**, even when nothing changed **[V]**, so config edits always take effect on the next `up`.
- **Gotcha:** removing a profile from `COMPOSE_PROFILES` does **not** stop its running containers. `up -d`, `up -d --remove-orphans` and even a plain `down` leave them running **[V]**. The documented stop is therefore **`docker compose --profile '*' down`**, which works back to 2.20.2 **[V]**; `sbd stop` runs the same.
- stacks-core's `STACKS_EVENT_OBSERVER` env var exists **[S]**, but it adds an observer with a **fixed 1 s timeout** on top of the TOML ones. It is unsuitable as the API's attach mechanism (today's API observer uses 60 s) and causes double delivery if the TOML observer is also present (D54). Rendering stays the approach.
- The renderer job should finish with `stacks-node check-config` / `stacks-signer check-config` on the result. Both reject unknown fields **[V]**, which would have caught the first half of D50. `check-config` does **not** catch the missing-Epoch-4.0 panic, so CI also needs a short boot.
- **Template source (Q27):** the network part of each template is the upstream stacks-core `main` `sample/conf/` file (`<net>-follower-conf.toml`, or `<net>-signer.toml` when the `signer` profile is on; `signer/mainnet-signer-conf.toml` for the signer, with `network` set per net). It is vendored with its source SHA. The renderer replaces only deployment keys (`working_dir`, BTC peer and optional credentials, observers, auth tokens, binds) and then applies user overrides.

## 3. Language / runtime options for the CLI

| Option | Linux | macOS | Windows | Other Unix | Install friction | Notes |
|---|:-:|:-:|:-:|:-:|---|---|
| **Go (static binary)** | ✔ amd64/arm64 | ✔ amd64/arm64 | ✔ native | ✔ FreeBSD etc. (CLI only; see §5) | download one file, or `go install` | Can shell out to `docker compose` or use the Docker SDK; embeds compose files and templates (`embed`); Bubble Tea for the TUI; easy cross-compile in CI |
| Rust (static binary) | ✔ | ✔ | ✔ | ✔ | same as Go | Equivalent outcome, slower to build, smaller contributor pool |
| Python | ✔ | ✔ (preinstalled versions vary) | ⚠ needs Python | ✔ | runtime + deps; packaging is painful on Windows | Fastest to write; weakest at "no-brainer install" |
| Node.js | ✔ | ✔ | ✔ | ✔ | runtime | Same friction as Python |
| POSIX `sh` + PowerShell (two scripts) | ✔ | ✔ | ✔ | ✔ | none | Two codebases that drift; still needs host tools |
| Bash (status quo, cleaned up) | ✔ | ⚠ bash 3.2 + BSD tools | ⚠ WSL/Git Bash only | ⚠ | none | Cannot meet the Windows goal natively |
| Containerized CLI (`docker run … ghcr.io/…/sbd-cli`) | ✔ | ✔ | ✔ | wherever Docker runs | Docker only | Needs the docker socket mounted (npipe on Windows) and host path translation for bind mounts. A useful **secondary** distribution |
| Compose-only (no CLI) | ✔ | ✔ | ✔ | ✔ | none | **Always supported (§2.1).** Rendering and safety checks live in containers, so it loses only the guided UX, preflight and friendly validation |

### Go vs Rust (owner question, 2026-10-25)

| Job | Go | Rust |
|---|---|---|
| Drive `docker compose` (shell out, Q17) | Trivial | Trivial |
| Container status/stats/logs | Official Docker SDK | `bollard` (community, async) |
| Load the compose model (profiles, plan/dry-run) | `compose-go`, the loader `docker compose` itself uses | No equivalent; parse `docker compose config --format json` |
| TOML render/merge | `go-toml` (fine for template rendering) | `toml_edit` (keeps comments and formatting) |
| Seed: hashing, zstd/xz streaming | SHA-256 at 2.7 GB/s on arm64, 1.5 GB/s on x86 SHA-NI [V]; zstd fast in pure Go; xz pure Go is slower | Equally fast hashing; C zstd/xz bindings are fastest, at the cost of C dependencies |
| TUI | Bubble Tea | Ratatui |
| Cross-compile (linux/darwin/windows × amd64/arm64) | `GOOS/GOARCH`, no extra toolchain | Native runners or `cross`/zig for macOS/Windows |
| Fit | Compose and Docker are written in Go | stacks-core and the signer are Rust |

Performance does not decide it (the work is I/O-bound and in Docker). **Owner decision: Go** (Q15/Q25).

### Recommendation (settled: Go)

**Go**, distributed as:
1. Release binaries for linux/darwin/windows × amd64/arm64 (plus freebsd for CLI-only use).
2. A container image (`ghcr.io/stx-labs/…`) for no-install use.
3. **Nothing at all:** the repo works with plain `docker compose` (§2.1, §11). The binary is a convenience layer and is never required.

Reasons:
- one codebase on every OS
- no runtime on the host
- the same binary hosts the TUI
- the Docker ecosystem (Compose itself is Go) gives you the libraries
- static typing and real tests replace a 1,150-line shell script

**Docker interaction:** prefer **shelling out to `docker compose`** with an argv array, which avoids reimplementing Compose and keeps behavior identical to what users can run by hand. Use the Docker Engine API (Go SDK) only for read-only state: container status, stats and logs for the dashboard. This also keeps a **`--dry-run` / `plan` mode** trivial (print the exact compose argv), and that becomes the new characterization-test interface (§7).

## 4. Function-by-function migration map

Each current `manage.sh` function and where it lives after the refactor:

| Current | New home | Notes |
|---|---|---|
| `.env` auto-create, `source .env` | CLI: load with a dotenv parser **compatible with Compose's**; never re-read mid-run | Fixes D03, D41 |
| `usage`, arg loop, `check_flags` | CLI command framework (e.g. cobra): `sbd start --network mainnet --with signer,proxy` | Proper exit codes (D04), no IFS leaks (D13), order-independent (D23) |
| `confirm` | CLI prompt with `--yes`, TTY detection | D05 |
| `check_device` (arm64) | Replaced by a storage-mode check (§5) | D39 |
| `check_network`, `status` | CLI → `docker compose ps --format json` scoped by project name | Network-aware (D22); `--json` output for the TUI |
| `check_event_replay` | API replay container with a defined exit status; CLI checks the container state/exit code instead of grepping logs | D38 |
| `check_container` | Docker API | — |
| `check_bns`, `download_bns_data`, `setup-bns.sh` | `bns` one-shot container (or drop: Q8/D45) | D10–D12 |
| `bns_import_env`, `events_file_env` | **Deleted.** Per-service `environment:` in compose instead of `env_file: .env` | D03, D06, D08 |
| `update_configs` | One-shot **`config` service** in Compose (Go renderer image, TOML library) → `conf/<net>/Config.toml`, `Signer.toml`; observers chosen from `COMPOSE_PROFILES`; user overrides merged. `sbd config render` runs the same container | D02, D15, D16, D35 |
| `set_flags`, `run_docker` | CLI builds the compose argv array; profiles instead of file juggling where possible | D28, D29 |
| `ordered_stop` | `stop_grace_period` + `depends_on` order **[V]** | D20, D21 |
| `docker_up/down/logs/pull` | CLI → `docker compose up -d / down / logs -f / pull` (identical to the manual commands) | flag persistence (D14) via `COMPOSE_PROFILES` in `.env`, with no private state file (R2) |
| `logs_export` | CLI via the Docker API (all services, including signer/nginx/monitoring) | D44 |
| `reset_data` | `reset` job in the `maintenance` profile (mount-safe emptying, refuses while running, needs `--yes`); the CLI wraps it with a typed confirmation. Named-volume users can also use `docker compose down -v` | D22, D48 |
| `event_replay` | `event-import` / `event-export` job services in the `event-replay` profile, each with its own guard (node stopped, file present) and a network-correct path; the CLI only sequences stop → job → start | D09, D37 |
| `seed-chainstate.sh` | **`seed` job service** (`docker compose run --rm seed`, or `sbd seed`); running check *before* any delete, inside the job; confirmation via TTY or `--yes` | D01, D17–D19, D42, D43 |
| `postgres-upgrade.sh` | `pg-upgrade` job service (`pgautoupgrade`-style, or dump/restore), run by hand or via `sbd db upgrade` | D26 |
| `postgres-initdb.sh` | keep (runs in-container already); quote identifiers | — |

## 5. Platform specifics to design for

| Topic | Decision needed / recommendation |
|---|---|
| **Storage backend** (Q16, settled) | **Bind mounts by default on every OS**, because Linux servers are the primary target. The location is configurable: a data root (default `persistent-data/<net>`) plus per-component overrides (`STACKS_DATA_DIR`, `POSTGRES_DATA_DIR`, `SIGNER_DATA_DIR`), so a dedicated mount on a separate disk works by design. **Named volumes are opt-in** for macOS/Windows users who want Docker Desktop I/O speed. They live inside the Docker VM disk image, so preflight checks that it can hold about 400 GB for mainnet. `seed`, `reset` and the load-local-archive job run in containers and work against either |
| Paths | Compose files use **relative paths** resolved from `--project-directory`; the CLI never builds absolute host paths by string concatenation |
| Windows | Docker Desktop (WSL2 backend). If bind mounts are used, data should live in the WSL filesystem; host NTFS paths through `/mnt/c` are very slow. Ship `.gitattributes` (`* text=auto eol=lf`; `*.ps1 eol=crlf` if any) so `.env`, TOML and scripts keep LF |
| Ownership | Containers write data as their own uid. No host `chown`/`sudo` in normal flows; with bind mounts on Linux, offer `--fix-permissions` (runs in a container) |
| "Unix" beyond Linux/macOS | FreeBSD/OpenBSD/illumos have no native Docker Engine. Support them as a **CLI client of a remote Docker host** (`DOCKER_HOST=ssh://…`), which the Go CLI and Compose support. Document this as the supported model |
| Podman | Optional stretch: `podman compose` / Docker-compatible socket. Test, don't promise |
| Architectures | All pinned images and all monitoring candidates publish linux/amd64 + linux/arm64 **[V]** ([15](./15-verification-log.md) #4). Re-check in CI whenever a pin changes |
| Minimum Compose version | Raise the documented minimum from 2.2.2 to **2.24.0**. `configs.content` is rejected by 2.20.2 and works on 2.23.3; 2.24.0 fixed config mount targets; `depends_on.required` needs ≥ 2.20.2 **[V]** ([15](./15-verification-log.md) #12). Recommend ≥ 2.27.2 (a `COMPOSE_PROFILES`-from-`.env` fix). CI tests 2.24.7; the CLI preflight enforces it |
| Network ports on Windows/macOS | Same compose port publishing; default binds to `127.0.0.1` for non-public services (D07) |

## 6. Fixing the "hardcoded API dependency" (#153)

Where it is hardwired today **[V]**:
1. `common.yaml`: `stacks-blockchain` `depends_on: [stacks-blockchain-api]`. The node can't start without the API.
2. `conf/*/Config.toml.sample`: an always-present `[[events_observer]] endpoint = "stacks-blockchain-api:3700"`. If the API is absent or down, the node **stalls**: `event_dispatcher_blocking` defaults to `true`, and delivery is retried forever **[S]** ([15](./15-verification-log.md) #5a). Real stall durations come from the #153 stress test. Removing the observer is safe, because pending payloads for observers that are no longer configured are dropped at startup **[S]**.
3. `conf/nginx/default.conf`: upstream hard-coded to `stacks-blockchain-api:3999`.
4. Profiles: postgres and the API are bundled into the `stacks-blockchain` profile, so there is no "node only" mode.

Target:
- **Modes:** `node` (stacks-node only), `node+api` (default, the #153 headline use case), `node+api+signer`, `signer` attached to an existing external node, `api` against an external node **[stretch]**.
- **Dependencies:** `depends_on … required: false`, and `service_healthy` where present **[V]**.
- **Observer list rendered from the enabled services:** API observer only when the API is enabled; signer observer only when the signer is enabled. This replaces the comment/uncomment sed.
- **Parameterized endpoints:** API host, port and event port come from config, so an **external** API/DB or node can be used.
- **Health checks** (also the #153 "companion containers for node status and health checks"):

  | Service | Check |
  |---|---|
  | node | `/v2/info` responds |
  | API | `/extended/v1/status` responds |
  | postgres | `pg_isready` |
  | signer | metrics endpoint responds |

  Status is visible in `docker compose ps` (no CLI needed), the CLI, the TUI and Prometheus.

## 7. Testing and verification for a rewrite

| Layer | How |
|---|---|
| Behavioral parity with today | Port the 50 harness scenarios ([harness/](./harness/README.md)) to the new CLI's `--dry-run`/`plan` output (exact compose argv + rendered files). Deliberate differences, such as fixing D03/D04, are listed as **intended deltas** in the PR |
| Compose validity | `docker compose config -q` for every mode × network × storage-backend combination in CI; diff against `reference/compose-resolved/` |
| **Compose-only path (R7)** | On a runner with **no CLI binary**: `cp sample.env .env`, set `NETWORK=testnet`, then run the testnet smoke test (`docker compose up -d` → postgres and API healthy, node running and logging burnchain sync for several minutes without exiting → `docker compose --profile '*' down`). The node's `/v2/info` isn't required, because it stays silent during header sync. Also run each job from §11 by hand (`run --rm seed --dry-run`, `reset` refusing while up) |
| CLI ⇔ compose equivalence | For every CLI command, assert that `sbd plan <cmd>` equals the corresponding row of the §11 table |
| Unit | Config rendering (escaping, overrides, observer selection), dotenv parsing parity with Compose, argument validation |
| OS matrix (CI) | GitHub Actions `ubuntu-latest`, `macos-latest`, `windows-latest`: build, unit tests, `--dry-run` goldens. Linux: live testnet smoke test, as above |
| Stress / soak (#153) | mainnet node+api for days with the monitoring stack on: track sync time, API lag, memory growth, restarts, disk growth. Feed thresholds back into the alert rules in [12](./12-monitoring-plan.md) |

## 8. Compatibility and migration

| Item | Plan |
|---|---|
| `manage.sh` | Keep for one release as a **thin shim** that maps old args to the new CLI (`-n mainnet -a start -f signer` → `sbd start --network mainnet --with signer`) and prints a deprecation notice; then remove |
| Existing installs | `sbd migrate`: read the existing `.env`, `conf/*/Config.toml` (keeping user edits as overrides) and `persistent-data/` layout (bind-mount backend), with no data movement required |
| Container names / network name | Keep (contract §A.19–20 in [11](./11-refactor-guidance.md)) unless Q7 (multi-instance) is chosen; if they change, `migrate` handles it |
| Docs | Two equal paths in the README: **"with plain Docker Compose"** (the §11 table) and **"with the `sbd` helper"**. Each CLI section shows its compose equivalent |

## 9. Seed and data-path redesign ([#152](https://github.com/stx-labs/stacks-blockchain-docker/issues/152), [#132](https://github.com/stx-labs/stacks-blockchain-docker/issues/132))

The maintainer comment on #132 already concludes that `seed-chainstate.sh` "was quite literally a POC" and should be fully rewritten. This section is the spec for that rewrite.

### 9.1 Data directories may be mount points (#152)

**Reported:** with dedicated volumes mounted at `persistent-data/mainnet/{postgres,stacks-blockchain}`, seed fails with `rm: cannot remove …: Device or resource busy`. You cannot `rmdir` a mount point. The reporter works around it with `find <dir> -mindepth 1 -delete`.

**Same flaw elsewhere** **[S]**:
- `manage.sh reset` (`rm -rf persistent-data/<net>`): it deletes the contents, then fails on the mount points, and wrongly suggests re-running with `sudo` (D48).
- `postgres-upgrade.sh` (`rm -rf …/postgres && mkdir`).

**Requirements:**
1. **Never delete a data root; empty it.** Clear *contents* (`-mindepth 1` semantics) of each component dir, and leave the directory or mount point in place.
2. **Per-component data locations are configurable**, not just `persistent-data/<net>/<component>`. For example `STACKS_DATA_DIR`, `POSTGRES_DATA_DIR`, `SIGNER_DATA_DIR` (or named volumes, Q16). Then dedicated disks, ZFS datasets or LVM volumes work by design.
3. **Mount awareness:** detect that a path is a mount point (or a different device from its parent) and show it in `status`/`doctor` output. A destructive op lists exactly what will be emptied and on which device before asking for confirmation.
4. Doing the delete **inside a container** that mounts each data dir makes this behave the same on every OS and removes the need for `sudo` on the host.
5. Deleting happens **after** downloads and verification succeed, and after the running-services check (D01). Extract to a staging dir on the same filesystem, then swap in. A failed seed never leaves the node with no data.

### 9.2 Seed performance (#132)

**Reported:** `sha256sum` on the multi-hundred-GB archives is slow. The suggestion `openssl dgst -sha256 -parallel 8` exits 1 (maintainer tested it). That fits the underlying fact: **a single SHA-256 digest over one stream is inherently sequential**; it cannot be split across cores without different published hashes.

Where the time actually goes, and what can be done:

| Stage (today, serial) | Improvement | Expected effect |
|---|---|---|
| Download archive, then re-read the whole file to hash it | **Hash while downloading** (tee the stream into SHA-256; Go `io.MultiWriter`) | Removes one full read of each archive (hundreds of GB of I/O) |
| Hash dump, then hash chainstate (sequential) | **Hash both archives concurrently** (one goroutine/process per file) | Wall time ≈ the larger file instead of the sum |
| Hash implementation | Use an implementation with SHA-NI / ARMv8 crypto extensions (Go's `crypto/sha256` uses them automatically; coreutils builds vary) | **Up to 8×** **[V]**: busybox 338 MB/s vs coreutils, OpenSSL or Go at 2.7–2.9 GB/s on arm64 ([15](./15-verification-log.md) #9). On x86 (i9-13900H, SHA-NI) Go/coreutils/OpenSSL run at about 1.5 GB/s and busybox at 184 MB/s. On a host with slow storage the extra disk read dominates. On fast NVMe (4.7 GB/s on the Linux test host) the hash itself is the bottleneck: about 4 min for 380 GB. Hash-while-download covers both cases |
| Download, then decompress, then extract (separate passes) | **Stream pipeline:** download → (hash ∥ `zstd -d -T0` → `tar -x` into staging). Commit staging only if the hash matches at EOF | Overlaps network, CPU and disk; one pass over the data |
| `zstd` single-threaded decode via `tar` | `zstd -T0` (multi-threaded) explicitly | Faster extraction |
| `pg_restore` single job | **`pg_restore -j <cores>`** (works with custom-format dumps; the `.dump` **is** custom format, magic `PGDMP` **[V]**) | Likely the largest single win on the DB side |
| Downloads (`aria2c -x10` optional) | Built-in parallel ranged HTTP download with resume (no aria2 dependency); keep partials resumable instead of "file exists = done" | Fixes D18's partial-file bug; portable |
| Archive naming (D52) | The archive keeps 30 days of dated, versioned files **[V]**. Choose the newest **same-date pair** D with `<net>-stacks-blockchain-<core>-D.tar.zst` and `stacks-blockchain-api-pg-<pg>-<api>-D.dump`, whose versions are compatible with the pins (same major, ≤ pin; dump pg major ≤ server). Discover dates from the HTML listing, or probe backwards with 1-byte ranged GETs. Verify each file against its own dated `.sha256`. Never download `-latest` (a per-archive symlink). Warn when the versions are older than the pins; exit 1 on a major change, newer versions or no pair, naming the reason and the latest snapshot (Q24, re-settled 2026-10-26). Get sizes from `Content-Range`, because HEAD is 403 (D53) | Seed works again; chainstate and API DB always come from the same snapshot; failures say what to change |
| True parallel hashing of one archive | Needs upstream (Hiro archive) to publish **per-chunk hashes** or a tree hash (e.g. BLAKE3) | Raise with the archive maintainers; optional |

**Verification semantics to preserve:** the archive's published `.sha256` must match before any data is committed. The streaming approach keeps that guarantee because extraction goes to staging, and staging is only swapped in after the final digest matches.

### 9.3 Seed as a containerized job

**Load-local-archive mode (Q16):** `docker compose run --rm -v /path/to/archive.tar.xz:/in/archive:ro seed --from /in/archive [--sha256 <hex>]`, or `sbd seed --from <file>`. It streams a local tar.xz/zst/gz (several hundred GB) into staging on the target filesystem, optionally verifies a digest, and swaps it in. The same guards apply. There is no copy of the archive, but staging needs room for the extracted data.

`docker compose run --rm seed [--yes] [--jobs N]` (or `sbd seed --network mainnet …`, which runs exactly that) starts a `seed` container (curl/HTTP client, zstd, tar, postgres client tools matching `POSTGRES_VERSION`) that:
1. refuses to run if services are up. It can't see the docker socket, so it combines (a) TCP probes of postgres:5432 and the node's P2P/RPC ports over the compose network, and (b) `postmaster.pid` in the postgres data dir. The node holds **no lock file**, and its RPC is silent during the initial header sync **[V]** ([15](./15-verification-log.md) #5f, #6), so a probe alone isn't enough. That's why §11 always runs `down` first and the CLI also checks container state;
2. downloads with hash-while-download;
3. extracts to staging;
4. restores with `pg_restore -j`, then renames the DB/schema (handling any `PG_USER`, D19);
5. empties the target dirs (mount-safe);
6. swaps staging in;
7. prints human-readable progress by default, plus structured events (`--progress=json`) for the CLI/TUI.

No host root, coreutils, zstd or aria2 is needed.

## 10. Open decisions (added to [11](./11-refactor-guidance.md) as Q15–Q19, Q22)

| # | Question | Recommendation |
|---|---|---|
| Q15 | CLI language | Go |
| Q16 | Default storage backend per OS | Named volumes on macOS/Windows, bind mounts on Linux; configurable |
| Q17 | Shell out to `docker compose` vs embed Compose as a library | Shell out (parity with manual usage; simpler) |
| Q18 | Supported-platform statement | Tier 1: Linux amd64/arm64, macOS arm64/amd64, Windows 11 + Docker Desktop (WSL2). Tier 2: other Unix as a CLI client of a remote Docker host. Podman: best effort |
| Q19 | Default mode | `node+api` (the #153 headline), with node-only and external-API modes supported |
| Q22 | CLI/TUI optional? | **Settled by the owner: yes.** Plain `docker compose` is first-class (§2.1, rules R1–R7; command reference §11) |

## 11. Compose-only operation reference

Target commands for running everything **without** the CLI, run from the repo root. The CLI column shows the wrapper that runs the same thing. Names are proposals.

| Task | Plain Docker Compose | `sbd` equivalent |
|---|---|---|
| First setup | `cp sample.env .env`, then edit `NETWORK`, `COMPOSE_PROFILES`, credentials | `sbd init` (wizard) |
| Choose services | edit `COMPOSE_PROFILES` in `.env`, e.g. `node,api,signer,monitoring` | `sbd start --with signer,monitoring` (writes `.env`) |
| Start | `docker compose up -d` | `sbd start` |
| Stop (graceful, node first) | `docker compose --profile '*' down` (uses `stop_grace_period`; **don't pass `-t`**, which overrides the 1200s node timeout). `--profile '*'` also stops services that were removed from `COMPOSE_PROFILES` | `sbd stop` |
| Restart | `docker compose --profile '*' down && docker compose up -d` | `sbd restart` |
| Status / health | `docker compose ps` | `sbd status [--json]` |
| Logs | `docker compose logs -f [svc]` | `sbd logs [svc]` |
| Export logs | `docker compose logs --no-color --timestamps <svc> > exported-logs/<svc>.log` | `sbd logs export` |
| Upgrade images | edit `*_VERSION` in `.env`, then `docker compose pull && docker compose up -d` | `sbd pull` / `sbd upgrade` |
| Re-render config after edits | `docker compose up -d` (the `config` job re-runs on every `up` **[V]**) | `sbd config render` |
| Seed from the Hiro archive | `docker compose down && docker compose run --rm seed` | `sbd seed` |
| Reset data for this network | `docker compose down && docker compose run --rm reset --yes` | `sbd reset` |
| Event export / import | `docker compose stop stacks-blockchain stacks-blockchain-api && docker compose run --rm event-export` (or `event-import`), then `docker compose up -d` | `sbd export` / `sbd import` |
| Postgres major upgrade | `docker compose down && docker compose run --rm pg-upgrade` | `sbd db upgrade` |
| Monitoring | add `monitoring` to `COMPOSE_PROFILES`, `docker compose up -d`; Grafana on `127.0.0.1:${GRAFANA_PORT}` | `sbd start --with monitoring` |
| Different network | change `NETWORK` in `.env` (single instance per host, as today), or keep one `.env` per network: `docker compose --env-file testnet.env up -d` | `sbd start --network testnet` |
| Remote Docker host (Tier 2 Unix) | `DOCKER_HOST=ssh://user@host docker compose up -d` | `sbd --host ssh://user@host start` |

What a compose-only user gives up (R6): preflight checks, the wizard, friendly `.env` validation before anything starts (the `config` job still rejects invalid values, but only at `up`), typed confirmations, progress bars, `--json`, `migrate`, and the TUI.
