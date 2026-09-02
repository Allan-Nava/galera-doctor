package audit

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Allan-Nava/galera-doctor/internal/cluster"
	"github.com/Allan-Nava/galera-doctor/internal/finding"
	"github.com/Allan-Nava/galera-doctor/internal/state"
)

var now = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

// healthy is one node of a cluster where nothing is wrong.
func healthy(name string, at time.Time) cluster.Snapshot {
	return cluster.Snapshot{
		Node: name,
		At:   at,
		Status: map[string]string{
			"wsrep_cluster_state_uuid":     "5b1e2a8c-1111-11ef-9d2b-000000000001",
			"wsrep_cluster_conf_id":        "42",
			"wsrep_cluster_size":           "3",
			"wsrep_cluster_status":         "Primary",
			"wsrep_local_state_comment":    "Synced",
			"wsrep_ready":                  "ON",
			"wsrep_connected":              "ON",
			"wsrep_local_recv_queue":       "0",
			"wsrep_local_send_queue":       "0",
			"wsrep_provider_version":       "26.4.16(r)",
			"wsrep_flow_control_paused_ns": "0",
			"wsrep_flow_control_paused":    "0.0",
			"wsrep_local_cert_failures":    "0",
			"wsrep_replicated":             "1000",
			"wsrep_replicated_bytes":       "1048576",
			"uptime":                       "86400",
		},
		Vars: map[string]string{
			"version":                "10.11.8-MariaDB",
			"wsrep_on":               "ON",
			"wsrep_provider":         "/usr/lib/galera/libgalera_smm.so",
			"read_only":              "OFF",
			"wsrep_provider_options": "gcache.size = 512M; gcs.fc_limit = 16;",
		},
		SysTables: map[string]string{"user": "aaaaaaaaaaaaaaaa", "column_stats": "bbbbbbbbbbbbbbbb"},
		// A non-nil map is "the application schemas were read"; nil is "they
		// were not", which is a different finding from "there are none".
		AppTables: map[string]string{"app.users": "1111111111111111", "app.events": "2222222222222222"},
	}
}

func threeHealthy() []cluster.Snapshot {
	return []cluster.Snapshot{healthy("sg-01", now), healthy("cl-02", now), healthy("ov-03", now)}
}

func opts() Options {
	o := DefaultOptions()
	o.Cluster = "compress"
	o.Now = now
	return o
}

func byCheck(t *testing.T, rep Report, check string) []finding.Finding {
	t.Helper()
	var out []finding.Finding
	for _, f := range rep.Findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

func one(t *testing.T, rep Report, check string) finding.Finding {
	t.Helper()
	fs := byCheck(t, rep, check)
	if len(fs) != 1 {
		t.Fatalf("want exactly one %q finding, got %d: %+v", check, len(fs), rep.Findings)
	}
	return fs[0]
}

func TestHealthyClusterIsQuiet(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if rep.Worst() != finding.OK {
		t.Fatalf("a healthy cluster must produce nothing above OK, got %s:\n%+v", rep.Worst(), rep.Findings)
	}
}

// One name, two clusters. Nothing in wsrep_cluster_size says so — both halves
// look internally consistent.
func TestSplitBrainIsBad(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Status["wsrep_cluster_state_uuid"] = "9999aaaa-2222-11ef-9d2b-000000000002"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "cluster/uuid")
	if f.Status != finding.BAD {
		t.Fatalf("status = %s, want BAD", f.Status)
	}
	if !strings.Contains(f.Message, "ov-03") {
		t.Fatalf("the finding must name the diverged node: %q", f.Message)
	}
}

func TestNonPrimaryNodeIsBad(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].Status["wsrep_cluster_status"] = "non-Primary"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "cluster/primary")
	if f.Status != finding.BAD || f.Target != "cl-02" {
		t.Fatalf("got %+v", f)
	}
}

func TestDisagreementAboutSizeBeatsTheSizeItself(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Status["wsrep_cluster_size"] = "2"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "cluster/size")
	if f.Status != finding.BAD || !strings.Contains(f.Message, "disagree") {
		t.Fatalf("got %+v", f)
	}
}

func TestMissingMemberIsBad(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Status["wsrep_cluster_size"] = "2"
	}
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "cluster/size")
	if f.Status != finding.BAD || !strings.Contains(f.Message, "expected 3") {
		t.Fatalf("got %+v", f)
	}
}

// The check no wsrep_* metric can make.
func TestSystemTableDriftIsFoundAndAttributed(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].SysTables["column_stats"] = "cccccccccccccccc"
	rep := Run(snaps, nil, opts())

	var drift *finding.Finding
	for i, f := range rep.Findings {
		if f.Check == "systables/drift" && f.Target == "mysql.column_stats" {
			drift = &rep.Findings[i]
		}
	}
	if drift == nil {
		t.Fatalf("no drift finding: %+v", rep.Findings)
	}
	if drift.Status != finding.BAD {
		t.Fatalf("status = %s, want BAD", drift.Status)
	}
	if !strings.Contains(drift.Message, "cl-02") {
		t.Fatalf("the drifted node must be named: %q", drift.Message)
	}
	if !strings.Contains(drift.Hint, "does not replicate") {
		t.Fatalf("the hint has to say why no replication metric shows it: %q", drift.Hint)
	}
}

func TestATableMissingOnOneNodeIsDrift(t *testing.T) {
	snaps := threeHealthy()
	delete(snaps[0].SysTables, "column_stats")
	rep := Run(snaps, nil, opts())
	found := false
	for _, f := range rep.Findings {
		if f.Check == "systables/drift" && f.Target == "mysql.column_stats" && f.Status == finding.BAD {
			found = true
			if !strings.Contains(f.Message, "absent") {
				t.Fatalf("an absent table must say so: %q", f.Message)
			}
		}
	}
	if !found {
		t.Fatalf("a table present on two nodes and missing on the third is drift: %+v", rep.Findings)
	}
}

// A node the audit could not read must not be silently dropped: every
// cluster-wide statement below was made without it.
func TestUnreadNodeIsAnError(t *testing.T) {
	snaps := threeHealthy()
	snaps[2] = cluster.Snapshot{Node: "ov-03", Err: "dial tcp: i/o timeout"}
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "node/read")
	if f.Status != finding.ERROR {
		t.Fatalf("status = %s, want ERROR", f.Status)
	}
	if rep.Worst() != finding.ERROR {
		t.Fatalf("worst = %s: an unread node has to outrank everything else", rep.Worst())
	}
}

func TestNoNodeReadableStopsAtMembership(t *testing.T) {
	snaps := []cluster.Snapshot{{Node: "a", Err: "no"}, {Node: "b", Err: "no"}}
	rep := Run(snaps, nil, opts())
	if len(byCheck(t, rep, "cluster/uuid")) != 0 {
		t.Fatal("no cluster-wide claim may be made when nothing was read")
	}
	if rep.Worst() != finding.ERROR {
		t.Fatalf("worst = %s", rep.Worst())
	}
}

// A counter that only goes up must never be graded. Without a baseline the
// lifetime figure is reported at OK and says why.
func TestFlowControlIsNotGradedWithoutABaseline(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Status["wsrep_flow_control_paused"] = "0.93" // an incident, months ago
	}
	rep := Run(snaps, nil, opts())
	for _, f := range byCheck(t, rep, "flow/paused") {
		if f.Status != finding.OK {
			t.Fatalf("a lifetime total must not produce a verdict: %+v", f)
		}
		if !strings.Contains(f.Message, "not graded") {
			t.Fatalf("the report has to say the number was not graded: %q", f.Message)
		}
	}
}

func TestFlowControlIsGradedOverTheInterval(t *testing.T) {
	prev := &state.State{
		Version: state.Version,
		At:      now.Add(-10 * time.Minute),
		Nodes: map[string]state.NodeState{
			"sg-01": {At: now.Add(-10 * time.Minute), Uptime: 86400 - 600, Counters: map[string]float64{
				"wsrep_flow_control_paused_ns": 0,
				"wsrep_replicated":             500,
				"wsrep_local_cert_failures":    0,
				"wsrep_replicated_bytes":       0,
			}},
		},
	}
	snaps := threeHealthy()
	// 120 s paused out of a 600 s interval = 20%.
	snaps[0].Status["wsrep_flow_control_paused_ns"] = "120000000000"
	rep := Run(snaps, prev, opts())

	var got *finding.Finding
	for i, f := range rep.Findings {
		if f.Check == "flow/paused" && f.Target == "sg-01" {
			got = &rep.Findings[i]
		}
	}
	if got == nil {
		t.Fatalf("no flow finding for sg-01: %+v", rep.Findings)
	}
	if got.Status != finding.BAD {
		t.Fatalf("20%% of the interval must be BAD, got %s (%s)", got.Status, got.Message)
	}
	if got.Value == nil || *got.Value < 0.19 || *got.Value > 0.21 {
		t.Fatalf("value = %v, want ~0.20", got.Value)
	}
}

// A restart resets the counters. Reading the difference then would invent an
// incident (or, after a wraparound, a spectacular one).
func TestARestartInvalidatesTheBaseline(t *testing.T) {
	prev := &state.State{
		Version: state.Version,
		At:      now.Add(-10 * time.Minute),
		Nodes: map[string]state.NodeState{
			"sg-01": {At: now.Add(-10 * time.Minute), Uptime: 999999, Counters: map[string]float64{
				"wsrep_flow_control_paused_ns": 500000000000,
			}},
		},
	}
	snaps := threeHealthy()
	snaps[0].Status["wsrep_flow_control_paused_ns"] = "1000" // way below the old total
	rep := Run(snaps, prev, opts())
	for _, f := range byCheck(t, rep, "flow/paused") {
		if f.Target == "sg-01" && f.Status != finding.OK {
			t.Fatalf("a reset counter must fall back to ungraded, got %+v", f)
		}
	}
}

func TestDonorAndDesyncAreWarnings(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Status["wsrep_local_state_comment"] = "Donor/Desynced"
	snaps[1].Vars["wsrep_on"] = "OFF"
	rep := Run(snaps, nil, opts())

	var donor, wsrepOff *finding.Finding
	for i, f := range rep.Findings {
		if f.Check == "node/state" && f.Target == "sg-01" {
			donor = &rep.Findings[i]
		}
		if f.Check == "node/wsrep-on" {
			wsrepOff = &rep.Findings[i]
		}
	}
	if donor == nil || donor.Status != finding.WARN {
		t.Fatalf("a donor is a warning, not a failure: %+v", donor)
	}
	if wsrepOff == nil || wsrepOff.Status != finding.BAD {
		t.Fatalf("wsrep_on OFF means writes are not replicated: %+v", wsrepOff)
	}
}

func TestMissingPrimaryKeysAreListedOnce(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].TablesNoPK = []string{"app.events", "app.sessions"}
	snaps[1].TablesNoPK = []string{"app.events"}
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "schema/no-pk")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s", f.Status)
	}
	if f.Value == nil || *f.Value != 2 {
		t.Fatalf("the union of the nodes is two tables, got %v", f.Value)
	}
}

func TestMixedVersionsWarn(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Vars["version"] = "10.6.18-MariaDB"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "cluster/versions")
	if f.Status != finding.WARN || !strings.Contains(f.Message, "ov-03") {
		t.Fatalf("got %+v", f)
	}
}

func TestGcacheWindowNeedsARateBeforeItJudges(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	for _, f := range byCheck(t, rep, "gcache/window") {
		if f.Status != finding.OK || !strings.Contains(f.Message, "not graded") {
			t.Fatalf("a size without a write rate is not a verdict: %+v", f)
		}
	}

	prev := &state.State{Version: state.Version, At: now.Add(-time.Minute), Nodes: map[string]state.NodeState{
		"sg-01": {At: now.Add(-time.Minute), Uptime: 86400 - 60, Counters: map[string]float64{
			// 512 MB gcache and 10 MB/s of writes is a window under a minute.
			"wsrep_replicated_bytes": 1048576 - 600*(1<<20),
		}},
	}}
	snaps := threeHealthy()
	rep = Run(snaps, prev, opts())
	var got *finding.Finding
	for i, f := range rep.Findings {
		if f.Check == "gcache/window" && f.Target == "sg-01" {
			got = &rep.Findings[i]
		}
	}
	if got == nil || got.Status != finding.WARN {
		t.Fatalf("a gcache that holds seconds of writes must warn: %+v", got)
	}
}

func TestEveryFindingCarriesATarget(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].Status["wsrep_cluster_status"] = "non-Primary"
	rep := Run(snaps, nil, opts())
	for _, f := range rep.Findings {
		if f.Check == "" || f.Target == "" || f.Message == "" {
			t.Fatalf("incomplete finding: %+v", f)
		}
	}
}

// A standalone server is not a cluster in ruins. Pointed at one, the audit has
// to say so once instead of firing every cluster check at the same time.
func TestAStandaloneServerIsOneFindingNotFive(t *testing.T) {
	solo := cluster.Snapshot{
		Node:   "local",
		At:     now,
		Status: map[string]string{"wsrep_cluster_size": "0", "wsrep_cluster_status": "Disconnected", "wsrep_ready": "OFF"},
		Vars:   map[string]string{"version": "11.4.5-MariaDB", "wsrep_provider": "none", "wsrep_on": "OFF"},
	}
	rep := Run([]cluster.Snapshot{solo}, nil, opts())

	f := one(t, rep, "node/not-galera")
	if f.Status != finding.ERROR {
		t.Fatalf("status = %s, want ERROR", f.Status)
	}
	for _, unwanted := range []string{"cluster/primary", "cluster/size", "node/ready", "node/wsrep-on"} {
		if got := byCheck(t, rep, unwanted); len(got) != 0 {
			t.Fatalf("%s must not fire on a standalone server: %+v", unwanted, got)
		}
	}
}

// wsrep_on OFF on a real cluster member is the opposite case: a serious
// finding that must not be swallowed by the standalone rule.
func TestARealMemberWithWsrepOffIsStillGraded(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_provider"] = "/usr/lib/galera/libgalera_smm.so"
	}
	snaps[1].Vars["wsrep_on"] = "OFF"
	rep := Run(snaps, nil, opts())
	if got := byCheck(t, rep, "node/not-galera"); len(got) != 0 {
		t.Fatalf("a configured member is not a standalone server: %+v", got)
	}
	if got := byCheck(t, rep, "node/wsrep-on"); len(got) != 1 || got[0].Status != finding.BAD {
		t.Fatalf("got %+v", got)
	}
}

// GD-13 — application schema drift.
//
// Galera *does* replicate application DDL, which is what makes a difference
// here a different diagnosis from systables/drift: it is not maintenance that
// was never replicated, it is a schema change that failed or was applied on
// one node by hand. Both are invisible to every wsrep_* counter.
func TestApplicationSchemaDriftIsFoundAndAttributed(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].AppTables["app.users"] = "9999999999999999"
	rep := Run(snaps, nil, opts())

	var drift *finding.Finding
	for i, f := range rep.Findings {
		if f.Check == "schema/drift" && f.Target == "app.users" {
			drift = &rep.Findings[i]
		}
	}
	if drift == nil {
		t.Fatalf("no application drift finding: %+v", rep.Findings)
	}
	if drift.Status != finding.BAD {
		t.Fatalf("status = %s, want BAD", drift.Status)
	}
	if !strings.Contains(drift.Message, "ov-03") {
		t.Fatalf("the drifted node must be named: %q", drift.Message)
	}
	// The hint has to distinguish this from systables/drift, or the operator
	// reaches for mysql_upgrade on a half-applied ALTER.
	if !strings.Contains(drift.Hint, "replicated") {
		t.Fatalf("the hint must say the DDL should have replicated and did not: %q", drift.Hint)
	}
}

func TestAnApplicationTableMissingOnOneNodeIsDrift(t *testing.T) {
	snaps := threeHealthy()
	delete(snaps[1].AppTables, "app.events")
	rep := Run(snaps, nil, opts())

	var f *finding.Finding
	for i := range rep.Findings {
		if rep.Findings[i].Check == "schema/drift" && rep.Findings[i].Target == "app.events" {
			f = &rep.Findings[i]
		}
	}
	if f == nil || f.Status != finding.BAD {
		t.Fatalf("a table missing on one node is drift: %+v", rep.Findings)
	}
	if !strings.Contains(f.Message, "absent") || !strings.Contains(f.Message, "cl-02") {
		t.Fatalf("the message must say the table is absent, and where: %q", f.Message)
	}
}

// A node whose grants do not reach information_schema is reported as not
// audited — never dropped from the comparison quietly.
func TestUnreadApplicationSchemaIsReportedAsNotAudited(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].AppTables = nil
	snaps[2].AppTables["app.users"] = "9999999999999999"
	rep := Run(snaps, nil, opts())

	var notAudited, drift bool
	for _, f := range rep.Findings {
		if f.Check != "schema/drift" {
			continue
		}
		if f.Target == "sg-01" && f.Status == finding.WARN {
			notAudited = true
		}
		if f.Target == "app.users" && f.Status == finding.BAD {
			drift = true
		}
	}
	if !notAudited {
		t.Fatalf("a node that could not be read must be reported: %+v", rep.Findings)
	}
	if !drift {
		t.Fatalf("the two nodes that were read must still be compared: %+v", rep.Findings)
	}
}

// A cluster with no application tables at all is not a cluster whose schema
// could not be read: a fresh cluster is quiet, not a warning.
func TestNoApplicationTablesIsQuiet(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].AppTables = map[string]string{}
	}
	rep := Run(snaps, nil, opts())
	if rep.Worst() != finding.OK {
		t.Fatalf("an empty schema must stay quiet, got %s: %+v", rep.Worst(), rep.Findings)
	}
}

// A node that missed a whole schema drifts on every table in it. One finding
// per table would bury the rest of the report — and the number is the point,
// not the list.
func TestManyDriftedTablesAreSummarised(t *testing.T) {
	snaps := threeHealthy()
	for i := 0; i < 40; i++ {
		table := fmt.Sprintf("app.t%02d", i)
		for n := range snaps {
			snaps[n].AppTables[table] = "1111111111111111"
		}
		snaps[2].AppTables[table] = "9999999999999999"
	}
	rep := Run(snaps, nil, opts())

	fs := byCheck(t, rep, "schema/drift")
	if len(fs) > 6 {
		t.Fatalf("40 drifted tables produced %d findings; the report has to stay readable", len(fs))
	}
	var summary *finding.Finding
	for i := range fs {
		if fs[i].Value != nil && *fs[i].Value == 40 {
			summary = &fs[i]
		}
	}
	if summary == nil {
		t.Fatalf("no finding carries the count of drifted tables: %+v", fs)
	}
	if summary.Status != finding.BAD {
		t.Fatalf("the summary status = %s, want BAD", summary.Status)
	}
}

// A healthy cluster's schema check says what it compared, so "quiet" is
// distinguishable from "did not look".
func TestIdenticalSchemasReportWhatWasCompared(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	f := one(t, rep, "schema/drift")
	if f.Status != finding.OK {
		t.Fatalf("status = %s, want OK", f.Status)
	}
	if f.Value == nil || *f.Value != 2 {
		t.Fatalf("two application tables were compared, got %v: %q", f.Value, f.Message)
	}
	if !strings.Contains(f.Message, "3 node(s)") {
		t.Fatalf("the message must say how many nodes were compared: %q", f.Message)
	}
}
