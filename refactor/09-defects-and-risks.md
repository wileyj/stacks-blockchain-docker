# 09 — Defects & Risks

**Severity scale:**
- **Critical**: can destroy data or credentials without the operator choosing to
- **High**: a common workflow is broken, or failures go undetected
- **Medium**: wrong behavior in a supported but less common path, or a significant maintenance hazard
- **Low**: cosmetic, latent, or dead code
- **Info**: intentional-looking behavior worth a decision

The verification tags ([V]/[S]/[U]) are defined in the [README](./README.md). The harness scenario that demonstrates a [V] item is named where one exists (`harness/golden/<name>.txt`).

## Summary

| ID | Sev | Area | Title |
|---|---|---|---|
| D01 | Critical | seed | Persistent data deleted before the running-services check, without confirmation |
| D02 | High | portability | `update_configs` requires GNU sed; stock macOS fails every start |
| D03 | High | mocknet | `.env` permanently modified; `NETWORK` reset by re-sourcing `.env` |
| D04 | High | CLI | Every usage/validation error exits 0 |
| D05 | High | CLI | `confirm` loops forever on non-TTY stdin |
| D06 | High | security | Secrets broadcast to every container / mounted into the node |
| D07 | Medium | security | Weak default credentials, all ports bound to 0.0.0.0 |
| D08 | Medium | mocknet | A failed `up` leaves `.env` modified (`set -e` skips the revert) |
| D09 | High | event-replay | Hard-coded mainnet TSV path; a missing file is not fatal before `--wipe-db --force` |
| D10 | Medium | bns | `bns` always exits 1, never reports completion, leaves its container |
| D11 | Low | bns | `setup-bns.sh` overwrites `USER_ID`, so the data is chowned to root |
| D12 | Low | bns | `check_bns` is a no-op (`BNS_FILES` undefined) |
| D13 | Low | CLI | `-f` leaks `IFS=','` and `set -f` for the rest of the run |
| D14 | Medium | lifecycle | Flags not persisted; restart without `-f signer` disables the signer |
| D15 | Medium | config | `Config.toml` never re-synced with updated samples |
| D16 | Medium | config | Port env vars not reflected in TOML/nginx |
| D17 | Medium | scripts | Helper scripts resolve `SCRIPTPATH` to cwd |
| D18 | Medium | seed | Unchecked GNU/Linux-only tool dependencies; partial downloads treated as complete |
| D19 | Medium | seed | Restore assumes `PG_USER=postgres` |
| D20 | Low | lifecycle | `ordered_stop` "not running" branch unreachable |
| D21 | Low | lifecycle | mocknet stop uses the default 10s timeout |
| D22 | Medium | architecture | One stack per host; status/reset checks ignore the network |
| D23 | Low | CLI | Standalone verbs act immediately and skip the chain-ID mapping |
| D24 | Low | CLI | `stop` on a stopped stack exits 1 (not idempotent) |
| D25 | Low | logging | Verbose timestamp uses month (`%m`) as minutes |
| D26 | Medium | scripts | `postgres-upgrade.sh` WIP defects |
| D27 | Medium | supply chain | Unpinned images; inconsistent version fallbacks |
| D28 | Low | code quality | `$(cmd) \|\| {…}` executes command output |
| D29 | Low | code quality | `eval`'d string commands break on paths with spaces |
| D30 | Low | code quality | Dead code and stale guards |
| D31 | Low | UX | Unconditional `grep` output during mocknet toggles |
| D32 | Low | CLI | Signer flag detected by substring |
| D33 | Low | config | `update_configs`/`.env.save` also run for bns and event-replay |
| D34 | Low | config | Testnet RPC vars ineffective |
| D35 | Medium | config | Unescaped values in `sed` replacements |
| D36 | Info | mocknet | mocknet chain/DB are ephemeral; signer DB is not |
| D37 | Low | event-replay | mocknet mounts mainnet's replay dir; docs say N/A but it's allowed |
| D38 | Medium | event-replay | Replay detection depends on API log strings |
| D39 | Low | UX | arm64 prompt wording/coverage |
| D40 | Medium | process | No CI, tests, or shell linting |
| D41 | Low | config | `.env` parsed by both bash and the Compose dotenv parser |
| D42 | Low | scripts | Fixed `sleep 15` waits for Postgres |
| D43 | Low | seed | Minor script defects (`export $CURRENT_USER`, undefined `DEBUG`, `tar -v` noise, password echo) |
| D44 | Low | logs | `logs export` omits the signer and nginx |
| D45 | Info | legacy | API 5.0.1 guard and BNS v1 flow likely obsolete |
| D46 | High | architecture | Hardcoded API dependency: no node-only mode ([#153](https://github.com/stx-labs/stacks-blockchain-docker/issues/153)) |
| D47 | Medium | portability | Bash-only host tooling; no `.gitattributes`; Windows unsupported |
| D48 | High | data | Deleting data roots fails on mount points (seed and reset) ([#152](https://github.com/stx-labs/stacks-blockchain-docker/issues/152)) |
| D49 | Medium | seed | Seed pipeline fully serial; checksum re-reads every archive ([#132](https://github.com/stx-labs/stacks-blockchain-docker/issues/132)) |
| D50 | High | config | `conf/testnet/Config.toml.sample` targets the **pre-reset** Hiro testnet and is rejected or panics on current stacks-core (obsolete keys, missing Epoch 4.0, old bootstrap node) |
| D51 | High | config | mocknet panics at startup on 4.0.1; 4.0.5 removed mocknet mode |
| D52 | High | seed | Seed archive URLs for the pinned versions no longer exist |
| D53 | Low | seed | Archive HEAD returns 403, so the reported download size is always 0 |
| D54 | Info | config | Uncommenting `STACKS_EVENT_OBSERVER` duplicates every event delivery to the API |
| D55 | Medium | config | After an API outage, the node keeps sleeping in backoff (up to 3 × `timeout_ms`, i.e. 15 min with the mainnet sample) before it notices the API is back |

---

## Details

### D01 — Critical — seed deletes data first, checks later **[S]**
- **Where:** `scripts/seed-chainstate.sh:176-179` (`rm -rf persistent-data/$NETWORK`) runs before `check_network` at `:207`. There is no prompt.
- **Impact:** running the seed while a node is running (or on the wrong `NETWORK`, which comes only from `.env`) deletes the live chainstate and DB. Only *then* does it notice the services are running and exit.
- **Note:** the deletion also happens before the downloads, so a failed download leaves the node with no data at all.

### D02 — High — GNU sed required **[V]**
- **Where:** `manage.sh:453-463` and `467-477` use `s/^/#/mg` / `s/^#//mg` and labels inside `{}`.
- **Evidence:** `/usr/bin/sed` on macOS → `bad flag in substitute command: 'm'`; `update_configs` → `log_exit "Unable to update values in Config.toml"`. Non-interactive bash resolves `sed` to `/usr/bin/sed` even when an interactive alias points at gsed.
- **Impact:** `start`, `restart`, `import`, `export` and `bns` fail on stock macOS. `docs/requirements.md` links GNU sed but does not say BSD sed fails.

### D03 — High — mocknet `.env` mutation and `NETWORK` reset **[V]** `start-mocknet-env-network-mainnet`
- **Where:** `run_docker:882-905`; `events_file_env` / `bns_import_env` end with `source "${ENV_FILE}"`.
- **Mechanism:** the first toggle comments out `STACKS_EXPORT_EVENTS_FILE` and re-sources `.env`, which sets `NETWORK=mainnet` (the sample default). The revert block is guarded by `NETWORK == mocknet`, so it is skipped.
- **Impact:**
  - `.env` keeps `#STACKS_EXPORT_EVENTS_FILE=…`, so later mainnet/testnet `import`/`export` fail with "Undefined or commented STACKS_EXPORT_EVENTS_FILE".
  - The success message reports the wrong network.
  - Any `.env` value overrides the CLI for the rest of the run.
- **Control:** with `NETWORK=mocknet` in `.env`, the revert works (`start-mocknet-env-network-mocknet`).

### D04 — High — validation errors exit 0 **[V]** `cli-*`
- **Where:** `usage()` ends with `exit 0` and is the error path for every validation failure. `download_bns_data` and `event_replay` also `exit 0` on missing env vars; `docker_logs` when not running → usage → 0.
- **Impact:** automation (systemd, CI, Ansible) cannot detect bad invocations.

### D05 — High — `confirm` infinite loop **[V]** `start-mainnet-arm64-noninteractive`
- **Where:** `confirm()` `manage.sh:169-179` (same pattern in `postgres-upgrade.sh`).
- **Mechanism:** `read -n 1` at EOF returns non-zero with an empty `REPLY`, which falls into the `*)` branch and loops. `set -e` is suppressed because `confirm` is called inside `||`.
- **Impact:** any non-interactive `start` on Apple Silicon, or `reset`, spins forever printing "invalid input".

### D06 — High — secret exposure **[V]**
- **Where:** `env_file: ${SCRIPTPATH}/.env` on postgres, node, API, signer and bns.
- **Evidence:** the resolved config shows `SIGNER_PRIVATE_KEY`, `TESTNET_SIGNER_PRIVATE_KEY`, `BTC_RPC_PASS`, `PG_PASSWORD` and `AUTH_TOKEN` in *every* service's environment (`reference/compose-resolved/*.yaml`).
- **Also:**
  - `conf/<net>/` (which holds `Signer.toml` with the plaintext private key) is mounted read-write into the stacks-blockchain container.
  - `seed-chainstate.sh` `echo`es the `ALTER USER … PASSWORD '<pw>'` command.
- **Impact:** the key is visible through `docker inspect`, in container crash dumps, and to any process in any of these containers.

### D07 — Medium — default credentials and public binds **[S]**
`AUTH_TOKEN=1234`, `PG_PASSWORD=postgres`, and every published port on `0.0.0.0` (RPC 20443, metrics 9153/9154, API 3999, optional 5432). Nothing warns when the defaults are still in place.

### D08 — Medium — failed mocknet `up` leaves `.env` modified **[S]**
`eval "${cmd}"` (`manage.sh:898`) runs in a non-conditional context under `set -e`. If compose fails, the script exits before the revert at `:900-905`. Recovery today: `.env.save`.

### D09 — High — event replay path and safety **[V]** `export-testnet`, `import-mainnet`
- `tsv_file` is always `persistent-data/mainnet/event-replay/<basename>` (`manage.sh:827`), even for testnet, whose mount is `persistent-data/testnet/event-replay`.
- A missing file only logs an error (`:828-830`). The script still stops services and starts `import-events --wipe-db --force`. **Resolved [V]:** API 9.0.1 checks that the file exists before touching the DB (52 tables before and after). There is no data-loss path, only needless downtime, because the services were already stopped ([15](./15-verification-log.md) #11d).

### D10 — Medium — `bns` result reporting **[V]** `bns-defined`
`docker_down` → `check_network bns` (common.yaml has no `bns` services) → "not running" → `status` → `exit 1`. The "BNS Download Operation is complete" message is never printed. `down` never runs, so the exited `bns_data` container remains **[S]**.

### D11 — Low — `setup-bns.sh` `USER_ID` overwrite **[S]**
Lines 11-15: after defaulting, `USER_ID=$(id -u "$(whoami)")` runs unconditionally inside the container (root → 0), so `chown -R 0`.

### D12 — Low — `check_bns` no-op **[S]**
`BNS_FILES` exists only in `setup-bns.sh`, so the `for item in "${BNS_FILES[@]}"` loop in `manage.sh:341` never runs and always returns 0. Even if it were fixed, `set -f` from D13 would stop the directory glob from expanding.

### D13 — Low — global `IFS`/noglob leak **[S]**
`manage.sh:996` `set -f; IFS=','`, never restored. It is currently harmless only because every later list join and split happens to agree on `,`.

### D14 — Medium — flags not persisted **[V]** `start-signer-then-plain`
Each `start`/`restart` re-derives the signer state from the flags in that call. Without `-f signer`, the signer observer is commented out and the signer is torn down (`down` includes all extras). There is no record of how the stack was last started.

### D15 — Medium — `Config.toml` drift from samples **[S]**
`Config.toml` is copied once. Updates to `*.sample` (for example new testnet `[[burnchain.epochs]]`, a new `bootstrap_node`, new required keys) never reach existing installs. Operators get no hint that a re-copy is needed.

### D16 — Medium — port variables vs static config **[S]**
Changing `STACKS_CORE_RPC_PORT`, `STACKS_CORE_P2P_PORT`, `STACKS_CORE_EVENT_PORT` or `STACKS_BLOCKCHAIN_API_PORT` in `.env` changes the compose port mappings and the API env, but not the node's `rpc_bind`/`p2p_bind`, the API observer endpoint `:3700`, or the nginx upstream `:3999`. The stack breaks silently.

### D17 — Medium — helper scripts' `SCRIPTPATH` is cwd **[V]**
`ABS_PATH="$( cd -- "$(dirname '${0}')" …)"`: the single quotes stop `$0` from expanding, so `dirname` returns `.`. Run from the repo root (as documented) this works. From anywhere else, `.env`, `sample.env` and `persistent-data/` resolve against the wrong directory (for example `cd scripts && sudo ./seed-chainstate.sh` → `cp: sample.env: No such file`).

### D18 — Medium — seed tool assumptions **[S]**
- These are needed but not checked: `numfmt` and `sha256sum` (GNU coreutils), `awk -v IGNORECASE=1` (gawk only; harmless elsewhere), and `tar` + `zstd` for `.tar.zst`.
- `local x=$(…)` hides the failures, so on macOS `sha256sum` is missing → an empty hash → "sha256 mismatch".
- `download_file` returns early if `dest` exists, so a partial download is never resumed (even though `curl -C -` is in the command) and fails the checksum.

### D19 — Medium — seed assumes `PG_USER=postgres` **[S]**
The import container sets no `POSTGRES_USER`, so only the `postgres` role exists. `pg_restore --username ${PG_USER}` and `ALTER USER ${PG_USER}` fail for any other value. The later rename steps hard-code `-U postgres`.

### D20 — Low — unreachable branch **[S]**
`ordered_stop` treats `compose ps -q stacks-blockchain` *succeeding* as "running", but `ps -q` exits 0 even when it prints nothing.

### D21 — Low — mocknet stop timeout **[S]**
mocknet skips `ordered_stop`, so `down` uses Compose's default 10s grace period. This matters little because mocknet is ephemeral (D36).

### D22 — Medium — single instance and network-agnostic checks **[S]**
- Fixed `container_name`s and project name allow only one stack per host.
- `check_network` ignores the network. `status` says "running" for any network, and `reset -n testnet` is refused while mainnet runs.
- `-n testnet -a stop` while mainnet runs stops mainnet's containers, using testnet's compose files.

### D23 — Low — standalone verbs **[S]**
`logs`, `status`, `pull` and `bns` run at their position in argv, ignore the arguments after them, and run before the `NETWORK`→`STACKS_CHAIN_ID` mapping.

### D24 — Low — `stop` not idempotent **[V]** `stop-not-running`
Stopping an already stopped stack exits 1.

### D25 — Low — verbose timestamp **[S]**
`date "+%D %H:%m:%S"` (manage.sh, seed, upgrade) prints the month where minutes should be.

### D26 — Medium — `postgres-upgrade.sh` defects **[S]**
- WIP: mainnet only, source version hard-coded to 13, user `postgres` hard-coded.
- Uses `docker exec -it` (needs a TTY).
- The start timestamp's `date` call errors (escaped quotes).
- No running-services check.
- Deletes the data dir before verifying the restore.
- Leaves `pg_dump/` in cwd on failure.
- Documented under the wrong filename.

### D27 — Medium — image pinning **[S]**
`nginx:alpine` and `bash` are unpinned. The compose fallbacks (`latest`, postgres `15`) differ from `sample.env` (pinned versions, postgres `17`) and from the docs (postgres `14`). A `.env` that lacks a version key silently runs `latest`.

### D28 — Low — command-output execution **[S]**
About a dozen sites use `$(sed … 2>&1) || { log_exit …; }` (see [03](./03-manage-sh-internals.md), mechanics #3).

### D29 — Low — `eval` of string commands **[S]**
All compose and `docker logs` invocations are strings run through `eval`. A `SCRIPTPATH` with spaces or shell metacharacters breaks them, and values from `.env` are re-parsed as shell.

### D30 — Low — dead code **[S]**
- `check_api` and its caller (commented out)
- the `private-testnet` branch (no such network file)
- `FLAGS="proxy"` default; `ENV_FILE_TMP`
- `LOG_TAIL` (not overridable)
- unused colors (`COLBLUE`, `COLMAGENTA`, …)
- `set -E`/`functrace` with no traps
- the API `5.0.1` guard
- `STACKS_CHAIN_ID_DEFAULT` (unused by scripts)

### D31 — Low — noisy output **[V]**
`cat ${ENV_FILE} | grep …` in the mocknet toggles prints `.env` lines regardless of `VERBOSE`.

### D32 — Low — signer substring match **[S]**
`[[ "${FLAGS_ARRAY[*]}" == *"signer"* ]]`, so any future flag name containing "signer" turns on signer mode.

### D33 — Low — unnecessary config writes **[V]** `bns-defined`, `import-mainnet`
`docker_up` runs `update_configs`, `.env.save` and the persistent-dir creation for the `bns` and `event-replay` profiles too. That touches `Config.toml` (and disables the signer block) even though the node doesn't run.

### D34 — Low — testnet RPC vars **[S]**
`TBTC_RPC_PORT` has no effect, and `TBTC_RPC_USER`/`TBTC_RPC_PASS` are undefined, because the testnet sample has only `peer_host`/`peer_port`. The docs list `TBTC_RPC_PORT` as configurable.

### D35 — Medium — `sed` injection **[S]**
`.env` values go straight into `s/.*/key = "VALUE"/`. A `BTC_RPC_PASS`, `AUTH_TOKEN` or key containing `/`, `&`, `\` or `"` breaks the sed (abort) or writes corrupt TOML.

### D36 — Info — mocknet persistence model **[S]**
mocknet mounts no node or DB data, so every `down` resets the chain. Meanwhile, `persistent-data/mocknet/stacks-signer` persists. `reset -n mocknet` reports "No data exists" unless the signer dir exists, and if it does, deleting it is all `reset` does.

### D37 — Low — mocknet replay **[S]**
mocknet maps `/tmp/event-replay` to `persistent-data/mainnet/event-replay`. The docs say import/export do not apply to mocknet, but the script allows them. Combined with the mocknet `.env` toggle, the API command receives an empty `$STACKS_EXPORT_EVENTS_FILE`.

### D38 — Medium — replay detection heuristic **[S]**
- `check_event_replay` greps fixed English log strings in the first and last 20 lines of `docker logs`. A change to the API log format, extra log lines after completion, or log rotation all defeat the guard.
- When the guard fails open, a stop or start can interrupt a replay.
- When it fails closed, the operator is locked out and must remove the container by hand.

### D39 — Low — arm64 prompt **[S]**
- Triggers on every `start` on any arm64 Darwin host (M1 through M4); Linux arm64 (`aarch64`) is not affected.
- Not shown on `restart`.
- The wording still says "M1".

### D40 — Medium — no automated checks **[S]**
The repo has only `.github/stale.yml`: no workflow, no shellcheck, no compose validation, no tests. The harness under `refactor/harness` is the first characterization suite.

### D41 — Low — dual `.env` parsing **[S]**
`source .env` (bash) and Compose's dotenv parser disagree on quoting, `export` prefixes, inline comments and `$` expansion. A value that works in one can differ in the other.

### D42 — Low — fixed sleeps **[S]**
The seed and upgrade scripts `sleep 15` instead of polling `pg_isready`. A slow disk causes restore failures.

### D43 — Low — seed minor defects **[S]**
- `export $CURRENT_USER` (exports a variable named after the user)
- `DEBUG` undefined in the verbose logger
- `tar -xvf` prints every file of a huge archive
- `echo`ed commands include the DB password (see D06)
- The final `exit_error` path always runs `docker stop postgres_import`, which can print an error before the container exists.

### D44 — Low — log export coverage **[S]**
`logs_export` iterates only over `DEFAULT_SERVICES` (node, API, postgres). Signer and nginx logs are not exported.

### D45 — Info — legacy features **[S]**
- The API `5.0.1` refusal refers to a 2022 issue and is obsolete.
- **Revised:** API 9.0.1 still runs the BNS v1 import when block height 1 arrives if `BNS_IMPORT_DIR` is set ([15](./15-verification-log.md) #11b). It only matters for a genesis sync that wants full BNS v1 history; a seed or replay doesn't need it. Keeping it is an owner call (Q8).
- `docs/issues.md` already notes that the BNS import moved into event-replay with API ≥ 5.
- Upstream should confirm before these are kept or removed.

### D46 — High — hardcoded API dependency **[V]** (issue [#153](https://github.com/stx-labs/stacks-blockchain-docker/issues/153))
- **Where:**
  - `common.yaml` `stacks-blockchain.depends_on: [stacks-blockchain-api]`
  - every `Config.toml.sample` has an always-on `[[events_observer]] endpoint = "stacks-blockchain-api:3700"`
  - nginx upstream is hard-coded
  - postgres and the API share the `stacks-blockchain` profile with the node
- **Impact:**
  - There is no node-only mode, and no external API/DB.
  - **Confirmed [S] [O]:** with the default `event_dispatcher_blocking = true`, a down or slow API **stalls the node indefinitely**, until the API is back. Each payload is retried forever. The backoff starts at 100 ms, doubles with jitter, and is capped at 3 × `timeout_ms`: **900 s** with the mainnet sample's `300_000`, 180 s with testnet's `60_000` (4.0.4 source **[S]**, measured in P2 **[V]**; see D55). Pending payloads persist in `event_observers.sqlite` and are redelivered before normal operation resumes; payloads for an observer that is no longer configured are dropped ([15](./15-verification-log.md) #5a). Real stall durations come from the stress test.
  - Note: a plain `depends_on` on a service in an inactive profile makes the compose project invalid **[V]**. The fix needs `required: false` (Compose ≥ ~2.20) together with a rendered observer list. See [14 §6](./14-platform-and-architecture.md).

### D47 — Medium — host portability **[V]**
- Requires bash, GNU sed/coreutils, `sudo`/`chown` and POSIX paths. Windows is unsupported natively.
- There is no `.gitattributes` (all 35 files are LF today), so a Windows checkout with `core.autocrlf=true` would convert `.env`, the TOML files and the scripts to CRLF.
- The goal of running on Linux, other Unix, macOS and Windows cannot be met by patching. See [14](./14-platform-and-architecture.md).

### D48 — High — mount-point deletion (issue [#152](https://github.com/stx-labs/stacks-blockchain-docker/issues/152))
- **Where:**
  - `seed-chainstate.sh:176-179` `rm -rf persistent-data/$NETWORK`
  - `manage.sh` `reset_data` `rm -rf persistent-data/$NETWORK`
  - `postgres-upgrade.sh` `rm -rf …/postgres`
- **Mechanism:** when `postgres/` or `stacks-blockchain/` are dedicated mounts, `rm` deletes their contents and then fails with `Device or resource busy` on the mount point itself. **[V by user report]**, mechanism **[S]**.
- **Impact:**
  - Seed aborts under `set -e` *after* wiping the data.
  - `reset` reports failure and wrongly suggests `sudo`.
- **Workaround from the issue:** `find <dir> -mindepth 1 -delete`.
- **Fix:** empty contents, never remove roots; support per-component data paths and mounts. See [14 §9.1](./14-platform-and-architecture.md).

### D49 — Medium — seed throughput (issue [#132](https://github.com/stx-labs/stacks-blockchain-docker/issues/132))
- Download, then a full re-read for `sha256sum`, then a second checksum, then a single-threaded `tar` + zstd extract, then a single-job `pg_restore`. Every stage is serial.
- `openssl -parallel` does not help, because one SHA-256 digest over one stream is inherently sequential; the maintainer reproduced exit 1.
- Real gains come from hashing during download, verifying both archives concurrently, streaming extraction to staging, `zstd -T0` and `pg_restore -j`. See [14 §9.2](./14-platform-and-architecture.md).

### D50 — High — testnet config is obsolete (pre-reset testnet) **[V]**
Re-checked on 4.0.4 on 2026-10-26 (P4 in [15](./15-verification-log.md)). The sample fails in four separate ways. `check-config` stops at the first error, so each fix only reveals the next:
1. `Invalid toml: unknown field always_use_affirmation_maps` (`conf/testnet/Config.toml.sample:6`), on 4.0.1, 4.0.4 and 4.0.5.
2. With that key removed: `unknown field require_affirmed_anchor_blocks` (line 7).
3. With both removed, `check-config` passes, but the node **panics at startup**: `FATAL: no Epoch40 defined in epoch list, cannot validate PEER_NETWORK_EPOCH` (`stackslib/src/core/mod.rs:1507`). Release builds require the release epoch (4.0 for 4.0.x) in a custom epoch list, and the sample stops at 3.4.
4. **Root cause:** the Hiro testnet was reset. Upstream `sample/conf/testnet-follower-conf.toml` was rewritten in stacks-core PR #7490 (2026-08-10):
   - new bootstrap node `0348af…@seed.testnet.hiro.so:20444`. The repo's `029266…@…:30444` port is **closed**;
   - new epoch heights: 3.0 → 1802 … 3.4 → 1806, plus **4.0 → 2702**. The repo has 3.0 → 1900 … 3.4 → 159350;
   - new `pox_5_sbtc_contract`, `pox_5_sbtc_registry_contract` and `pox_5_bond_admin` keys;
   - eight more `[[ustx_balance]]` entries.

So `-n testnet -a start` can't work with any current image.

**Fix:**
- **Owner decision (Q27, 2026-10-26):** use upstream stacks-core **`main`** [`sample/conf/`](https://github.com/stacks-network/stacks-core/tree/main/sample/conf) as the source: `testnet-follower-conf.toml`, or `testnet-signer.toml` when a signer is enabled. On 2026-10-26 `main` equals 4.0.4 for these files. Regenerate `conf/testnet/Config.toml.sample` from it, keeping the repo-specific `working_dir`, observers and `auth_token`.
- The refactor's renderer treats the vendored upstream `main` samples as the source of the network definition (bootstrap node, epochs, balances, `pox_5_*`) instead of a hand-copied file. CI then runs `check-config` **and** a short boot, because `check-config` alone missed the Epoch 4.0 panic.
- A config built this way booted and served RPC with a signer attached in P4.
- Mainnet: the 4.0.4 mainnet sample still passes and synced in P1. Upstream's 4.0.4 mainnet sample should still be diffed for new keys during phase 0.

### D51 — High — mocknet broken **[V]**
- On 4.0.1 the repo's mocknet config passes `check-config`, but the node panics at startup: `FATAL: Epoch 3.0 must not start at a reward cycle boundary (offset 0 or 1). Activation height: 7001`. The image's built-in `stacks-node mocknet` fails the same way.
- 4.0.5 has **no `mocknet` subcommand** and rejects `commit_anchor_block_within`.
- Mocknet was also the planned CI smoke test (11 §E, 14 §7). A replacement is needed ([15](./15-verification-log.md) #5b).

### D52 — High — seed archive names changed **[V]**
- `seed-chainstate.sh` builds `…-pg-${POSTGRES_VERSION}-${STACKS_BLOCKCHAIN_API_VERSION}-latest.dump` and `${NETWORK}-stacks-blockchain-${STACKS_BLOCKCHAIN_VERSION}-latest.tar.zst`. For 9.0.1/4.0.1 both return **404** `NoSuchKey`. The curl fallback runs without `--fail`, so it saves the XML error body as the archive and only the checksum step catches it. The HEAD pre-check doesn't abort, by design (HEAD is always 403, see D53).
- The archive now serves `stacks-blockchain-api-pg-17-latest.dump` and `<net>-stacks-blockchain-latest.tar.zst`. Each `.sha256` line names the real dated version, for example `…-17-9.6.0-20261019.dump` and `mainnet-stacks-blockchain-4.0.4-20261019.tar.zst`.
- Sizes on 2026-10-19: dump 105 GB, chainstate 277 GB.
- **Correction (2026-10-26):** the archive is not "`-latest` only".
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
- **Hotfix direction (Q24, re-settled 2026-10-26):** select the newest date **D on which both** `<net>-stacks-blockchain-<core>-D.tar.zst` and `stacks-blockchain-api-pg-<pg>-<api>-D.dump` exist **and** their versions are compatible with the pins (same major, ≤ pin; dump pg major ≤ server). Verify each file against its own `.sha256`. Never mix dates, and never download `-latest`. Warn if the versions are older than the pins; exit 1 with the reason and the latest snapshot's versions otherwise. See [11](./11-refactor-guidance.md) Q24.

### D53 — Low — download size via HEAD **[V]**
archive.hiro.so redirects to pre-signed URLs that answer HEAD with **403**, so `Content-Length` is never found and the size prints as 0. A 1-byte ranged GET returns `Content-Range: bytes 0-0/<size>`.

### D54 — Info (was Low) — duplicate event delivery via `STACKS_EVENT_OBSERVER` **[S]** **[V]**
Owner **[O]**: the API should not write an event it has already processed, so no data corruption is expected. What remains is a second blocking delivery per event. **Confirmed live by P3** ([15](./15-verification-log.md), Linux x64, 2026-10-26):
- The node registered both observers and the API ingested about **2.2× `block` and 2.3× `burn_block`** events per tip advance (about 1:1 before).
- The API stored **no duplicates**: unique `index_block_hash` / `(tx_id, index_block_hash)` / `burn_block_hash`, one canonical block per height, and an unchanged orphan rate.
- **Residual risk, not measured:** the extra observer's fixed 1 s timeout. A slow `/new_block` (large blocks, a busy DB) would time out and retry against the API, and while blocking, it stalls the node.
`sample.env` carries `# STACKS_EVENT_OBSERVER=stacks-blockchain-api:3700`. stacks-core adds this observer (with a 1 s timeout) *in addition to* the TOML observers. Because its timeout differs from the TOML API observer's (60 s testnet, 300 s mainnet), both are kept, and every event is POSTed to the API twice ([15](./15-verification-log.md) #10).

### D55 — Medium — slow recovery after an API outage (backoff sleep) **[V]**
Measured in P2 ([15](./15-verification-log.md), 2026-10-26, stacks-core 4.0.4, mainnet sample `timeout_ms = 300_000`):
- **Stall:** the API was stopped and the Stacks tip froze at once (600 for 8 min).
- **Retries:** the dispatcher retried at roughly doubling intervals: 2.4, 4.9, 9.8, 19.6, 39.2, 78.4, 156.8, then 313.7 s. Left alone it would go on to 627 s and then sit at the 900 s cap (`event_dispatcher/worker.rs:342-391`).
- **Restart:** the API was restarted at 17:16:01 and served requests by 17:16:11.
- **Recovery:** the node only retried when its current sleep ended (17:18:26), **2 min 15 s after the API was healthy**. Then it resumed normally (tip 600 → 737 within 10 s; no events lost, API height 743). Repeated on Linux x64 [V]: **5 min 29 s** of lag (a 379 s sleep at attempt 11), with the same no-loss result ([15](./15-verification-log.md) "P2 results — Linux x64").

**Impact:**
- After any API outage longer than about 10–15 min (an upgrade, pg-upgrade, `import-events`, a crash loop), the stack may sit idle for up to **15 min** after everything is "up". It looks hung, and operators may restart things unnecessarily.
- Healthchecks and alerts should expect this.

**Decision (owner, 2026-10-26, Q26 in [11](./11-refactor-guidance.md)): option (a), and document it.** (b) doesn't cover an API that is running but failing, e.g. one that has lost its Postgres connection. It answers non-200, the node logs `Failed POST` and backs off the same way (`worker.rs:356-366`), and no container lifecycle event exists to hook a restart on.

**Options considered:**
- (a) Render a smaller API observer `timeout_ms`, e.g. 60 s, which caps the backoff at 3 min. Check this against slow API ingests: the timeout must stay above the API's worst-case `/new_block` handling time.
- (b) After the API comes back from a planned outage (pg-upgrade, import, upgrade), restart the node. A restart redelivers pending payloads from `event_observers.sqlite` immediately. Jobs can do this automatically.
- (c) Document it in the runbook and the monitoring alert text.
- Note that the error seen while the API container is stopped is a **DNS lookup failure** (`failed to lookup address information`), not a connection refusal. The behavior is the same.

## Issue cross-reference

| Upstream issue | Findings | Plan |
|---|---|---|
| [#153](https://github.com/stx-labs/stacks-blockchain-docker/issues/153) Make this the default way to self-host node + API | D46 (API dependency), D47 (portability), D06/D07 (secure defaults), D40 (no tests for stress-testing) | [14](./14-platform-and-architecture.md), [12](./12-monitoring-plan.md) (built-in Grafana, health companions) |
| [#152](https://github.com/stx-labs/stacks-blockchain-docker/issues/152) seed-chainstate fails with device busy | D48, D01 | [14 §9.1](./14-platform-and-architecture.md) |
| [#132](https://github.com/stx-labs/stacks-blockchain-docker/issues/132) Parallelize SHA-256 verification | D49, D18 | [14 §9.2](./14-platform-and-architecture.md) |
