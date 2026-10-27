# Characterization Harness

`run.sh` records what `manage.sh` does **today**, so a refactor can prove it kept that behavior (or show exactly what it changed).

## What it does

1. Copies the tracked files from the current working tree (excluding `refactor/`) into a temp dir. **The real repo is never touched.**
2. Puts stubs first in `PATH`:
   - **`docker`** logs every invocation. `compose version` succeeds. `ps -q` prints a container ID if `STUB_RUNNING=1`. `docker logs stacks-blockchain-api` prints canned replay log lines selected by `STUB_API_LOG` (`import-started`, `import-done`, `export-started`, `export-done`, comma separated).
   - **`uname -m`** returns `STUB_ARCH` (default `x86_64`) so the arm64 prompt can be tested.
   - **`sed`** is linked to `gsed` when the system `sed` is BSD. `manage.sh` needs GNU sed (see D02); without it, the start scenarios record the failure.
3. For each scenario, in a fresh copy:
   - pre-create `.env` from `sample.env`, except for `env-autocreate-*` scenarios;
   - apply `.env` overrides;
   - run an optional `pre` step;
   - snapshot the tree;
   - run `./manage.sh <args>` with optional stdin and a 15s timeout (exit `142` = hung).
4. Records:
   - exit code
   - output (ANSI stripped, paths normalized to `<REPO>`)
   - docker calls
   - files added or removed
   - unified diffs of modified files, and of files newly created from a `*.sample`

## Usage

```bash
refactor/harness/run.sh               # print all scenarios
refactor/harness/run.sh mocknet       # only scenarios whose name contains "mocknet"
refactor/harness/run.sh --record      # rewrite golden/*.txt
refactor/harness/run.sh --check       # compare to golden/, exit 1 on any difference
```

Requirements: bash, perl (for the timeout), git, tar, diff, comm, and GNU sed available as `sed` or `gsed`. The whole suite takes about 30s.

## Scenario format

Each entry in the `SCENARIOS` array has six `|`-separated fields:

```
name | stub env (K=V ...) | .env overrides (K=V ...) | pre shell snippet | stdin | manage.sh args
```

Example: `"stop-import-in-progress|STUB_RUNNING=1 STUB_API_LOG=import-started|||| -n mainnet -a stop"`

## Golden files

`golden/<scenario>.txt` was recorded from `d2e558c` on 2026-10-16. **The goldens deliberately capture current defects** (exit 0 on errors, the arm64 hang, the mocknet `.env` mutation, `bns` exiting 1, and so on). When a refactor fixes one of these, the matching golden is expected to change. Re-record it and review the diff as part of the change.

## Carrying it into the rewrite

The full refactor ([14](../14-platform-and-architecture.md)) replaces `manage.sh` with a cross-platform CLI. The scenarios here are the parity spec:
- Port each scenario to the new CLI's `--dry-run`/`plan` output: the exact `docker compose` argv plus rendered files.
- Keep the same golden-diff workflow.
- The stub-`docker`-on-`PATH` technique keeps working as long as the CLI shells out to `docker compose`, which is the Q17 recommendation.
- Because the CLI is optional (Q22), each ported scenario should also assert that its recorded compose argv matches the documented plain-compose command in [14 §11](../14-platform-and-architecture.md). That keeps the CLI and the manual path from drifting apart.
- Every intended behavior change, such as the D03/D04 fixes or the new profiles, should show up as a reviewed golden diff.

## Limitations

- `STUB_RUNNING` is static for the whole run: "running" stays true even after the script stops services. Scenarios that stop and then start in one invocation (`restart`, `import` while running) therefore end with "already running". See `import-mainnet-running`. Treat those as partial coverage.
- Docker Compose itself is never executed, so compose file validity is covered separately by `../reference/compose-resolved/` (regenerate with `docker compose config`).
- Helper scripts (`seed-chainstate.sh`, `postgres-upgrade.sh`, `setup-bns.sh`) are not exercised: they need root, the network, and real containers.
- `logs` scenarios return immediately because the stub doesn't follow logs.
