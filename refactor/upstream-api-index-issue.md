# Upstream tracking: slow rebuild of `nft_events_asset_identifier_index` (stacks-blockchain-api)

**Status:** tracking only. **No issue or PR opened** in `hirosystems/stacks-blockchain-api` yet; this
document collects the evidence and a proposed mitigation to take upstream later.

**Found:** 2026-10-28 while seeding mainnet with the containerized seed job (refactor/HANDOFF "Phase 3c",
[15](./15-verification-log.md) "Phase 3c").

## Summary

Restoring the Hiro archive's mainnet API dump spends **more than 19 hours on a single index**,
`nft_events_asset_identifier_index`. It is a **hash** index over a column whose values are extremely
skewed in current mainnet data. A logical restore (`pg_restore`) rebuilds every index, so anyone
seeding an API from the archive hits this, with or without this repo's tooling. While the restore
runs, the node and API are down.

## Environment

| Item | Value |
|---|---|
| API | `hirosystems/stacks-blockchain-api:9.7.0` |
| Postgres | `postgres:17-alpine` (restore server: `fsync=off`, `maintenance_work_mem=1GB`, default `shared_buffers`) |
| Dump | `stacks-blockchain-api-pg-17-9.7.0-20261027.dump` (98.8 GiB, custom format), from `archive.hiro.so/mainnet` |
| Restore | `pg_restore --no-owner --no-privileges --create -j 8` |
| Host | Linux x64, i9-13900H (20 threads), 62 GiB RAM, NVMe |

## The index

Defined in the original `nft_events` migration, `migrations/1600000000005_nft_events.ts`:

```ts
pgm.createIndex('nft_events', 'asset_identifier', {
  where: 'asset_event_type_id = 2',   // DbAssetEventTypeId.Mint
  method: 'hash',
});
```

The restore runs it as:

```sql
CREATE INDEX nft_events_asset_identifier_index ON stacks_blockchain_api.nft_events
  USING hash (asset_identifier) WHERE (asset_event_type_id = 2);
```

**Used by:** `PgStore.getNftMints` in `src/datastore/pg-store.ts`, the "NFT mints for an asset
identifier" query (`WHERE nft.asset_identifier = $1 AND nft.asset_event_type_id = 2 … ORDER BY
nft.block_height DESC, txs.microblock_sequence DESC, txs.tx_index DESC, nft.event_index DESC`).

## What was observed [measured]

| Measurement | Value |
|---|---|
| `nft_events` size | 42 GB, about 56.3 M rows |
| Rows covered by the index (`asset_event_type_id = 2`) | about 30.0 M (`pg_stat_progress_create_index.tuples_total = 30 037 225`) |
| Distinct `asset_identifier` values (1% `TABLESAMPLE`) | **783** among 301 687 sampled rows |
| Top values (1% sample) | `SM1FKXGNZJWSTWDWXQZJNF7B5TV5ZB235JTCXYXKD.dlmm-pool-stx-usdcx-v-1-bps-10::pool-token-id` 77 705; `…dlmm-pool-stx-sbtc-v-1-bps-15::pool-token-id` 70 527; `…dlmm-pool-stx-usdcx-v-2-bps-10::pool-token-id` 62 228; `…dlmm-pool-sbtc-usdcx-v-1-bps-10::pool-token-id` 34 720; `SPVD6CE8RW90BGGKJZTKCSMGKS7HP0K8364TFR48.bitcoin-faces-airdrop::bitcoin-faces` 8 208. **The top three hold about 70%** of the indexed rows |
| Build progress | Table scan finished quickly (`blocks_done = blocks_total`). Then 7.0 M of 30.0 M tuples after about 2 h 25 m, and **48.2% after 8 h 50 m**. The rate fell from about 800 to about 300 tuples/s at times (it varies with the values being inserted) |
| Resources during the build | **One CPU core at 100%**; no significant I/O wait. Other `pg_restore` work had finished |
| Projection | **18 h or more** for this index alone; the run was stopped at 48% |
| **Measured, full build** (deferred, `CREATE INDEX CONCURRENTLY` on the running stack, 2026-10-29) | **19 h 25 m 39 s** (00:23:35 → 19:49:14 UTC); index valid, **2.1 GB** |

For comparison, everything else in the same dump (all other tables and indexes) restored in about
2.5 h with `-j 8`. The slowest other step was the single-threaded `COPY` of `event_observer_requests`.

The skewed values are canonical on-chain data (DLMM liquidity-pool position NFTs and similar), so the
data itself is correct. The problem is the index type chosen for it.

## Why it is slow [measured structure, inferred mechanism]

A PostgreSQL hash index maps each distinct value to one bucket. With three values holding about 21 M
of the 30 M rows, those buckets grow very long **overflow page chains**, and inserting a tuple
appears to walk its bucket's chain. The build then degrades towards quadratic behaviour and is
bound by a single core. This matches the observations: CPU-bound, no I/O wait, and a rate that drops
as the chains grow.

`pgstathashindex('nft_events_asset_identifier_index')` on the built index:

| bucket pages | overflow pages | live items | free space |
|---|---|---|---|
| 196 608 | **73 582** | 30 214 205 | **72.6%** |

So about 196 k buckets for fewer than 1 000 distinct values: nearly all buckets are empty (72.6% free
space overall), while a few carry chains totalling 73.6 k overflow pages. The index is **2.1 GB**,
against 226 MB for a B-tree over the same rows.

The same structure probably also slows ordinary inserts for those identifiers during normal API
ingestion (each new mint for a hot identifier extends a long chain). **Not measured.**

## API behaviour while the index is missing [measured]

From the mainnet stack running on the seed with the index deferred (2026-10-28):

| Request | Time |
|---|---|
| `/extended/v1/block?limit=1` | 0.22 s |
| `/extended/v1/tokens/nft/mints?asset_identifier=SPVD6CE8RW90BGGKJZTKCSMGKS7HP0K8364TFR48.bitcoin-faces-airdrop::bitcoin-faces&limit=5` | **39.4 s** (`total` 792 097) |
| `/extended/v1/tokens/nft/mints?asset_identifier=SM1FKXGNZJWSTWDWXQZJNF7B5TV5ZB235JTCXYXKD.dlmm-pool-stx-usdcx-v-1-bps-10::pool-token-id&limit=5` | **>60 s** (client timeout) |

**The API doesn't cancel its database query when the HTTP client disconnects.** The timed-out request's query was still running 74 s later. Repeated calls can therefore pile up full scans. This is a separate, smaller upstream point: tie the query to the request's lifetime or set a `statement_timeout`. The query also computes `COUNT(*) OVER()` over every matching row, which is expensive for hot identifiers even with an index.

## Index options compared [measured]

On the same mainnet database (2026-10-29), with the `getNftMints` query exactly as API 9.7.0 sends it
(`nft.*`, `COUNT(*) OVER()`, join to `txs`, `ORDER BY … LIMIT 5`). Each option ran twice in a
transaction with the other indexes dropped (then rolled back); the second run is shown. The stack was
running, so the cache state varies between rows: compare orders of magnitude, not single seconds.

| Index used by the plan | Build | Size | `bitcoin-faces` (792 k mints) | DLMM `usdcx-v-1` (7.78 M mints) |
|---|---|---|---|---|
| hash `(asset_identifier)` partial (as shipped) | **19 h 25 m** | 2.1 GB | 2.1 s | 45.4 s |
| none: existing B-tree `(asset_identifier, value, …)` (`nft_events_asset_value_position_index`) | — | — | 1.8 s | 22.3 s |
| B-tree `(asset_identifier)` partial | **33 s** | 226 MB | 2.0 s | 42.0 s |
| B-tree `(asset_identifier, block_height DESC)` partial | **38 s** | 274 MB | 1.5 s | 30.9 s |

- **The hash index doesn't make `getNftMints` faster** than the existing B-tree, and a B-tree
  replacement builds in about 35 s instead of 19 h.
- **No index makes hot identifiers fast**: `COUNT(*) OVER()` (the `total` in the response) forces the
  query to read and join every matching row (7.78 M joins to `txs` for the DLMM identifier) before
  `LIMIT 5`. Even the index in `ORDER BY` order can't stop early. That is a query change (a separate
  or capped count), not an index change.
- Earlier, with the index missing and a cold cache, the same requests took 39 s and > 60 s (above).

## Impact

- **Seeding/restoring the API from the archive takes about a day longer** than it otherwise would,
  and the node and API are unavailable meanwhile (a seed must restore everything before starting).
- Affects anyone using the Hiro archive dumps: this repo's `seed` job, the older
  `scripts/seed-chainstate.sh`, or a manual `pg_restore`.
- Likely an ingestion cost for hot identifiers during normal operation (unmeasured).

## Proposed mitigation (upstream)

1. **Replace the hash index with a partial B-tree index** on the same column and predicate (measured:
   33 s to build, 226 MB, same query speed):

   ```sql
   CREATE INDEX nft_events_asset_identifier_mint_index ON nft_events (asset_identifier)
     WHERE asset_event_type_id = 2;
   ```

   - **Why it helps:** B-tree indexes (PostgreSQL ≥ 13) **deduplicate** repeated keys and build by
     sorting, so a skewed column costs about the same as any other.
   - **Possibly better:** match the query's `ORDER BY` so `getNftMints` can read in index order, e.g.
     `(asset_identifier, block_height DESC) WHERE asset_event_type_id = 2`.
   - **As a migration:** create the B-tree index `CONCURRENTLY`, then drop the hash index.
2. **Or drop it.** The existing B-tree `nft_events_asset_value_position_index` (leading column
   `asset_identifier`) served `getNftMints` as fast as the hash index in the comparison above.
3. **For hot identifiers, change the count:** `COUNT(*) OVER()` costs a full read and join of every
   mint of the identifier on every page request. A separate (cacheable or capped) count would let the
   `(asset_identifier, block_height DESC)` index stop after `LIMIT` rows.
4. **For the archive (independent of the index):** also publish a **physical** backup (a tarball of
   the data directory, like the chainstate archive) next to the logical dump. Restoring would then be
   extraction only, with no index rebuilds, at the cost of requiring the exact Postgres major
   version.

## Mitigation in this repo (until upstream changes)

- `docker compose run --rm seed --defer-index nft_events_asset_identifier_index` (or
  `SEED_DEFER_INDEXES=…` in `.env`) restores everything except this index and records it in
  `sbd_seed.deferred_indexes`. The `build-indexes` job (api profile) then builds it with
  `CREATE INDEX CONCURRENTLY` while the stack runs, and cleans up an invalid leftover from an
  interrupted build. Verified on testnet and on mainnet: a 2 h restore, then node and API up 39 s after start while the index builds in the background (15 "Phase 3c mainnet seed").
- Trade-off: until the build finishes, `getNftMints` (NFT mints by asset identifier) may be slow;
  everything else is unaffected.

## To verify before filing upstream

- [x] Build time of the proposed partial B-tree index on the same mainnet data: **33 s** (38 s with `block_height DESC`).
- [x] `EXPLAIN ANALYZE` of `getNftMints` for hot identifiers with the hash index, the B-tree options and
      neither: no meaningful difference; `COUNT(*) OVER()` dominates.
- [x] Overflow chains: `pgstathashindex()` shows 73.6 k overflow pages and 72.6% free space (the
      per-insert chain walk is still inferred, not traced).
- [ ] Measure insert cost into `nft_events` for a hot identifier with and without the hash index.
- [x] Final build time of the hash index from the deferred build: **19 h 25 m 39 s**.
- [ ] Check whether newer API releases already changed this index.

## Related observation (separate topic)

`event_observer_requests` (the API's raw event log) is the largest single `COPY` in the restore (about
2 h, single-threaded). Migration `1779800000015_burn-blocks` backfills `burn_blocks` from it, so it
can't simply be skipped when the dump is older than the pinned API. `STACKS_API_STORE_RAW_EVENTS`
(default `true`) controls whether the API keeps writing it. Worth a separate note upstream on whether
archive dumps need the full raw log.
