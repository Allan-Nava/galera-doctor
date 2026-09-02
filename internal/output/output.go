// Package output renders audits. Three renderers, one rule: worst first, and
// every line traceable to the node it came from.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/Allan-Nava/galera-doctor/internal/audit"
	"github.com/Allan-Nava/galera-doctor/internal/finding"
)

// Text renders audits for a terminal.
func Text(w io.Writer, reps []audit.Report, min finding.Status) error {
	sort.SliceStable(reps, func(i, j int) bool {
		if a, b := finding.Severity(reps[i].Worst()), finding.Severity(reps[j].Worst()); a != b {
			return a > b
		}
		return reps[i].Cluster < reps[j].Cluster
	})
	for _, rep := range reps {
		if _, err := fmt.Fprintf(w, "%-5s %s  %d node(s)\n", rep.Worst(), rep.Cluster, len(rep.Nodes)); err != nil {
			return err
		}
		for _, f := range rep.Findings {
			if min != "" && !finding.AtLeast(f.Status, min) {
				continue
			}
			if _, err := fmt.Fprintf(w, "  %-5s %-24s %-14s %s\n", f.Status, f.Check, f.Target, f.Message); err != nil {
				return err
			}
			if f.Hint != "" {
				if _, err := fmt.Fprintf(w, "        ↳ %s\n", f.Hint); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Summary is the one line at the end.
func Summary(w io.Writer, reps []audit.Report) error {
	var counts map[finding.Status]int = map[finding.Status]int{}
	var nodes int
	for _, r := range reps {
		nodes += len(r.Nodes)
		for _, f := range r.Findings {
			counts[f.Status]++
		}
	}
	_, err := fmt.Fprintf(w, "\n%d cluster(s), %d node(s): %d ERROR, %d BAD, %d WARN, %d OK\n",
		len(reps), nodes,
		counts[finding.ERROR], counts[finding.BAD], counts[finding.WARN], counts[finding.OK])
	return err
}

// JSON renders everything.
func JSON(w io.Writer, reps []audit.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Tool    string         `json:"tool"`
		Reports []audit.Report `json:"reports"`
	}{Tool: "galera-doctor", Reports: reps})
}

// Findings renders the flat findings array. An empty run emits `[]`, never
// `null`: a consumer iterating the array must not have to special-case a
// healthy cluster.
func Findings(w io.Writer, reps []audit.Report, min finding.Status) error {
	out := []finding.Finding{}
	for _, r := range reps {
		for _, f := range r.Findings {
			if min != "" && !finding.AtLeast(f.Status, min) {
				continue
			}
			out = append(out, f)
		}
	}
	finding.SortWorstFirst(out)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
