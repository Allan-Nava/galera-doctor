package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Allan-Nava/galera-doctor/internal/audit"
	"github.com/Allan-Nava/galera-doctor/internal/finding"
)

var at = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func reports() []audit.Report {
	return []audit.Report{
		{Cluster: "quiet", Nodes: []string{"a", "b", "c"}, Findings: []finding.Finding{
			{Check: "cluster/size", Target: "quiet", Status: finding.OK, Message: "3 member(s), expected 3"},
		}},
		{Cluster: "broken", Nodes: []string{"a", "b"}, Findings: []finding.Finding{
			{Check: "cluster/uuid", Target: "broken", Status: finding.BAD, Message: "two clusters", Hint: "this is a partition"},
			{Check: "node/state", Target: "a", Status: finding.OK, Message: "local state is Synced"},
		}},
	}
}

func TestTextPutsTheWorstClusterFirst(t *testing.T) {
	var b bytes.Buffer
	if err := Text(&b, reports(), ""); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.HasPrefix(out, "BAD") {
		t.Fatalf("worst cluster must lead:\n%s", out)
	}
	if !strings.Contains(out, "↳ this is a partition") {
		t.Fatalf("the hint is the actionable half:\n%s", out)
	}
}

func TestTextKeepsTheClusterHeaderUnderAFilter(t *testing.T) {
	var b bytes.Buffer
	if err := Text(&b, reports(), finding.BAD); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "quiet") {
		t.Fatalf("a cluster with nothing above the threshold was audited too:\n%s", out)
	}
	if strings.Contains(out, "local state is Synced") {
		t.Fatalf("findings below the threshold must be hidden:\n%s", out)
	}
}

func TestFindingsEmitsAnEmptyArrayNotNull(t *testing.T) {
	var b bytes.Buffer
	if err := Findings(&b, nil, ""); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(b.String()) != "[]" {
		t.Fatalf("got %q", b.String())
	}
}

func TestJSONRoundTrip(t *testing.T) {
	var b bytes.Buffer
	if err := JSON(&b, reports()); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Tool    string `json:"tool"`
		Reports []struct {
			Cluster  string `json:"cluster"`
			Nodes    []string
			Findings []finding.Finding `json:"findings"`
		} `json:"reports"`
	}
	if err := json.Unmarshal(b.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Tool != "galera-doctor" || len(parsed.Reports) != 2 {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestSummaryCounts(t *testing.T) {
	var b bytes.Buffer
	if err := Summary(&b, reports()); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); !strings.Contains(got, "2 cluster(s), 5 node(s)") || !strings.Contains(got, "1 BAD") {
		t.Fatalf("summary = %q", got)
	}
}

// GD-18 — watch mode prints only what changed.
//
// The window this exists for is the one in which somebody is repairing a
// cluster: they ran the audit, they did something, and they want to see the
// effect. Reprinting twenty OK lines every ten seconds buries the one line
// that moved, so silence is the feature — and a run that changed nothing has
// to print nothing at all.
func TestChangedPrintsOnlyWhatMoved(t *testing.T) {
	prev := map[string]string{
		"broken/cluster/uuid@broken": "BAD",
		"broken/node/state@a":        "OK",
		"quiet/cluster/size@quiet":   "OK",
	}
	// The partition cleared; everything else is where it was.
	reps := reports()
	reps[1].Findings[0].Status = finding.OK
	reps[1].Findings[0].Message = "all nodes report one cluster"

	var b bytes.Buffer
	changed, err := Changed(&b, reps, prev, at)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("one finding moved, got %d", changed)
	}
	out := b.String()
	if !strings.Contains(out, "cluster/uuid") {
		t.Fatalf("the finding that moved is missing:\n%s", out)
	}
	if strings.Contains(out, "cluster/size") || strings.Contains(out, "node/state") {
		t.Fatalf("unchanged findings were reprinted:\n%s", out)
	}
	// Where it came from is half the information.
	if !strings.Contains(out, "BAD") || !strings.Contains(out, "OK") {
		t.Fatalf("the transition must show both statuses:\n%s", out)
	}
	if !strings.Contains(out, "12:00") {
		t.Fatalf("a watch line without a time is not much use:\n%s", out)
	}
	if !strings.Contains(out, "broken") {
		t.Fatalf("the cluster has to be named: several are audited at once:\n%s", out)
	}
}

func TestChangedPrintsNothingWhenNothingMoved(t *testing.T) {
	prev := map[string]string{
		"broken/cluster/uuid@broken": "BAD",
		"broken/node/state@a":        "OK",
		"quiet/cluster/size@quiet":   "OK",
	}
	var b bytes.Buffer
	changed, err := Changed(&b, reports(), prev, at)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 0 || b.Len() != 0 {
		t.Fatalf("a run that changed nothing must print nothing, got %d:\n%s", changed, b.String())
	}
}

// A finding that appeared for the first time has no previous status, and that
// is a transition too — the interesting one, usually.
func TestANewFindingIsAChange(t *testing.T) {
	var b bytes.Buffer
	changed, err := Changed(&b, reports(), map[string]string{"quiet/cluster/size@quiet": "OK"}, at)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 2 {
		t.Fatalf("two findings are new, got %d:\n%s", changed, b.String())
	}
	if !strings.Contains(b.String(), "new") {
		t.Fatalf("a first sighting must say so rather than show an empty status:\n%s", b.String())
	}
}

// Worst first, here too: in a stream of lines the order is the only structure
// there is.
func TestChangedPutsTheWorstFirst(t *testing.T) {
	var b bytes.Buffer
	if _, err := Changed(&b, reports(), map[string]string{}, at); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "BAD") {
		t.Fatalf("the worst transition must lead:\n%s", b.String())
	}
}

// A finding that is gone is a transition too — during a repair it is the one
// somebody is waiting for. Walking only the current findings misses it: the
// node that was ERROR is simply not in this run's list any more, and silence
// reads as "nothing happened".
func TestAFindingThatDisappearedIsAChange(t *testing.T) {
	prev := map[string]string{
		"broken/cluster/uuid@broken": "BAD",
		"broken/node/state@a":        "OK",
		"quiet/cluster/size@quiet":   "OK",
		// This one was found last time and is not in this run at all.
		"broken/node/not-galera@b": "ERROR",
	}
	var b bytes.Buffer
	changed, err := Changed(&b, reports(), prev, at)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("one finding disappeared, got %d changes:\n%s", changed, b.String())
	}
	out := b.String()
	if !strings.Contains(out, "node/not-galera") {
		t.Fatalf("the finding that went away is missing:\n%s", out)
	}
	if !strings.Contains(out, "ERROR → gone") {
		t.Fatalf("it has to say what it was and that it is gone:\n%s", out)
	}
}

// An OK that disappears is not news: the check simply did not run this time,
// and reporting it would fill a repair window with lines about nothing.
func TestAnOKThatDisappearedIsNotNews(t *testing.T) {
	prev := map[string]string{
		"broken/cluster/uuid@broken": "BAD",
		"broken/node/state@a":        "OK",
		"quiet/cluster/size@quiet":   "OK",
		"quiet/gcache/window@a":      "OK",
	}
	var b bytes.Buffer
	changed, err := Changed(&b, reports(), prev, at)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 0 || b.Len() != 0 {
		t.Fatalf("an OK that stopped being reported is not a transition:\n%s", b.String())
	}
}
