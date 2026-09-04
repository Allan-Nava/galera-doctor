// Package output renders audits. Three renderers, one rule: worst first, and
// every line traceable to the node it came from.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Allan-Nava/galera-doctor/internal/audit"
	"github.com/Allan-Nava/galera-doctor/internal/finding"
	"github.com/Allan-Nava/galera-doctor/internal/state"
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

// Changed renders only the findings whose status moved since the statuses in
// prev, keyed "check@target" the way the state file keys them. It returns how
// many it printed, and prints nothing at all when that is zero (GD-18).
//
// This is what watch mode outputs. The window it exists for is the one in
// which somebody is repairing a cluster: they ran the audit, they did
// something, and they want the effect. Reprinting twenty OK lines every ten
// seconds buries the one line that moved, so silence is the feature.
func Changed(w io.Writer, reps []audit.Report, prev map[string]string, now time.Time) (int, error) {
	type transition struct {
		cluster string
		before  string
		f       finding.Finding
	}
	var moved []transition
	for _, rep := range reps {
		for _, f := range rep.Findings {
			// The transition summary is about the transitions; including it
			// would make every tick a change.
			if f.Check == "audit/changes" {
				continue
			}
			// Namespaced by cluster, the way the state file namespaces
			// everything: two clusters can carry the same check and target,
			// and a collision would report a change that never happened.
			key := rep.Cluster + "/" + state.Key(f.Check, f.Target)
			before, seen := prev[key]
			if seen && before == string(f.Status) {
				continue
			}
			if !seen {
				before = "new"
			}
			moved = append(moved, transition{cluster: rep.Cluster, before: before, f: f})
		}
	}
	// A finding that is gone is a transition too, and during a repair it is
	// the one somebody is waiting for. Walking only the current findings
	// misses it: the node that was ERROR is simply not in this run's list any
	// more, and silence reads as "nothing happened".
	//
	// An OK that stopped being reported is not news, though — the check did
	// not run this time, and a repair window full of lines about nothing is
	// the thing this renderer exists to avoid.
	here := Statuses(reps)
	var gone []string
	for key, before := range prev {
		if _, still := here[key]; still || before == string(finding.OK) {
			continue
		}
		gone = append(gone, key+"\t"+before)
	}
	sort.Strings(gone)

	if len(moved) == 0 && len(gone) == 0 {
		return 0, nil
	}
	// Worst first here too: in a stream of lines the order is the only
	// structure there is.
	sort.SliceStable(moved, func(i, j int) bool {
		a, b := finding.Severity(moved[i].f.Status), finding.Severity(moved[j].f.Status)
		if a != b {
			return a > b
		}
		return moved[i].f.Check < moved[j].f.Check
	})

	stamp := now.Format("15:04:05")
	for _, m := range moved {
		if _, err := fmt.Fprintf(w, "%s  %-5s %-24s %-14s %s  [%s → %s]\n",
			stamp, m.f.Status, m.f.Check, m.cluster+"/"+m.f.Target, m.f.Message, m.before, m.f.Status); err != nil {
			return 0, err
		}
		if m.f.Hint != "" && finding.AtLeast(m.f.Status, finding.WARN) {
			if _, err := fmt.Fprintf(w, "%*s↳ %s\n", len(stamp)+3, "", m.f.Hint); err != nil {
				return 0, err
			}
		}
	}
	for _, g := range gone {
		key, before, _ := strings.Cut(g, "\t")
		if _, err := fmt.Fprintf(w, "%s  %-5s %-24s %-14s no longer reported  [%s → gone]\n",
			stamp, before, "", key, before); err != nil {
			return 0, err
		}
	}
	return len(moved) + len(gone), nil
}

// Statuses is the shape Changed compares against: every finding's status,
// keyed the same way. It is what a watch loop carries from one tick to the
// next, and it deliberately drops the transition summary.
func Statuses(reps []audit.Report) map[string]string {
	out := map[string]string{}
	for _, rep := range reps {
		for _, f := range rep.Findings {
			if f.Check == "audit/changes" {
				continue
			}
			out[rep.Cluster+"/"+state.Key(f.Check, f.Target)] = string(f.Status)
		}
	}
	return out
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
