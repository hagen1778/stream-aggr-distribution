package main

import (
	"sort"
	"strconv"
	"strings"
)

// aggregateLabels computes the output series identity produced by a stream
// aggregation `by`/`without` grouping:
//
//   - by:      keep only the labels whose name is in set.
//   - without: keep all labels except those in set.
//
// The __name__ label is aggregated away unless it is explicitly listed in a
// `by` set. It is never kept in `without` mode (you can only list labels to
// drop there, so there is no way to ask for __name__ to be preserved).
func aggregateLabels(labels []Label, mode string, set map[string]struct{}) []Label {
	out := make([]Label, 0, len(labels))
	for _, l := range labels {
		_, inSet := set[l.Name]
		keep := false
		if mode == "by" {
			keep = inSet
		} else { // without
			keep = !inSet && l.Name != "__name__"
		}
		if keep {
			out = append(out, l)
		}
	}
	return out
}

// seriesKey returns an order-independent identity for a label set, so two series
// that differ only in label order are treated as the same output series.
func seriesKey(labels []Label) string {
	sorted := append([]Label(nil), labels...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	for _, l := range sorted {
		b.WriteString(l.Name)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(l.Value))
		b.WriteByte(',')
	}
	return b.String()
}

// formatSeries renders a label set as `name{k="v",...}` (or `{k="v",...}` when
// __name__ was aggregated away). Labels are sorted by name for stable display.
func formatSeries(labels []Label) string {
	name := ""
	rest := make([]Label, 0, len(labels))
	for _, l := range labels {
		if l.Name == "__name__" {
			name = l.Value
			continue
		}
		rest = append(rest, l)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Name < rest[j].Name })

	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('{')
	for i, l := range rest {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(l.Name)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(l.Value))
	}
	b.WriteByte('}')
	return b.String()
}
