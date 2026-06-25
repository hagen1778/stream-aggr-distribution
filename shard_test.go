package main

import (
	"fmt"
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/consistenthash"
	"github.com/cespare/xxhash/v2"
)

// Confirms the xxhash dependency matches the canonical XXH64 reference vector,
// i.e. it is byte-compatible with the one vmagent uses.
func TestXXHashReferenceVector(t *testing.T) {
	if got := xxhash.Sum64([]byte("")); got != 0xef46db3751d8e999 {
		t.Fatalf("XXH64(\"\") = %#x, want 0xef46db3751d8e999", got)
	}
	if got := xxhash.Sum64([]byte("abc")); got != 0x44bc2cf5ad770999 {
		t.Fatalf("XXH64(\"abc\") = %#x, want 0x44bc2cf5ad770999", got)
	}
}

func TestLabelsHashLayout(t *testing.T) {
	// getLabelsHashForShard concatenates name+value with no separators.
	h := getLabelsHashForShard([]Label{{"__name__", "m"}, {"a", "1"}})
	if want := xxhash.Sum64([]byte("__name__ma1")); h != want {
		t.Fatalf("hash = %#x, want %#x", h, want)
	}
}

func TestByVsWithoutFiltering(t *testing.T) {
	labels := []Label{{"__name__", "m"}, {"le", "0.1"}, {"instance", "x"}}
	by := filterShardLabels(labels, "by", map[string]struct{}{"instance": {}})
	if len(by) != 1 || by[0].Name != "instance" {
		t.Fatalf("by[instance] = %+v", by)
	}
	without := filterShardLabels(labels, "without", map[string]struct{}{"le": {}})
	if len(without) != 2 || without[0].Name != "__name__" || without[1].Name != "instance" {
		t.Fatalf("without[le] = %+v", without)
	}
	// empty "by" hashes nothing; empty "without" keeps everything.
	if got := filterShardLabels(labels, "by", nil); got != nil {
		t.Fatalf("by[] = %+v, want nil", got)
	}
	if got := filterShardLabels(labels, "without", nil); len(got) != 3 {
		t.Fatalf("without[] kept %d labels, want 3", len(got))
	}
}

// The whole histogram (all le buckets of one series identity) must land on one
// shard when le is excluded from the key. This is the invariant behind the
// "shard histograms by everything except le" recommendation.
func TestHistogramBucketsColocateWhenLeIgnored(t *testing.T) {
	const n = 20
	nodes := make([]string, n)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("%d:", i+1)
	}
	ch := consistenthash.NewConsistentHash(nodes, 0)

	mkBucket := func(le string) []Label {
		return []Label{
			{"__name__", "grpc_server_handling_seconds_bucket"},
			{"grpc_method", "Get"},
			{"grpc_service", "Store"},
			{"job", "api"},
			{"instance", "host1"},
			{"le", le},
		}
	}
	ignoreLe := map[string]struct{}{"le": {}}
	shardOf := func(le string) int {
		f := filterShardLabels(mkBucket(le), "without", ignoreLe)
		h := getLabelsHashForShard(f)
		return assignShard(ch, h)
	}

	base := shardOf("0.1")
	for _, le := range []string{"0.5", "1", "5", "+Inf"} {
		if got := shardOf(le); got != base {
			t.Fatalf("bucket le=%s went to shard %d, want %d (histogram split across shards)", le, got, base)
		}
	}
}

func TestAggregateLabels(t *testing.T) {
	labels := []Label{{"__name__", "m"}, {"pod", "a"}, {"path", "foo"}}

	// by: keep only listed labels; __name__ kept only if explicitly listed.
	by := aggregateLabels(labels, "by", map[string]struct{}{"path": {}})
	if len(by) != 1 || by[0].Name != "path" {
		t.Fatalf("by[path] = %+v", by)
	}
	byName := aggregateLabels(labels, "by", map[string]struct{}{"__name__": {}, "path": {}})
	if len(byName) != 2 || byName[0].Name != "__name__" {
		t.Fatalf("by[__name__,path] = %+v", byName)
	}

	// without: drop listed labels and always drop __name__.
	without := aggregateLabels(labels, "without", map[string]struct{}{"pod": {}})
	if len(without) != 1 || without[0].Name != "path" {
		t.Fatalf("without[pod] = %+v (expected only path; __name__ must be dropped)", without)
	}
}

// Aggregating away the shard key produces duplicates: series that landed on
// different shards collapse to the same identity, so each shard emits it.
func TestAggregationDuplicatesAcrossShards(t *testing.T) {
	const n = 3
	nodes := make([]string, n)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("%d:", i+1)
	}
	ch := consistenthash.NewConsistentHash(nodes, 0)

	// Three series differing only by pod, sharded over all labels (without {}).
	series := [][]Label{
		{{"__name__", "http_request_total"}, {"pod", "a"}, {"path", "foo"}},
		{{"__name__", "http_request_total"}, {"pod", "b"}, {"path", "foo"}},
		{{"__name__", "http_request_total"}, {"pod", "c"}, {"path", "foo"}},
	}
	shardOf := func(l []Label) int {
		return assignShard(ch, getLabelsHashForShard(filterShardLabels(l, "without", nil)))
	}

	// Aggregate `without pod` -> every series collapses to {path="foo"}.
	identityShards := map[string]map[int]struct{}{}
	for _, l := range series {
		agg := aggregateLabels(l, "without", map[string]struct{}{"pod": {}})
		k := seriesKey(agg)
		if identityShards[k] == nil {
			identityShards[k] = map[int]struct{}{}
		}
		identityShards[k][shardOf(l)] = struct{}{}
	}

	if len(identityShards) != 1 {
		t.Fatalf("expected 1 aggregated identity, got %d", len(identityShards))
	}
	for k, shards := range identityShards {
		if len(shards) < 2 {
			t.Fatalf("identity %q emitted by %d shard(s); expected duplicates across shards", k, len(shards))
		}
	}
}
