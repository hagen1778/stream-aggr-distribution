package main

import (
	"fmt"
	"strings"
)

// parseSeries parses a single Prometheus-style series selector without value or
// timestamp, e.g. `http_requests_total{instance="localhost",path="/metrics"}`.
//
// The metric name becomes the __name__ label, placed first (as VictoriaMetrics
// holds it internally). Remaining labels keep the order they were written in,
// which is what vmagent hashes when -sortLabels is not set.
func parseSeries(line string) ([]Label, error) {
	s := strings.TrimSpace(line)
	if s == "" {
		return nil, fmt.Errorf("empty line")
	}

	br := strings.IndexByte(s, '{')
	if br < 0 {
		name := strings.TrimSpace(s)
		if err := validateMetricName(name); err != nil {
			return nil, err
		}
		return []Label{{Name: "__name__", Value: name}}, nil
	}

	name := strings.TrimSpace(s[:br])
	if name != "" {
		if err := validateMetricName(name); err != nil {
			return nil, err
		}
	}

	inner := strings.TrimRight(s[br+1:], " \t")
	if !strings.HasSuffix(inner, "}") {
		return nil, fmt.Errorf("missing closing '}'")
	}
	inner = inner[:len(inner)-1]

	labels := make([]Label, 0, 8)
	if name != "" {
		labels = append(labels, Label{Name: "__name__", Value: name})
	}

	i := 0
	skipSpaces := func() {
		for i < len(inner) && (inner[i] == ' ' || inner[i] == '\t') {
			i++
		}
	}

	skipSpaces()
	for i < len(inner) {
		// label name
		start := i
		for i < len(inner) && isLabelNameChar(inner[i]) {
			i++
		}
		key := inner[start:i]
		if key == "" {
			return nil, fmt.Errorf("expected label name at position %d", i)
		}
		skipSpaces()
		if i >= len(inner) || inner[i] != '=' {
			return nil, fmt.Errorf("expected '=' after label %q", key)
		}
		i++ // '='
		skipSpaces()
		if i >= len(inner) || inner[i] != '"' {
			return nil, fmt.Errorf("expected '\"' for value of label %q", key)
		}
		i++ // opening quote

		var sb strings.Builder
		closed := false
		for i < len(inner) {
			c := inner[i]
			if c == '\\' {
				i++
				if i >= len(inner) {
					break
				}
				switch inner[i] {
				case 'n':
					sb.WriteByte('\n')
				case 't':
					sb.WriteByte('\t')
				case '\\':
					sb.WriteByte('\\')
				case '"':
					sb.WriteByte('"')
				default:
					sb.WriteByte(inner[i])
				}
				i++
				continue
			}
			if c == '"' {
				closed = true
				i++
				break
			}
			sb.WriteByte(c)
			i++
		}
		if !closed {
			return nil, fmt.Errorf("unterminated value for label %q", key)
		}
		labels = append(labels, Label{Name: key, Value: sb.String()})

		skipSpaces()
		if i >= len(inner) {
			break
		}
		if inner[i] != ',' {
			return nil, fmt.Errorf("expected ',' between labels at position %d", i)
		}
		i++ // ','
		skipSpaces()
		// allow trailing comma
		if i >= len(inner) {
			break
		}
	}

	if len(labels) == 0 {
		return nil, fmt.Errorf("series has neither metric name nor labels")
	}
	return labels, nil
}

func validateMetricName(name string) error {
	if name == "" {
		return fmt.Errorf("empty metric name")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if isLabelNameChar(c) || c == ':' {
			continue
		}
		return fmt.Errorf("invalid character %q in metric name %q", string(c), name)
	}
	return nil
}

func isLabelNameChar(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
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
