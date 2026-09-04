package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Allan-Nava/galera-doctor/internal/audit"
	"github.com/Allan-Nava/galera-doctor/internal/finding"
)

func TestNodeFlagWantsNameEqualsDSN(t *testing.T) {
	var n nodeFlag
	if err := n.Set("sg-01=audit:pw@tcp(10.11.1.5:3306)/"); err != nil {
		t.Fatal(err)
	}
	if len(n) != 1 || n[0].Name != "sg-01" || !strings.HasPrefix(n[0].DSN, "audit:") {
		t.Fatalf("got %+v", n)
	}
	// A DSN contains "=" in its parameters, so the split has to be on the first
	// one only: name=user:pw@tcp(h:3306)/?timeout=5s must survive.
	if err := n.Set("cl-02=audit:pw@tcp(h:3306)/?timeout=5s"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(n[1].DSN, "?timeout=5s") {
		t.Fatalf("the DSN was truncated: %q", n[1].DSN)
	}
	for _, bad := range []string{"", "no-equals-sign", "=dsn-only", "name-only="} {
		if err := n.Set(bad); err == nil {
			t.Fatalf("Set(%q) must fail", bad)
		}
	}
}

func TestValidStatus(t *testing.T) {
	for _, ok := range []string{"", "OK", "WARN", "BAD", "ERROR"} {
		if err := validStatus(ok); err != nil {
			t.Fatalf("validStatus(%q) = %v", ok, err)
		}
	}
	if err := validStatus("bad"); err == nil {
		t.Fatal("statuses are upper case; a silently accepted lower-case one filters nothing")
	}
}

// GD-18 — watch mode.
//
// A loop that re-audits and prints only the transitions, for the window in
// which a cluster is being repaired. It is not a daemon and not a monitoring
// system: it runs in the foreground, it holds its baseline in memory, and it
// stops when the person watching it stops.
func TestWatchIntervalHasToBeUsable(t *testing.T) {
	for _, bad := range []time.Duration{0, -1 * time.Second, 500 * time.Millisecond} {
		if err := validWatch(bad, false, false); err == nil {
			t.Fatalf("--watch %s must be refused: a shorter interval than one audit takes is a busy loop", bad)
		}
	}
	if err := validWatch(10*time.Second, false, false); err != nil {
		t.Fatalf("--watch 10s is the documented example: %v", err)
	}
}

// The machine-readable renderers emit one document per run. A stream of them
// is not a document, and a consumer that parses stdout would break on the
// second tick — so the combination is a usage error rather than a surprise.
func TestWatchRefusesTheMachineRenderers(t *testing.T) {
	if err := validWatch(10*time.Second, true, false); err == nil {
		t.Fatal("--watch with --json must be a usage error")
	}
	if err := validWatch(10*time.Second, false, true); err == nil {
		t.Fatal("--watch with --findings must be a usage error")
	}
}

// The loop is driven by a tick channel so a test can advance it, and it stops
// when that channel closes: a watch that only stops on a signal cannot be
// tested, and an untested loop is how "it printed nothing" becomes "it printed
// nothing because it never ran".
func TestWatchPrintsTheBaselineThenOnlyTransitions(t *testing.T) {
	statuses := []finding.Status{finding.BAD, finding.BAD, finding.OK}
	var runs int
	run := func() []audit.Report {
		st := statuses[runs]
		runs++
		return []audit.Report{{
			Cluster: "compress", Nodes: []string{"sg-01"},
			Findings: []finding.Finding{{
				Check: "cluster/uuid", Target: "compress", Status: st,
				Message: "the state of the cluster", Hint: "do something",
			}},
		}}
	}

	ticks := make(chan time.Time, 3)
	for i := 0; i < 3; i++ {
		ticks <- time.Date(2026, 9, 4, 12, 0, i*10, 0, time.UTC)
	}
	close(ticks)

	var b strings.Builder
	if code := watch(&b, ticks, run); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	out := b.String()
	if runs != 3 {
		t.Fatalf("the loop ran %d times, want 3", runs)
	}
	// The first tick is the baseline: the full report, so somebody knows where
	// they are starting from.
	if !strings.Contains(out, "BAD") || !strings.Contains(out, "cluster/uuid") {
		t.Fatalf("no baseline was printed:\n%s", out)
	}
	// The second tick changed nothing and must have printed nothing extra.
	// The third cleared it.
	if strings.Count(out, "cluster/uuid") != 2 {
		t.Fatalf("want the baseline and one transition, got %d mentions:\n%s",
			strings.Count(out, "cluster/uuid"), out)
	}
	if !strings.Contains(out, "[BAD → OK]") {
		t.Fatalf("the transition must show where it came from:\n%s", out)
	}
}
