package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Allan-Nava/galera-doctor/internal/cluster"
)

var now = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

func nodeState(at time.Time, uptime, paused float64) NodeState {
	return NodeState{At: at, Uptime: uptime, Counters: map[string]float64{"wsrep_flow_control_paused_ns": paused}}
}

func TestSinceComputesARate(t *testing.T) {
	prev := &State{Version: Version, Nodes: map[string]NodeState{
		"a": nodeState(now.Add(-10*time.Minute), 1000, 0),
	}}
	d, ok := prev.Since("a", "wsrep_flow_control_paused_ns", nodeState(now, 1600, 60e9))
	if !ok {
		t.Fatal("a well-formed baseline must produce a delta")
	}
	if d.Elapsed != 10*time.Minute {
		t.Fatalf("elapsed = %s", d.Elapsed)
	}
	if f := d.Fraction(); f < 0.09 || f > 0.11 {
		t.Fatalf("60s paused in 600s is 10%%, got %.3f", f)
	}
}

// Every one of these is a way to get a wrong number, and all of them mean the
// same thing: no baseline.
func TestSinceRefusesEveryUnusableBaseline(t *testing.T) {
	base := &State{Version: Version, Nodes: map[string]NodeState{"a": nodeState(now.Add(-time.Minute), 1000, 100)}}

	if _, ok := (*State)(nil).Since("a", "k", nodeState(now, 1060, 200)); ok {
		t.Fatal("no state at all is no baseline")
	}
	if _, ok := base.Since("b", "wsrep_flow_control_paused_ns", nodeState(now, 1060, 200)); ok {
		t.Fatal("a node that was not in the previous run has no baseline")
	}
	if _, ok := base.Since("a", "wsrep_flow_control_paused_ns", nodeState(now, 1060, 50)); ok {
		t.Fatal("a counter that went backwards means a reset, not a negative rate")
	}
	if _, ok := base.Since("a", "wsrep_flow_control_paused_ns", nodeState(now, 30, 200)); ok {
		t.Fatal("uptime shorter than before means the server restarted")
	}
	if _, ok := base.Since("a", "wsrep_flow_control_paused_ns", nodeState(now.Add(-time.Hour), 1060, 200)); ok {
		t.Fatal("a non-positive interval cannot produce a rate")
	}
	stale := &State{Version: Version + 1, Nodes: base.Nodes}
	if _, ok := stale.Since("a", "wsrep_flow_control_paused_ns", nodeState(now, 1060, 200)); ok {
		t.Fatal("a state file from another format version must be ignored")
	}
}

func TestNewSkipsUnreadNodes(t *testing.T) {
	st := New([]cluster.Snapshot{
		{Node: "a", At: now, Status: map[string]string{"wsrep_replicated": "10", "uptime": "500"}},
		{Node: "b", Err: "unreachable"},
	}, now)
	if _, ok := st.Nodes["b"]; ok {
		t.Fatal("a node that was not read has no counters to remember")
	}
	if st.Nodes["a"].Counters["wsrep_replicated"] != 10 {
		t.Fatalf("counters = %+v", st.Nodes["a"].Counters)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	st := State{Version: Version, At: now, Nodes: map[string]NodeState{"a": nodeState(now, 1, 2)}}
	if err := Save(path, st); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Nodes["a"].Counters["wsrep_flow_control_paused_ns"] != 2 {
		t.Fatalf("round trip lost the counters: %+v", got)
	}
}

// The first run of anything has no state file, and that is normal.
func TestLoadOfAMissingFileIsNotAnError(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || got != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got, err := Load(""); err != nil || got != nil {
		t.Fatalf("no path means no state: %+v, %v", got, err)
	}
}

// GD-32 — the previous run's findings, carried in the same file as its
// counters. The state file is a cache: a file from an older format is not a
// file to migrate, it is no baseline at all.
func TestFindingsRoundTripThroughTheStateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	st := State{Version: Version, At: now, Nodes: map[string]NodeState{}}
	st.Findings = map[string]string{
		"systables/drift@mysql.user": "BAD",
		"flow/paused@sg-01":          "WARN",
	}
	if err := Save(path, st); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil || got == nil {
		t.Fatalf("load: %+v %v", got, err)
	}
	if got.Findings["systables/drift@mysql.user"] != "BAD" {
		t.Fatalf("the previous run's findings did not survive: %+v", got.Findings)
	}
	if len(got.Findings) != 2 {
		t.Fatalf("want 2 findings, got %d", len(got.Findings))
	}
}

// A file written by an older format version must be ignored rather than half
// read: a partial baseline invents transitions that never happened.
func TestAnOlderFormatVersionIsNoBaseline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"at":"2026-09-02T12:00:00Z","nodes":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != nil {
		t.Fatalf("a v1 file must read as no baseline, got %+v", got)
	}
}

// The format version has moved past 1, or a state file written before findings
// were carried would be read as "everything cleared".
func TestTheFormatVersionMovedWithTheFormat(t *testing.T) {
	if Version < 2 {
		t.Fatalf("Version = %d: carrying findings changed the file, so the version has to change with it", Version)
	}
}

// GD-46 — the file namespaces nodes by cluster, and the audit asks about bare
// node names.
//
// This was a silent, total failure: main.go wrote "compress/sg-01" and
// Since() looked up "sg-01", so the lookup missed on every run and every
// counter check reported "not graded: no baseline" forever. A check that never
// grades looks exactly like a cluster with nothing to grade, which is the one
// failure mode this tool is supposed to refuse.
func TestScopeIsWhatMakesABaselineFindable(t *testing.T) {
	one := State{Version: Version, At: now, Nodes: map[string]NodeState{
		"sg-01": nodeState(now.Add(-10*time.Minute), 3600, 1_000_000_000),
	}, Findings: map[string]string{"flow/paused@sg-01": "WARN"}}
	two := State{Version: Version, At: now, Nodes: map[string]NodeState{
		"db-01": nodeState(now.Add(-10*time.Minute), 3600, 5),
	}, Findings: map[string]string{"cluster/size@other": "BAD"}}

	var file State
	file.Version = Version
	file.At = now
	file.Merge("compress", one)
	file.Merge("other", two)

	// What lands on disk is namespaced, so one file can hold several clusters.
	if _, ok := file.Nodes["compress/sg-01"]; !ok {
		t.Fatalf("the merged file must namespace nodes: %+v", file.Nodes)
	}
	if _, ok := file.Findings["other/cluster/size@other"]; !ok {
		t.Fatalf("the merged file must namespace findings: %+v", file.Findings)
	}

	// What the audit gets is one cluster's view, keyed the way it asks.
	scoped := file.Scope("compress")
	if scoped == nil {
		t.Fatal("scoping a present cluster returned nil")
	}
	now2 := nodeState(now, 4200, 3_000_000_000)
	d, ok := scoped.Since("sg-01", "wsrep_flow_control_paused_ns", now2)
	if !ok {
		t.Fatalf("the baseline was not found after scoping: %+v", scoped.Nodes)
	}
	if d.Value != 2_000_000_000 {
		t.Fatalf("delta = %v, want 2e9", d.Value)
	}
	if scoped.Findings["flow/paused@sg-01"] != "WARN" {
		t.Fatalf("the previous findings did not survive scoping: %+v", scoped.Findings)
	}
	// And it must not see the other cluster's.
	if _, leaked := scoped.Findings["cluster/size@other"]; leaked {
		t.Fatalf("another cluster's findings leaked in: %+v", scoped.Findings)
	}
	if _, leaked := scoped.Nodes["db-01"]; leaked {
		t.Fatalf("another cluster's nodes leaked in: %+v", scoped.Nodes)
	}
}

// A cluster that is not in the file yet is no baseline, not a panic.
func TestScopingAClusterThatIsNotThereIsNoBaseline(t *testing.T) {
	file := State{Version: Version, At: now, Nodes: map[string]NodeState{"compress/sg-01": nodeState(now, 1, 1)}}
	scoped := file.Scope("brand-new")
	if scoped == nil {
		t.Fatal("scoping must return a usable value, not nil")
	}
	if _, ok := scoped.Since("sg-01", "wsrep_flow_control_paused_ns", nodeState(now, 2, 2)); ok {
		t.Fatal("a cluster with no baseline must not borrow another cluster's")
	}
}

func TestScopingNilIsNil(t *testing.T) {
	var s *State
	if s.Scope("compress") != nil {
		t.Fatal("no state file scopes to no state")
	}
}
