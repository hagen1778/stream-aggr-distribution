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
