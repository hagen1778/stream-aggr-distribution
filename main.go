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
	URLs      string `json:"urls"`     // optional, one -remoteWrite.url per line
	Replicas  int    `json:"replicas"` // optional, defaults to 1
}

type seriesResult struct {
	Raw        string  `json:"raw"`
	OK         bool    `json:"ok"`
	Error      string  `json:"error,omitempty"`
	HashLabels []Label `json:"hashLabels,omitempty"`
	HashInput  string  `json:"hashInput,omitempty"`
	Hash       string  `json:"hash,omitempty"`
	Primary    int     `json:"primary"`
	Shards     []int   `json:"shards,omitempty"`
}

type shardResponse struct {
	NumShards int            `json:"numShards"`
	Mode      string         `json:"mode"`
	Labels    []string       `json:"labels"`
	Replicas  int            `json:"replicas"`
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
	if req.NumShards > 100000 {
		writeJSONError(w, "number of shards is unreasonably large (max 100000)")
		return
	}
	mode := req.Mode
	if mode != "by" {
		mode = "without"
	}
	replicas := req.Replicas
	if replicas < 1 {
		replicas = 1
	}

	// Build node identifiers exactly as vmagent does: "<idx+1>:<url>".
	// URLs are optional; when omitted the URL part is empty, which keeps the
	// algorithm faithful while indices stay deterministic.
	urls := splitNonEmptyLines(req.URLs)
	nodes := make([]string, req.NumShards)
	for i := 0; i < req.NumShards; i++ {
		url := ""
		if i < len(urls) {
			url = urls[i]
		}
		nodes[i] = fmt.Sprintf("%d:%s", i+1, url)
	}
	ch := consistenthash.NewConsistentHash(nodes, 0)

	labelSet := parseLabelSet(req.Labels)
	labelList := make([]string, 0, len(labelSet))
	for _, p := range strings.Split(req.Labels, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			labelList = append(labelList, p)
		}
	}

	resp := shardResponse{
		NumShards: req.NumShards,
		Mode:      mode,
		Labels:    labelList,
		Replicas:  replicas,
		Nodes:     nodes,
		PerShard:  make([]int, req.NumShards),
	}

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
		hashLabels := filterShardLabels(labels, mode, labelSet)
		h, hashInput := getLabelsHashForShard(hashLabels)
		shards := assignShards(ch, h, req.NumShards, replicas)

		res.OK = true
		res.HashLabels = hashLabels
		res.HashInput = hashInput
		res.Hash = fmt.Sprintf("0x%016x", h)
		res.Primary = shards[0]
		res.Shards = shards
		for _, s := range shards {
			resp.PerShard[s]++
		}
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

func splitNonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
