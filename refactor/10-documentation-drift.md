# 10 — Documentation Drift

Each row is a place where the shipped docs (or the script's own `usage`) disagree with actual behavior. "Actual" refers to the code at `d2e558c`.

| # | Location | Docs say | Actual |
|---|---|---|---|
| 1 | `docs/config.md` table | `POSTGRES_VERSION` default `14` | `sample.env` = `17`; compose fallback = `15` |
| 2 | `docs/config.md` testnet table | `TBTC_RPC_PORT` `18332`, `TBTC_P2P_PORT` `18333`, "FQDN of bitcoin testnet host" | `sample.env`: `18443` / `18444`; host is a **regtest** node; `TBTC_RPC_PORT` has no effect (D34) |
| 3 | `docs/config.md` intro | "Burnchain environment variables … overwrite values in `Config.toml` for mainnet/testnet" | Also overwrites `auth_token`, the signer endpoint and `prometheus_bind`, and toggles the signer block, on **all** networks including mocknet; also rewrites `Signer.toml` |
| 4 | `docs/config.md` file list | `.env`, mainnet/testnet `Config.toml`/`Signer.toml` | mocknet `Config.toml`/`Signer.toml` exist as well; `conf/nginx/default.conf` is also user-editable |
| 5 | `docs/config.md` | (missing) | Undocumented vars: `NETWORK`, `PG_SCHEMA`, `STACKS_CHAIN_ID_DEFAULT`, `V2_POX_MIN_AMOUNT_USTX`, `STACKS_ADDRESS_CACHE_SIZE`, `STACKS_EVENT_OBSERVER` |
| 6 | `docs/config.md`, `docs/issues.md` | "`./env`" | The file is `.env` |
| 7 | `docs/issues.md` "Missing bit_xor" | Run `./scripts/postgres_upgrade.sh` to go 13 → 14 | The file is `postgres-upgrade.sh`; it targets `POSTGRES_VERSION` (17), is WIP and mainnet-only |
| 8 | `docs/issues.md` "Database Issues" | Seed "requires at least 150GB" | README says ~**1.5TB**; requirements says 250GB "if downloading a chainstate archive" |
| 9 | `docs/requirements.md` | `jq` required | Not used by any script (README examples only) |
| 10 | `docs/requirements.md` | links GNU sed | Does not state that **BSD/macOS sed fails** (D02) |
| 11 | `docs/requirements.md` | "MacOS with an M1 processor" | The check triggers on every arm64 Mac and loops forever without a TTY (D05) |
| 12 | `docs/usage.md` / `usage()` | actions: `start stop logs reset upgrade import export bns`; flags: `signer,proxy` | Also accepted: `up`, `down`, `restart`, `log`, `pull`, `status`; flag `postgres`; standalone `status`/`logs`/`pull`/`upgrade` |
| 13 | `docs/usage.md` | Event replay "Not applicable for mocknet" | Not enforced; mocknet replay runs, with a broken env (D37) |
| 14 | `docs/upgrade.md` | `upgrade` presented as part of the API upgrade procedure | `upgrade` == `pull`: pulls images only; no restart, no replay, no schema-change detection (`check_api` is commented out) |
| 15 | `docs/usage.md` "Download BNS data" | `./manage.sh bns` completes the download | It does, but it exits 1 and prints "not running" instead of the completion message (D10) |
| 16 | `docs/issues.md` BNS section | BNS import via event-replay for API ≥ 5 | API 9.0.1 still imports BNS v1 data when block height 1 arrives if `BNS_IMPORT_DIR` is set **[S]** (D45, [15](./15-verification-log.md)) |
| 17 | `README.md` | `cp sample.env .env` as a required step | Optional: every script auto-creates `.env` |
| 18 | `README.md` seed section | `sudo ./scripts/seed-chainstate.sh` | Correct only when run from the repo root (D17), and it deletes existing data without prompting (D01). Neither is mentioned |
| 19 | `docs/usage.md` "Delete all chainstate data" | "Some of the data … is owned by root" | Accurate; the cause (Docker `create_host_path`) is undocumented |
| 20 | `docs/docker.md` | Compose ≥ 2.2.2 | The script only checks that `docker compose version` succeeds; there is no version comparison |
| 21 | `manage.sh` arm64 warning link | `stacks-network/stacks-blockchain-docker/.../requirements.md#macos-with-an-m1-processor-is-not-recommended-for-this-repo` | Hard-coded upstream URL; the anchor depends on the heading text |
| 22 | `packer/README.md` | Build images via an external repo | No packer content here; `.gitignore` still lists `packer/vars.json` |
