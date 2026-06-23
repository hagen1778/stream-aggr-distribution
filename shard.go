package main

import "github.com/cespare/xxhash/v2"

// This file reproduces vmagent's sharding logic byte-for-byte so the helper
// computes exactly the same shard assignment a real vmagent would.
//
// Sources (VictoriaMetrics):
//   - lib/consistenthash/consistent_hash.go        -> ConsistentHash, GetNodeIdx, fastHashUint64
//   - app/vmagent/remotewrite/remotewrite.go        -> getLabelsHashForShard, shardAmountRemoteWriteCtx
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

// ConsistentHash is copied verbatim from lib/consistenthash/consistent_hash.go.
// It implements rendezvous (highest-random-weight) hashing, NOT modulo.
type ConsistentHash struct {
	hashSeed   uint64
	nodeHashes []uint64
}

// NewConsistentHash creates a consistent hash based on the nodes.
func NewConsistentHash(nodes []string, hashSeed uint64) *ConsistentHash {
	nodeHashes := make([]uint64, len(nodes))
	for i, node := range nodes {
		nodeHashes[i] = xxhash.Sum64([]byte(node))
	}
	return &ConsistentHash{
		hashSeed:   hashSeed,
		nodeHashes: nodeHashes,
	}
}

// GetNodeIdx returns the node index that the input hash value should belong to.
func (rh *ConsistentHash) GetNodeIdx(h uint64, excludeIdxs []int) int {
	var mMax uint64
	var idx int
	h ^= rh.hashSeed

	if len(excludeIdxs) == len(rh.nodeHashes) {
		// All the nodes are excluded. Treat this case as no nodes are excluded.
		excludeIdxs = nil
	}

next:
	for i, nh := range rh.nodeHashes {
		for _, j := range excludeIdxs {
			if i == j {
				continue next
			}
		}
		if m := fastHashUint64(nh ^ h); m > mMax {
			mMax = m
			idx = i
		}
	}
	return idx
}

func fastHashUint64(x uint64) uint64 {
	x ^= x >> 12 // a
	x ^= x << 25 // b
	x ^= x >> 27 // c
	return x * 2685821657736338717
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
func assignShards(ch *ConsistentHash, h uint64, numShards, replicas int) []int {
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
