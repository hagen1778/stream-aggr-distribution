package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/consistenthash"
)

//go:embed static
var staticFiles embed.FS

var listenAddr = flag.String("listenAddr", ":8080", "TCP address for the web UI to listen on")

func main() {
	flag.Parse()

	staticRoot, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("cannot open embedded static dir: %s", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticRoot)))
	mux.HandleFunc("/api/shard", handleShard)

	log.Printf("stream-aggr-distribution helper listening on http://localhost%s", *listenAddr)
	if err := http.ListenAndServe(*listenAddr, mux); err != nil {
		log.Fatalf("server error: %s", err)
	}
}

type shardRequest struct {
	Series    string `json:"series"`
	Mode      string `json:"mode"`   // sharding key: "by" | "without"
	Labels    string `json:"labels"` // comma-separated sharding labels
	NumShards int    `json:"numShards"`
	AggMode   string `json:"aggMode"`   // aggregation: "by" | "without"
	AggLabels string `json:"aggLabels"` // comma-separated aggregation labels
}

type seriesResult struct {
	Raw     string `json:"raw"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Primary int    `json:"primary"`
}

// aggResult is one aggregated output series emitted by one shard. When the same
// identity is emitted by more than one shard, those entries are duplicates at
// the remote storage and are marked accordingly.
type aggResult struct {
	Display   string `json:"display"`   // formatted output series identity
	Shard     int    `json:"shard"`     // shard that emits it
	Duplicate bool   `json:"duplicate"` // emitted by >1 shard
	DupCount  int    `json:"dupCount"`  // number of shards emitting this identity
	DupShards []int  `json:"dupShards"` // those shards
}

type shardResponse struct {
	NumShards int            `json:"numShards"`
	Nodes     []string       `json:"nodes"`
	Results   []seriesResult `json:"results"`
	PerShard  []int          `json:"perShard"`
	Agg       []aggResult    `json:"agg"`
}

func handleShard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req shardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, fmt.Sprintf("invalid JSON: %s", err))
		return
	}

	if req.NumShards < 1 {
		writeJSONError(w, "number of shards must be >= 1")
		return
	}
	if req.NumShards > 1000 {
		writeJSONError(w, "number of shards is unreasonably large (max 1000)")
		return
	}
	mode := req.Mode
	if mode != "by" {
		mode = "without"
	}
	aggMode := req.AggMode
	if aggMode != "by" {
		aggMode = "without"
	}

	// Build node identifiers as vmagent does: "<idx+1>:<url>". The UI shards by
	// shard count alone, so the URL part is empty ("1:", "2:", …) — the algorithm
	// and distribution are identical, only the absolute indices differ from a
	// specific cluster's -remoteWrite.url ordering.
	nodes := make([]string, req.NumShards)
	for i := 0; i < req.NumShards; i++ {
		nodes[i] = fmt.Sprintf("%d:", i+1)
	}
	ch := consistenthash.NewConsistentHash(nodes, 0)

	resp := shardResponse{
		NumShards: req.NumShards,
		Nodes:     nodes,
		PerShard:  make([]int, req.NumShards),
	}

	shardBy := parseLabelSet(req.Labels)
	aggBy := parseLabelSet(req.AggLabels)

	// aggAccum tracks, per aggregated-output identity, which shards emit it.
	type aggAccum struct {
		labels []Label
		shards map[int]struct{}
	}
	aggByKey := make(map[string]*aggAccum)
	var aggOrder []string

	for _, line := range strings.Split(req.Series, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		res := seriesResult{Raw: line}
		labels, err := parseSeries(line)
		if err != nil {
			res.OK = false
			res.Error = err.Error()
			resp.Results = append(resp.Results, res)
			continue
		}
		hashLabels := filterShardLabels(labels, mode, shardBy)
		h := getLabelsHashForShard(hashLabels)
		shard := assignShard(ch, h)

		res.OK = true
		res.Hash = fmt.Sprintf("0x%016x", h)
		res.Primary = shard
		resp.PerShard[shard]++
		resp.Results = append(resp.Results, res)

		// Aggregate this series within its shard: a shard emits one output series
		// per distinct aggregation-key identity it sees.
		aggLabels := aggregateLabels(labels, aggMode, aggBy)
		key := seriesKey(aggLabels)
		acc := aggByKey[key]
		if acc == nil {
			acc = &aggAccum{labels: aggLabels, shards: make(map[int]struct{})}
			aggByKey[key] = acc
			aggOrder = append(aggOrder, key)
		}
		acc.shards[shard] = struct{}{}
	}

	// Emit one aggResult per (identity, shard). An identity emitted by more than
	// one shard yields duplicates at the remote storage.
	for _, key := range aggOrder {
		acc := aggByKey[key]
		shards := make([]int, 0, len(acc.shards))
		for s := range acc.shards {
			shards = append(shards, s)
		}
		sort.Ints(shards)
		display := formatSeries(acc.labels)
		dup := len(shards) > 1
		for _, s := range shards {
			resp.Agg = append(resp.Agg, aggResult{
				Display:   display,
				Shard:     s,
				Duplicate: dup,
				DupCount:  len(shards),
				DupShards: shards,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeJSONError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
