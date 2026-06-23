package main

import (
	"fmt"
	"strings"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/promutil"
)

// parseSeries parses a single Prometheus-style series selector without value or
// timestamp, e.g. `http_requests_total{instance="localhost",path="/metrics"}`.
//
// It delegates to VictoriaMetrics' promutil.NewLabelsFromString, which uses the
// same Prometheus text parser vmagent relies on. The metric name becomes the
// __name__ label, placed first; remaining labels keep the parser's order, which
// is what vmagent hashes when -sortLabels is not set.
func parseSeries(line string) ([]Label, error) {
	s := strings.TrimSpace(line)
	if s == "" {
		return nil, fmt.Errorf("empty line")
	}

	lbls, err := promutil.NewLabelsFromString(s)
	if err != nil {
		return nil, err
	}

	out := make([]Label, 0, len(lbls.Labels))
	for _, l := range lbls.Labels {
		out = append(out, Label{Name: l.Name, Value: l.Value})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("series has neither metric name nor labels")
	}
	return out, nil
}

// parseLabelSet splits a comma-separated label list into a set.
func parseLabelSet(s string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, part := range strings.Split(s, ",") {
		p := strings.TrimSpace(part)
		if p != "" {
			set[p] = struct{}{}
		}
	}
	return set
}
