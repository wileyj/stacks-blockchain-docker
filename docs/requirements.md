# Requirements

- [Docker](./docker.md) >= `20.10.12`
- [docker compose](./docker.md) >= `2.24.0` (`2.27.2` or later recommended)
- [git](https://git-scm.com/downloads)
- For the deprecated `./manage.sh` only: bash and GNU sed. Plain `docker compose` and `sbd` need nothing else.

`sbd doctor` checks all of the following on the host it runs on (warnings only; nothing blocks).

## Host

| | Testnet | Mainnet, node only | Mainnet, node + API |
|---|---|---|---|
| Memory available to Docker | 4 GB | 4 GB | 8 GB or more |
| CPUs | 2 | 2 | 4 or more |
| Storage | about 50 GB | about 620 GB | about 1.15 TB |

Storage, measured with this repo's seed job (2026-10): the mainnet chainstate is about 620 GB and the
API's postgres database about 510 GB with all its indexes; testnet snapshots are 6.4 + 2.7 GiB
compressed. Both grow with the chain, so leave room: `sbd doctor` wants the estimate plus 20% free for
a component that has no data yet, and 50 GB (mainnet) or 5 GB (testnet) where data already exists.

Seeding from the Hiro archive needs more while it runs: the downloaded archives (about 360 GB for
mainnet; deleted after a successful seed unless `--keep-downloads`) and, when replacing existing data,
the old data until the new one is swapped in. The seed job prints the space it needs before it
starts (`docker compose run --rm seed --dry-run`).

Put the data on a dedicated disk with `DATA_ROOT`, or per component with `STACKS_DATA_DIR`,
`POSTGRES_DATA_DIR` and `SIGNER_DATA_DIR` in `.env`. They may be mount points.

## Bitcoin node

A Stacks node follows Bitcoin over its P2P port (8333 on mainnet). **Run your own Bitcoin node if at all
possible** and point `BTC_HOST` / `BTC_P2P_PORT` at it; a node on the same host or LAN syncs Bitcoin
headers in about a minute. It must still have the blocks the Stacks node needs: everything from Bitcoin
block 666 050 (Stacks genesis) for a sync from genesis, or from the snapshot's height after a seed, so
an unpruned `bitcoind` is the safe choice; a pruned one works only if its window covers that range. The default, `bitcoin.mainnet.stacks.org`, is
a single public peer shared by many Stacks nodes; it often turns connections away, and the node then
retries (16, 32, 60 s ...), so a first sync from it can take much longer. No RPC credentials are needed.

## Docker Desktop (macOS, Windows)

Chain data on a bind mount goes through Docker Desktop's file sharing, which is much slower than a
Linux host's disk, and a blockchain is very heavy on I/O. Testnet and trying things out work fine;
for mainnet, use a Linux host. On Windows, keep the checkout (or `DATA_ROOT`) inside the WSL
filesystem: data on a Windows drive (`C:\`, `/mnt/c`) is slower still.

Other arm64 hosts (Linux on Raspberry Pi or Graviton, for example) are not affected: every image this
repo uses is published for linux/arm64.
