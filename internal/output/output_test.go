package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Allan-Nava/galera-doctor/internal/audit"
	"github.com/Allan-Nava/galera-doctor/internal/finding"
)

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
