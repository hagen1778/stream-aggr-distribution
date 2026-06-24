package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
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
	Mode      string `json:"mode"`   // "by" | "without"
	Labels    string `json:"labels"` // comma-separated
	NumShards int    `json:"numShards"`
}

type seriesResult struct {
	Raw     string `json:"raw"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Primary int    `json:"primary"`
}

type shardResponse struct {
	NumShards int            `json:"numShards"`
	Nodes     []string       `json:"nodes"`
	Results   []seriesResult `json:"results"`
	PerShard  []int          `json:"perShard"`
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
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeJSONError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
