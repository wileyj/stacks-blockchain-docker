# 02 — `manage.sh` CLI Reference

## Synopsis

```
./manage.sh -n|--network <network> -a|--action <action> [-f|--flags <flag[,flag...]>] [export]
./manage.sh <standalone-verb>          # logs | log | status | upgrade | pull | bns
./manage.sh -h|--help
```

Running it with no arguments prints usage (`manage.sh:928-932`).

## Options

| Option | Value | Normalization | Validation | Notes |
|---|---|---|---|---|
| `-n`, `--network` | network name | spaces stripped, lowercased (`tr -d ' ' \| awk tolower`) | must match a basename in `compose-files/networks/*.yaml` → `mainnet`, `testnet`, `mocknet` | Defaults to `NETWORK` from `.env` (sample: `mainnet`); the script's hard default `mainnet` is set before `.env` is sourced |
| `-a`, `--action` | action name | same | must be in the `SUPPORTED_ACTIONS` list | Required (`manage.sh:1067-1071`) |
| `-f`, `--flags` | comma-separated list | same | each item is kept only if it matches a basename in `compose-files/extra-services/*.yaml`; **unknown items are silently dropped [V]** | `bns` is rejected unless `ACTION` is already `bns` (order-dependent, see below). **Side effect:** sets `IFS=','` and `set -f` for the rest of the run (`manage.sh:996`) |
| `export` | literal word | — | only honored immediately after `-a log`/`-a logs` (as `$3`) or after the standalone `log`/`logs` verb (as `$2`) | Triggers `logs_export` |
| `-h`, `--help` | — | — | — | Prints usage, exits 0 |

A missing value (`-n` as the last argument) produces `Missing required value for -n` plus usage, with exit **0** **[V]**. Values that start with `-` are taken as values (`-n -a` sets `NETWORK="-a"`, which then fails validation).

## Actions (`-a`)

| Action | Alias | Function | Interactive? | Network-specific behavior |
|---|---|---|---|---|
| `start` | `up` | `check_device` → `docker_up` | arm64 prompt | mainnet/testnet: create `persistent-data/<net>/event-replay`, check BNS. mocknet: temporarily comment env vars in `.env` |
| `stop` | `down` | `docker_down` | no | mainnet/testnet: ordered stop (node 1200s → API → postgres), then `down`. mocknet: `down` only |
| `restart` | — | `docker_down` then `docker_up` | no (skips the arm64 check) | as start/stop |
| `logs` | `log` | `docker_logs "-f --tail 100"` | follows until Ctrl-C | none |
| `logs export` | `log export` | `logs_export` | no | none (writes `exported-logs/<service>.log` for postgres, API, node only) |
| `pull` | `upgrade` | `docker_pull` | no | Pulls images for all extra-services under profile `stacks-blockchain`. **Does not restart anything or run a replay** |
| `status` | — | `status` | no | Not network-aware (any stack counts as running) |
| `reset` | — | `reset_data` | **y/n prompt** | Deletes `persistent-data/<net>` |
| `import` | — | `event_replay import` | no | Starts profile `event-replay` with `api-import-events.yaml` (`--wipe-db --force`) |
| `export` | — | `event_replay export` | no | Starts profile `event-replay` with `api-export-events.yaml` |
| `bns` | — | `download_bns_data` | no | Runs the `bns_data` container in the foreground |

`usage()` (`manage.sh:151-166`) only advertises `start | stop | logs | reset | upgrade | import | export | bns` and flags `signer,proxy`. The rest is accepted but undocumented.

## Standalone verbs (no `-a`)

The argument loop handles these as soon as it reaches them (`manage.sh:1006-1045`). They run immediately, and **any arguments after them are ignored** **[S]**.

| Verb | Runs | Notes |
|---|---|---|
| `upgrade`, `pull` | `docker_pull`; exit 0 | `break`s out of the loop instead if `ACTION` is already `status` |
| `log`, `logs` | `docker_logs` (or `logs_export` if followed by `export`); exit 0 | `break`s out if `ACTION` is already `status` |
| `status` | `status` (exit 0 = running, 1 = not) | `break`s out if `ACTION` is already `logs` |
| `bns` | `download_bns_data` (always exits) | Re-exports `STACKS_CHAIN_ID` (still the mocknet default at this point) |

The verbs run *before* the post-parse block that maps `NETWORK` to `STACKS_CHAIN_ID` (`manage.sh:1080-1097`). For standalone verbs, `STACKS_CHAIN_ID` is therefore always `2147483648` and `NETWORK` comes from `-n` (only if `-n` came earlier) or from `.env`.

## Parsing semantics (behaviors a refactor must consciously keep or drop)

1. **Left-to-right, with immediate effects.** `-a logs export` exports the logs the moment `-a` is parsed, even if `-n` comes later. `bns`, `status`, `logs` and `pull` likewise act at their position.
2. **Order-dependent flag validation.** `-f bns -a bns` → error "bns is not a valid flag", because `ACTION` is still empty when `-f` is parsed **[V]**. `-a bns -f bns` passes validation.
3. **Flags are case-insensitive** and spaces inside them are stripped (`-f "foo, PROXY"` → `proxy` is kept, `foo` dropped) **[V]**.
4. **The `signer` flag is detected by substring.** `[[ "${FLAGS_ARRAY[*]}" == *"signer"* ]]` (`manage.sh:606`), so any flag containing "signer" turns on signer mode.
5. **Flags are not persisted.** Each `start`/`restart` acts on only the flags given in that call. `stop` always tears down *every* extra service.
6. **Positional garbage** (`-n mainnet -a start foo`) → "Malformed arguments"; unknown dashed options → "Unknown arg supplied". Both exit **0** **[V]**.
7. **`.env` is sourced before parsing**, so `.env` sets `VERBOSE`, the default `NETWORK`, `STACKS_SHUTDOWN_TIMEOUT` and the rest. CLI `-n` then overrides `NETWORK`. In the mocknet path, though, `.env` is sourced *again* mid-run, which resets `NETWORK` back to the `.env` value **[V]** (see [09 D03](./09-defects-and-risks.md)).

## Exit-code matrix (observed, [V] unless noted)

| Situation | Exit |
|---|---|
| No args / `-h` / any validation error (bad network, bad action, missing value, unknown arg, malformed, `-f bns`) | **0** |
| Missing `-a` | **0** |
| `start` success | 0 |
| `start` while already running | 1 |
| `start -f signer` with empty private key | 1 |
| `start` on arm64 with non-TTY stdin | **never exits** (infinite "invalid input" loop) |
| `stop` success | 0 |
| `stop` when nothing is running | **1** (falls through to `status`) |
| `stop`/`restart` while an event-replay import/export is in progress | 1 |
| `restart` when nothing is running | 0 (acts as start) |
| `status` running / not running | 0 / 1 |
| `logs` when not running | **0** (error + usage) |
| `logs` running | exit code of `docker compose logs -f` (130 on Ctrl-C) **[S]** |
| `logs export` | 0 |
| `pull` | 0 (compose failure → non-zero via `set -e`) **[S]** |
| `import`/`export` with `STACKS_EXPORT_EVENTS_FILE` unset | **0** |
| `import`/`export` started | 0 (the script exits right after `up -d`; the replay continues in the background) |
| `bns` with `BNS_IMPORT_DIR` unset | **0** |
| `bns` when services are running | 1 |
| `bns` successful run | **1** (prints "services are not running" instead of the completion message) |
| `reset` confirmed / declined / while running / no data | 0 / 1 / 1 / **0** |
| Missing `docker`, `id`, or `docker compose` | 1 |
| Any failing docker command in a non-conditional context | non-zero (`set -e`) **[S]** |

## Examples (all verified against the stub harness)

```bash
./manage.sh -n mainnet -a start                 # node + API + postgres
./manage.sh -n mainnet -a start -f signer,proxy # + stacks-signer + nginx (needs SIGNER_PRIVATE_KEY)
./manage.sh -n testnet -a start -f signer       # uses TESTNET_SIGNER_PRIVATE_KEY and TBTC_* vars
./manage.sh -n mainnet -a start -f postgres     # undocumented: publish postgres on ${PG_PORT}
./manage.sh -n mainnet -a stop                  # ordered stop, then down
./manage.sh -n mainnet -a restart -f proxy      # flags must be repeated on restart
./manage.sh -n mainnet -a logs                  # follow last 100 lines of all services
./manage.sh -n mainnet -a logs export           # -> ./exported-logs/{stacks-blockchain,stacks-blockchain-api,postgres}.log
./manage.sh logs export                         # same, standalone
./manage.sh -n mainnet -a pull                  # == upgrade
./manage.sh status                              # exit 0 if running
./manage.sh -n mainnet -a export                # API export-events -> persistent-data/mainnet/event-replay/*.tsv
./manage.sh -n mainnet -a import                # API import-events --wipe-db --force
./manage.sh bns                                 # requires BNS_IMPORT_DIR uncommented in .env
sudo ./manage.sh -n mainnet -a reset            # prompts y/n; deletes persistent-data/mainnet
```

## Environment-driven CLI behavior

| Variable | Effect on CLI |
|---|---|
| `VERBOSE=true` (in `.env`) | Turns on debug log lines and redefines the log aliases with a timestamp/function/line prefix. **Caveat:** the aliases are defined once at line 76, *after* `.env` is sourced, so it works, but the timestamp uses `%m` (month) where minutes were meant **[S]** |
| `STACKS_SHUTDOWN_TIMEOUT` | Stop timeout for `stacks-blockchain` during the ordered stop (default 1200s) |
| `EXPOSE_POSTGRES=true` | Adds `extra-services/postgres.yaml` to every compose invocation |
| `LOG_TAIL` | **No effect.** `LOG_OPTS` is computed from the script's own `LOG_TAIL=100` before `.env` is sourced |
