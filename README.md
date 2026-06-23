# vmagent: sharding emulator

A small web tool to visualise how vmagent's `-remoteWrite.shardByURL` distributes
time series across shards. Useful for planning horizontally scalable stream
aggregation topologies — confirming a sharding key spreads series evenly and
keeps logically-grouped series (e.g. all `le` buckets of one histogram) together
on a single shard.

## Run

```bash
go run .                 # serves on http://localhost:8080
go run . -listenAddr :9000
```

Open the URL, paste time series (one per line, no values/timestamps), choose the
sharding key (`Without` → `shardByURL.ignoreLabels`, `By` → `shardByURL.labels`),
set the shard count, and read off the distribution.

## Faithfulness

The shard assignment is computed with VictoriaMetrics' **exact** code path:

- `consistenthash.ConsistentHash` — rendezvous / highest-random-weight selection
  (**not** modulo), seed `0` — **imported directly** from
  `github.com/VictoriaMetrics/VictoriaMetrics/lib/consistenthash`, so it tracks
  the upstream implementation rather than a copy.
- `getLabelsHashForShard` — concatenates `name+value` per label with **no
  separators** and hashes with XXH64. Copied into `shard.go` (with attribution)
  because it lives in the internal `app/vmagent/remotewrite` package and cannot
  be imported; it uses the same `github.com/cespare/xxhash/v2` version vmagent pins.
- The label filtering and replica fan-out from `shardAmountRemoteWriteCtx` —
  likewise copied from the internal package.

`shard_test.go` checks the XXH64 reference vectors, the hash byte layout, the
`by`/`without` filtering, and the histogram-co-location invariant.

### Modelling choices (documented, not bugs)

- **Node identity.** vmagent builds node IDs as `"<i+1>:<url>"`. The UI drives
  sharding by shard count alone, so the URL part is empty (`"1:"`, `"2:"`, …) —
  the algorithm and distribution are identical, only the absolute indices differ
  from a specific cluster. (The `/api/shard` endpoint still accepts an optional
  `urls` field for cluster-exact indices.)
- **Label order.** Labels are hashed in the order written, with `__name__` first.
  This matches vmagent's default (it does not sort labels unless `-sortLabels` is
  set), so `m{a,b}` and `m{b,a}` may hash differently — as they would in vmagent.
- **All targets healthy.** We model the path where every remote-write queue is
  healthy (the relevant case for planning); shard index then equals the node
  index from the consistent hash.

## Files

- `shard.go` — copied VM sharding primitives.
- `parser.go` — parses `name{label="value",...}` selectors.
- `main.go` — HTTP server + `/api/shard` JSON endpoint + embedded UI.
- `static/` — single-page UI (`index.html`, `app.js`, `styles.css`).
