package state

import (
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
