package main

import (
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/consistenthash"
	"github.com/cespare/xxhash/v2"
)

// This file reproduces vmagent's sharding logic so the helper computes exactly
// the same shard assignment a real vmagent would.
//
// The rendezvous (highest-random-weight) shard selection is imported directly
// from VictoriaMetrics' lib/consistenthash. The label hashing and label
// filtering below are copied from app/vmagent/remotewrite/remotewrite.go, which
// is an internal (unexported) package and cannot be imported.
//
// The only intentional simplification: we model the "all remote-write targets
// healthy" path of shardAmountRemoteWriteCtx (no blocked queues), which is the
// case relevant for topology planning. In that path the shard index equals the
// node index returned by the consistent hash.

// Label is a single (name, value) pair, equivalent to prompb.Label.
type Label struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// getLabelsHashForShard is copied from app/vmagent/remotewrite/remotewrite.go.
//
// It deliberately omits the '=' separator between label name and value and uses
// no separator between labels. Changing it would re-shard all series across
// remoteWrite targets, so this exact byte layout matters.
func getLabelsHashForShard(labels []Label) (uint64, string) {
	var b []byte
	for _, label := range labels {
		b = append(b, label.Name...)
		b = append(b, label.Value...)
	}
	return xxhash.Sum64(b), string(b)
}

// filterShardLabels reproduces the label selection in shardAmountRemoteWriteCtx.
//
// mode "by":      keep only labels whose name is in set (shardByURL.labels).
// mode "without": keep all labels except those in set (shardByURL.ignoreLabels).
// Empty set in either mode mirrors vmagent: "by" with no labels hashes the empty
// string (everything collapses to one shard); "without" with no labels keeps all
// labels (default even distribution over the full label set).
//
// Label order is preserved exactly as given, because vmagent does not sort labels
// before hashing unless -sortLabels is set.
func filterShardLabels(labels []Label, mode string, set map[string]struct{}) []Label {
	if len(set) == 0 {
		if mode == "by" {
			return nil
		}
		return labels
	}
	out := make([]Label, 0, len(labels))
	for _, label := range labels {
		_, inSet := set[label.Name]
		if mode == "by" {
			if inSet {
				out = append(out, label)
			}
		} else { // without
			if !inSet {
				out = append(out, label)
			}
		}
	}
	return out
}

// assignShards returns the shard indexes a series is routed to, mirroring the
// replica fan-out loop in shardAmountRemoteWriteCtx (append to shardIdx, then
// shardIdx++ with wrap-around, `replicas` times). With all targets healthy the
// shard index equals the node index from the consistent hash.
func assignShards(ch *consistenthash.ConsistentHash, h uint64, numShards, replicas int) []int {
	if replicas <= 0 {
		replicas = 1
	}
	if replicas > numShards {
		replicas = numShards
	}
	shardIdx := ch.GetNodeIdx(h, nil)
	res := make([]int, 0, replicas)
	for {
		res = append(res, shardIdx)
		if len(res) >= replicas {
			break
		}
		shardIdx++
		if shardIdx >= numShards {
			shardIdx = 0
		}
	}
	return res
}
