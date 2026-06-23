# vmagent: sharding emulator

A small web tool to visualise how vmagent's `-remoteWrite.shardByURL` distributes
time series across shards. Useful for planning horizontally scalable stream
aggregation topologies, confirming a sharding key spreads series evenly and
keeps logically-grouped series (e.g. all `le` buckets of one histogram) together
on a single shard.

## Run

```bash
make run
```

Open the URL, paste time series one per line, no values/timestamps. For example:
```
http_request_total{pod="a", path="foo"}
http_request_total{pod="b", path="foo"}
http_request_total{pod="c", path="foo"}
```

Choose the sharding key (`Without` => `shardByURL.ignoreLabels`, `By` => `shardByURL.labels`),
set the shard count, and read off the distribution.

![img.png](img.png)

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

### Modelling choices

- **Label order.** Labels are hashed in the order written, with `__name__` first.
  This matches vmagent's default (it does not sort labels unless `-sortLabels` is
  set), so `m{a,b}` and `m{b,a}` may hash differently — as they would in vmagent.