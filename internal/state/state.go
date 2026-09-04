// Package state remembers the previous run so that counters can be graded as
// rates instead of as totals.
//
// This exists because of a rule learned the hard way: never put a
// non-decaying counter in a verdict. wsrep_local_cert_failures and
// wsrep_flow_control_paused_ns only ever go up, and they reset on restart —
// so a cluster that flow-controlled badly for ten minutes last March reports
// the same number today as it did the day after, and a threshold over it goes
// red once and stays red until someone silences it. Graded over the interval
// between two runs, the same counters answer a question worth asking: is it
// happening *now*?
//
// The state file is a cache, never a source of truth. A missing file, an
// unreadable one or a counter that went backwards (a restart, a FLUSH STATUS)
// all mean the same thing: no baseline, so say so and do not grade.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Allan-Nava/galera-doctor/internal/cluster"
)

// Version is the on-disk format. A file from another version is ignored rather
// than migrated: the worst outcome of a stale cache is one ungraded run.
//
// 2 added the previous run's findings alongside its counters (GD-32). A v1
// file has no findings in it, and reading one as "this run had none" would
// report every current finding as newly appeared — which is why the version
// moves with the format instead of the field being treated as optional.
const Version = 2

// Counters are the status variables carried between runs. They are all
// monotonic totals, which is precisely why they are here.
var Counters = []string{
	"wsrep_flow_control_paused_ns",
	"wsrep_local_cert_failures",
	"wsrep_local_bf_aborts",
	"wsrep_replicated",
	"wsrep_replicated_bytes",
	"wsrep_received_bytes",
}

// NodeState is one node's counters at one moment.
type NodeState struct {
	At     time.Time `json:"at"`
	Uptime float64   `json:"uptime_seconds"`
	// GcommUUID is wsrep_gcomm_uuid: a new value on every boot, which is how a
	// restart between two runs becomes a fact rather than an unexplained
	// missing baseline (GD-52).
	//
	// Unlike Findings, this one did not need the format version to move: an
	// absent uuid means "nothing to compare", which cannot be mistaken for a
	// different one, so an older file simply produces no finding. A version
	// bump would have thrown away everybody's baseline to gain nothing.
	GcommUUID string             `json:"gcomm_uuid,omitempty"`
	Counters  map[string]float64 `json:"counters"`
}

// State is the previous run.
type State struct {
	Version int                  `json:"version"`
	At      time.Time            `json:"at"`
	Nodes   map[string]NodeState `json:"nodes"`
	// Findings is the previous run's verdicts, keyed "check@target", so the
	// next run can say what appeared, what cleared and what got worse. Only
	// the status is kept: the message is prose that changes with the
	// measurement, and comparing prose would report a change every time a
	// percentage moved (GD-32).
	Findings map[string]string `json:"findings,omitempty"`
}

// New builds the state to persist from a set of snapshots.
func New(snaps []cluster.Snapshot, now time.Time) State {
	st := State{Version: Version, At: now, Nodes: map[string]NodeState{}}
	for _, s := range snaps {
		if !s.OK() {
			continue
		}
		ns := NodeState{At: s.At, Counters: map[string]float64{}}
		if up, ok := s.Float("uptime"); ok {
			ns.Uptime = up
		}
		if uuid, ok := s.Get("wsrep_gcomm_uuid"); ok {
			ns.GcommUUID = strings.TrimSpace(uuid)
		}
		for _, key := range Counters {
			if v, ok := s.Float(key); ok {
				ns.Counters[key] = v
			}
		}
		if ns.At.IsZero() {
			ns.At = now
		}
		st.Nodes[s.Node] = ns
	}
	return st
}

// Merge folds one cluster's state into a file that can hold several: one
// --state file is allowed to cover a whole --config, so everything in it is
// namespaced by cluster name.
func (s *State) Merge(cluster string, from State) {
	if s.Nodes == nil {
		s.Nodes = map[string]NodeState{}
	}
	for node, ns := range from.Nodes {
		s.Nodes[cluster+"/"+node] = ns
	}
	if len(from.Findings) == 0 {
		return
	}
	if s.Findings == nil {
		s.Findings = map[string]string{}
	}
	for k, v := range from.Findings {
		s.Findings[cluster+"/"+k] = v
	}
}

// Scope is the view of one cluster, keyed the way the audit asks for it (GD-46).
//
// The file namespaces everything by cluster; Since() and the transition report
// both ask about a bare node name and a bare "check@target". Without this in
// between, every lookup misses and every counter check reports "not graded: no
// baseline" forever — which is indistinguishable from a cluster that has
// nothing to grade, and is exactly the silence this tool exists to refuse.
//
// A cluster that is not in the file yet scopes to an empty baseline rather
// than to another cluster's.
func (s *State) Scope(cluster string) *State {
	if s == nil {
		return nil
	}
	out := &State{
		Version:  s.Version,
		At:       s.At,
		Nodes:    map[string]NodeState{},
		Findings: map[string]string{},
	}
	prefix := cluster + "/"
	for k, ns := range s.Nodes {
		if node, ok := trimPrefix(k, prefix); ok {
			out.Nodes[node] = ns
		}
	}
	for k, v := range s.Findings {
		if key, ok := trimPrefix(k, prefix); ok {
			out.Findings[key] = v
		}
	}
	return out
}

func trimPrefix(s, prefix string) (string, bool) {
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

// Key is how a finding is identified between runs: the check and what it was
// about. Not the message — that is prose carrying a measurement, and comparing
// it would report a change every time a percentage moved by 0.1.
func Key(check, target string) string { return check + "@" + target }

// Delta is a counter's movement between two runs.
type Delta struct {
	Value   float64
	Elapsed time.Duration
}

// PerSecond is the rate, or 0 for a zero-length interval.
func (d Delta) PerSecond() float64 {
	if d.Elapsed <= 0 {
		return 0
	}
	return d.Value / d.Elapsed.Seconds()
}

// Fraction is the share of the interval a nanosecond counter accounts for —
// the shape wsrep_flow_control_paused_ns is in.
func (d Delta) Fraction() float64 {
	if d.Elapsed <= 0 {
		return 0
	}
	return d.Value / float64(d.Elapsed.Nanoseconds())
}

// Since returns the movement of key on node since the previous run.
//
// The bool is false when there is no usable baseline, and *that includes a
// counter that went backwards*: a server that restarted between runs has a
// smaller total, and reading the difference as a negative rate — or worse, as
// a huge one after a wraparound — would invent an incident. A restart is
// detected by the counter itself, not by trusting uptime, because uptime is
// also missing on some builds.
func (s *State) Since(node, key string, now NodeState) (Delta, bool) {
	if s == nil || s.Version != Version {
		return Delta{}, false
	}
	prev, ok := s.Nodes[node]
	if !ok {
		return Delta{}, false
	}
	before, ok1 := prev.Counters[key]
	after, ok2 := now.Counters[key]
	if !ok1 || !ok2 || after < before {
		return Delta{}, false
	}
	elapsed := now.At.Sub(prev.At)
	if elapsed <= 0 {
		return Delta{}, false
	}
	// A node whose uptime is shorter than the interval restarted, even if the
	// counter happens to have climbed past its old value already.
	if prev.Uptime > 0 && now.Uptime > 0 && now.Uptime < prev.Uptime {
		return Delta{}, false
	}
	return Delta{Value: after - before, Elapsed: elapsed}, true
}

// Load reads a state file. A missing file is not an error: the first run of
// anything has no baseline, and that is a normal state to be in.
func Load(path string) (*State, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if st.Version != Version {
		return nil, nil
	}
	return &st, nil
}

// Save writes the state file atomically, so an interrupted run cannot leave a
// half-written baseline that the next one reads as real numbers.
func Save(path string, st State) error {
	if path == "" {
		return nil
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
