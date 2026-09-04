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
// serverIDs are the distinct server_id values a real cluster must have: two
// nodes sharing one is a finding, so the fixture cannot share them either.
var serverIDs = map[string]string{"sg-01": "1", "cl-02": "2", "ov-03": "3"}

func healthy(name string, at time.Time) cluster.Snapshot {
	return cluster.Snapshot{
		Node: name,
		At:   at,
		Status: map[string]string{
			"wsrep_cluster_state_uuid":  "5b1e2a8c-1111-11ef-9d2b-000000000001",
			"wsrep_cluster_conf_id":     "42",
			"wsrep_cluster_size":        "3",
			"wsrep_cluster_status":      "Primary",
			"wsrep_local_state_comment": "Synced",
			"wsrep_ready":               "ON",
			"wsrep_connected":           "ON",
			"wsrep_local_recv_queue":    "0",
			"wsrep_local_send_queue":    "0",
			// The cluster's own measurement of how far apart its nodes are:
			// min/avg/max/stddev/samples, in seconds.
			"wsrep_evs_repl_latency":       "0.000228/0.000544/0.001121/0.000202/15",
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
			// State transfer: the settings that cost nothing until a node
			// restarts and has to rejoin.
			"wsrep_sst_method": "mariabackup",
			"wsrep_sst_donor":  "",
			"wsrep_sst_auth":   "********",
			"wsrep_node_name":  name,
			"server_id":        serverIDs[name],
			// What this node is configured to believe about the cluster.
			"wsrep_cluster_address": "gcomm://sg-01,cl-02,ov-03",
			"wsrep_slave_threads":   "4",
			// Everything that decides what leaves the cluster, or what a
			// trigger does when a writeset lands.
			"log_bin":                  "ON",
			"gtid_domain_id":           "1",
			"gtid_strict_mode":         "ON",
			"wsrep_slave_run_triggers": "OFF",
			// Limits and durability: uniform in every diagram, per node in
			// every server.
			"wsrep_max_ws_size":              "2147483647",
			"wsrep_max_ws_rows":              "0",
			"innodb_flush_log_at_trx_commit": "1",
			"sync_binlog":                    "1",
		},
		SysTables: map[string]string{"user": "aaaaaaaaaaaaaaaa", "column_stats": "bbbbbbbbbbbbbbbb"},
		// A non-nil map is "the application schemas were read"; nil is "they
		// were not", which is a different finding from "there are none".
		// The server's own clock, read in the same snapshot.
		Clock: at,
		// Asked, and there is no async replication: an empty slice is a
		// different statement from nil, which means nobody asked.
		Replicas:     []cluster.ReplicaLink{},
		ReplicaHosts: []string{},
		// What a full state transfer would have to copy.
		DataBytes: dataBytes(42 * 1024 * 1024 * 1024),
		AppTables: map[string]string{"app.users": "1111111111111111", "app.events": "2222222222222222"},
	}
}

// dataBytes is the pointer the fixture needs: nil means "not read", which is a
// different statement from "this cluster holds no data".
func dataBytes(n int64) *int64 { return &n }

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

// GD-25 — SST readiness. Nothing here is wrong with the cluster today: it is
// wrong with the next node that restarts, and no counter has an opinion.
func TestDisagreementAboutTheSSTMethodIsFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].Vars["wsrep_sst_method"] = "rsync"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "sst/method")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN", f.Status)
	}
	if !strings.Contains(f.Message, "rsync") || !strings.Contains(f.Message, "cl-02") {
		t.Fatalf("the message must name the method and the node: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "donor") {
		t.Fatalf("the hint has to explain that the donor serves the joiner's method: %q", f.Hint)
	}
}

func TestOneSSTMethodEverywhereIsOK(t *testing.T) {
	f := one(t, Run(threeHealthy(), nil, opts()), "sst/method")
	if f.Status != finding.OK || !strings.Contains(f.Message, "mariabackup") {
		t.Fatalf("got %+v", f)
	}
}

// A donor list is a list of names somebody typed. When it names a server that
// is no longer in the cluster, the node cannot rejoin — and whether it refuses
// to start or quietly falls back is decided by a trailing comma.
func TestADonorThatIsNotInTheClusterIsFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["wsrep_sst_donor"] = "sg-99"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "sst/donor")
	if f.Status != finding.BAD {
		t.Fatalf("a strict donor list naming a node that does not exist is BAD, got %s: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "sg-99") {
		t.Fatalf("the message must name the donor: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "refuse") {
		t.Fatalf("the hint has to say the node will refuse to start: %q", f.Hint)
	}
}

// The same list with a trailing comma falls back to any donor: still worth
// saying, not worth waking somebody for.
func TestADonorListThatFallsBackIsOnlyAWarning(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["wsrep_sst_donor"] = "sg-99,"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "sst/donor")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "sg-99") {
		t.Fatalf("the message must name the donor: %q", f.Message)
	}
}

// A donor named by any spelling the cluster answers to is fine: the node name,
// the configured name, or the address it advertises.
func TestADonorNamedByAnySpellingIsQuiet(t *testing.T) {
	for _, donor := range []string{"cl-02", "ov-03,", "cl-02,ov-03"} {
		snaps := threeHealthy()
		snaps[0].Vars["wsrep_sst_donor"] = donor
		rep := Run(snaps, nil, opts())
		if fs := byCheck(t, rep, "sst/donor"); len(fs) != 0 {
			t.Fatalf("donor %q is in the cluster and must be quiet: %+v", donor, fs)
		}
	}
}

// A backup-based SST needs credentials. An empty wsrep_sst_auth is not proof
// they are missing — they can live in the [sst] section of the config — so it
// is a warning that says exactly that.
func TestABackupSSTWithoutCredentialsWarns(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Vars["wsrep_sst_auth"] = ""
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "sst/auth")
	if f.Status != finding.WARN || f.Target != "ov-03" {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Hint, "[sst]") {
		t.Fatalf("the hint must admit where else the credentials can be: %q", f.Hint)
	}
}

func TestRsyncNeedsNoSSTCredentials(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_sst_method"] = "rsync"
		snaps[i].Vars["wsrep_sst_auth"] = ""
	}
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "sst/auth"); len(fs) != 0 {
		t.Fatalf("rsync does not authenticate: %+v", fs)
	}
}

// A build that does not report the method is not a build that agrees with
// everybody else.
func TestAnUnreportedSSTMethodIsNotGraded(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		delete(snaps[i].Vars, "wsrep_sst_method")
	}
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "sst/method"); len(fs) != 0 {
		t.Fatalf("a variable nobody reported cannot be graded: %+v", fs)
	}
	if rep.Worst() != finding.OK {
		t.Fatalf("and it must not make the cluster look unhealthy: %s", rep.Worst())
	}
}

// GD-26 — a split brain that is already configured. The cluster is Primary and
// green; the settings have already decided what happens at the next partition.
func TestIgnoreSplitBrainLeftOnIsBad(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].Vars["wsrep_provider_options"] = "gcache.size = 512M; pc.ignore_sb = true;"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "quorum/ignore-sb")
	if f.Status != finding.BAD || f.Target != "cl-02" {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Hint, "partition") {
		t.Fatalf("the hint has to say what happens at the next partition: %q", f.Hint)
	}
}

func TestALeftoverBootstrapTriggerWarns(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["wsrep_provider_options"] = "gcache.size = 512M; pc.bootstrap = true;"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "quorum/bootstrap")
	if f.Status != finding.WARN || f.Target != "sg-01" {
		t.Fatalf("got %+v", f)
	}
}

// Weights are the quorum arithmetic. Unequal weights are legal and sometimes
// deliberate, so the finding states the sum rather than an opinion.
func TestUnequalQuorumWeightsAreReportedAsArithmetic(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["wsrep_provider_options"] = "gcache.size = 512M; pc.weight = 3;"
	snaps[1].Vars["wsrep_provider_options"] = "gcache.size = 512M; pc.weight = 1;"
	snaps[2].Vars["wsrep_provider_options"] = "gcache.size = 512M; pc.weight = 1;"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "quorum/weight")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	// 3 of 5: sg-01 alone holds a majority, which is the whole point of saying
	// it out loud.
	if !strings.Contains(f.Message, "sg-01") || !strings.Contains(f.Message, "5") {
		t.Fatalf("the message must carry the arithmetic: %q", f.Message)
	}
}

func TestEqualQuorumWeightsAreOK(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_provider_options"] = "gcache.size = 512M; pc.weight = 1;"
	}
	f := one(t, Run(snaps, nil, opts()), "quorum/weight")
	if f.Status != finding.OK {
		t.Fatalf("got %+v", f)
	}
}

// A node that cannot vote is a cluster of two wearing three nodes' clothes.
func TestANodeWithZeroQuorumWeightIsBad(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Vars["wsrep_provider_options"] = "gcache.size = 512M; pc.weight = 0;"
	rep := Run(snaps, nil, opts())
	var f *finding.Finding
	for i := range rep.Findings {
		if rep.Findings[i].Check == "quorum/weight" && rep.Findings[i].Target == "ov-03" {
			f = &rep.Findings[i]
		}
	}
	if f == nil || f.Status != finding.BAD {
		t.Fatalf("a node with weight 0 never counts towards quorum: %+v", rep.Findings)
	}
}

// A cluster that does not report pc.* at all is not a cluster with pc.* set to
// zero.
func TestMissingProviderOptionsAreNotGraded(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		delete(snaps[i].Vars, "wsrep_provider_options")
	}
	rep := Run(snaps, nil, opts())
	for _, check := range []string{"quorum/weight", "quorum/ignore-sb", "quorum/bootstrap"} {
		if fs := byCheck(t, rep, check); len(fs) != 0 {
			t.Fatalf("%s graded a variable nobody reported: %+v", check, fs)
		}
	}
}

// GD-27 — causal reads that are not. The same query is fresh or stale
// depending on which node the proxy picked, and neither node reports anything.
func TestNodesDisagreeingAboutSyncWaitIsFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["wsrep_sync_wait"] = "1"
	snaps[1].Vars["wsrep_sync_wait"] = "0"
	snaps[2].Vars["wsrep_sync_wait"] = "0"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/sync-wait")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "sg-01") {
		t.Fatalf("the odd node must be named: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "proxy") && !strings.Contains(f.Hint, "which node") {
		t.Fatalf("the hint has to say the answer depends on the node served: %q", f.Hint)
	}
}

// Every node agreeing is not a finding, whatever the value: this tool does not
// have an opinion about whether a cluster wants causal reads.
func TestAgreementAboutSyncWaitIsQuiet(t *testing.T) {
	for _, v := range []string{"0", "1", "7"} {
		snaps := threeHealthy()
		for i := range snaps {
			snaps[i].Vars["wsrep_sync_wait"] = v
		}
		rep := Run(snaps, nil, opts())
		if fs := byCheck(t, rep, "repl/sync-wait"); len(fs) != 0 {
			t.Fatalf("wsrep_sync_wait=%s everywhere is a choice, not a finding: %+v", v, fs)
		}
	}
}

// GD-28 — auto-increment collision on failover.
func TestSharedAutoIncrementOffsetsAreBad(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_auto_increment_control"] = "OFF"
		snaps[i].Vars["auto_increment_increment"] = "3"
		snaps[i].Vars["auto_increment_offset"] = "1"
	}
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/auto-increment")
	if f.Status != finding.BAD {
		t.Fatalf("status = %s, want BAD: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "offset") {
		t.Fatalf("the message must say what collides: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "wsrep_auto_increment_control") {
		t.Fatalf("the hint must name the setting that would fix it: %q", f.Hint)
	}
}

// Galera manages the offsets itself unless somebody turned that off, and then
// the values differing per node is exactly right.
func TestGaleraManagedAutoIncrementIsOK(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_auto_increment_control"] = "ON"
		snaps[i].Vars["auto_increment_increment"] = "3"
		snaps[i].Vars["auto_increment_offset"] = fmt.Sprint(i + 1)
	}
	f := one(t, Run(snaps, nil, opts()), "repl/auto-increment")
	if f.Status != finding.OK {
		t.Fatalf("got %+v", f)
	}
}

// Distinct offsets but a step too small for the cluster: the ids collide as
// soon as the third node takes a write.
func TestAnIncrementSmallerThanTheClusterWarns(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_auto_increment_control"] = "OFF"
		snaps[i].Vars["auto_increment_increment"] = "2"
		snaps[i].Vars["auto_increment_offset"] = fmt.Sprint(i + 1)
	}
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/auto-increment")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "3") {
		t.Fatalf("the message must compare the step with the number of nodes: %q", f.Message)
	}
}

// GD-29 — tables Galera does not replicate at all. The write succeeds, the
// counters stay green, and the row exists on one node.
func TestNonInnoDBTablesAreFound(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].TablesNonInnoDB = []string{"app.legacy (MyISAM)"}
	}
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "schema/engine")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "app.legacy") || !strings.Contains(f.Message, "MyISAM") {
		t.Fatalf("the message must name the table and its engine: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "not replicated") {
		t.Fatalf("the hint has to say the writes do not travel: %q", f.Hint)
	}
}

// Nodes disagreeing about whether MyISAM is replicated is worse than nobody
// replicating it: the same write lands on some nodes and not others.
func TestDisagreementAboutMyisamReplicationIsBad(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].TablesNonInnoDB = []string{"app.legacy (MyISAM)"}
		snaps[i].Vars["wsrep_mode"] = ""
	}
	snaps[1].Vars["wsrep_mode"] = "REPLICATE_MYISAM"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "schema/engine")
	if f.Status != finding.BAD {
		t.Fatalf("status = %s, want BAD: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "cl-02") {
		t.Fatalf("the odd node must be named: %q", f.Message)
	}
}

func TestAnAllInnoDBSchemaIsQuiet(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if fs := byCheck(t, rep, "schema/engine"); len(fs) != 0 {
		t.Fatalf("an InnoDB-only schema is not a finding: %+v", fs)
	}
}

// GD-33 — a restart that throws the gcache away.
//
// gcache/window measures how much time the write-set cache buys before a
// restart needs a full SST. With gcache.recover off, a clean restart discards
// that cache: the window the other check reports is a buffer this setting
// quietly throws away.
func TestGcacheRecoverOffIsFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].Vars["wsrep_provider_options"] = "gcache.size = 512M; gcache.recover = no;"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "gcache/recover")
	if f.Status != finding.WARN || f.Target != "cl-02" {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Hint, "SST") {
		t.Fatalf("the hint has to say what the restart costs: %q", f.Hint)
	}
}

func TestGcacheRecoverOnIsQuiet(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_provider_options"] = "gcache.size = 512M; gcache.recover = yes;"
	}
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "gcache/recover"); len(fs) != 0 {
		t.Fatalf("gcache.recover on is the answer, not a finding: %+v", fs)
	}
}

// A provider that does not report the option is not a provider with it off:
// gcache.recover arrived in Galera 3.19.
func TestAnUnreportedGcacheRecoverIsNotGraded(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if fs := byCheck(t, rep, "gcache/recover"); len(fs) != 0 {
		t.Fatalf("an option nobody reported cannot be graded: %+v", fs)
	}
}

// Each node's restart is its own: two nodes with it off are two findings, not
// one about the cluster.
func TestGcacheRecoverIsReportedPerNode(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["wsrep_provider_options"] = "gcache.size = 512M; gcache.recover = no;"
	snaps[2].Vars["wsrep_provider_options"] = "gcache.size = 512M; gcache.recover = no;"
	rep := Run(snaps, nil, opts())
	fs := byCheck(t, rep, "gcache/recover")
	if len(fs) != 2 {
		t.Fatalf("want one finding per node, got %d: %+v", len(fs), fs)
	}
}

// GD-34 — the DDL method that explains GD-13.
func TestRSUIsFoundAndPointsAtTheDrift(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Vars["wsrep_osu_method"] = "RSU"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/osu-method")
	if f.Status != finding.WARN || f.Target != "ov-03" {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Message, "RSU") {
		t.Fatalf("the message must name the method: %q", f.Message)
	}
	// The whole point of this check is that it is the cause of the other one.
	if !strings.Contains(f.Hint, "schema/drift") {
		t.Fatalf("the hint must connect it to the drift it produces: %q", f.Hint)
	}
}

func TestTOIIsQuiet(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_osu_method"] = "TOI"
	}
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "repl/osu-method"); len(fs) != 0 {
		t.Fatalf("TOI is the default and replicates DDL: %+v", fs)
	}
}

// NBO replicates the DDL too — it is TOI without the cluster-wide lock, not
// RSU with a nicer name.
func TestNBOIsQuiet(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_osu_method"] = "NBO"
	}
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "repl/osu-method"); len(fs) != 0 {
		t.Fatalf("NBO replicates: %+v", fs)
	}
}

func TestAnUnreportedOSUMethodIsNotGraded(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if fs := byCheck(t, rep, "repl/osu-method"); len(fs) != 0 {
		t.Fatalf("a variable nobody reported cannot be graded: %+v", fs)
	}
}

// A drifted schema and a node on RSU in the same report is a diagnosis rather
// than two findings: the second explains the first, and both have to survive
// into the same run.
func TestRSUAndDriftAreBothReported(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Vars["wsrep_osu_method"] = "RSU"
	snaps[2].AppTables["app.users"] = "9999999999999999"
	rep := Run(snaps, nil, opts())

	osu := one(t, rep, "repl/osu-method")
	if osu.Target != "ov-03" {
		t.Fatalf("the RSU node must be named: %+v", osu)
	}
	var drift bool
	for _, f := range rep.Findings {
		if f.Check == "schema/drift" && f.Target == "app.users" && f.Status == finding.BAD {
			drift = true
		}
	}
	if !drift {
		t.Fatalf("the drift must still be reported next to its cause: %+v", rep.Findings)
	}
}

// GD-16 — node clock skew. Certification does not need the clocks to agree;
// every human reading two error logs side by side does.
func TestClockSkewBetweenNodesIsFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].Clock = now.Add(4 * time.Second)
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "node/clock")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "cl-02") {
		t.Fatalf("the node that is out must be named: %q", f.Message)
	}
	if f.Value == nil || *f.Value < 3.9 || *f.Value > 4.1 {
		t.Fatalf("the spread has to be the measurement: %v", f.Value)
	}
}

func TestALargeClockSkewIsBad(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Clock = now.Add(-5 * time.Minute)
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "node/clock")
	if f.Status != finding.BAD {
		t.Fatalf("status = %s, want BAD: %+v", f.Status, f)
	}
}

func TestAgreeingClocksAreOK(t *testing.T) {
	f := one(t, Run(threeHealthy(), nil, opts()), "node/clock")
	if f.Status != finding.OK {
		t.Fatalf("got %+v", f)
	}
}

// A clock that was not read is not a clock that agrees.
func TestAnUnreadClockIsNotGraded(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Clock = time.Time{}
	}
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "node/clock"); len(fs) != 0 {
		t.Fatalf("a clock nobody read cannot be graded: %+v", fs)
	}
}

// GD-30 — write-set limits that disagree. The transaction certifies on the node
// that accepted it and is refused by the applier with the smaller limit, which
// reads as a node failure rather than as the configuration difference it is.
func TestDisagreeingWriteSetLimitsAreFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Vars["wsrep_max_ws_size"] = "10485760"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/ws-limits")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "ov-03") {
		t.Fatalf("the node with the smallest limit must be named: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "node failure") {
		t.Fatalf("the hint has to say how it will be misread: %q", f.Hint)
	}
}

func TestAgreeingWriteSetLimitsAreQuiet(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if fs := byCheck(t, rep, "repl/ws-limits"); len(fs) != 0 {
		t.Fatalf("equal limits are not a finding: %+v", fs)
	}
}

// GD-35 — durability is the weakest node's, not the average.
func TestWeakerDurabilityOnOneNodeIsFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["innodb_flush_log_at_trx_commit"] = "2"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "node/durability")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "sg-01") {
		t.Fatalf("the weakest node must be named: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "weakest") {
		t.Fatalf("the hint has to say what the cluster's durability actually is: %q", f.Hint)
	}
}

func TestUniformDurabilityIsQuiet(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if fs := byCheck(t, rep, "node/durability"); len(fs) != 0 {
		t.Fatalf("uniform durability is a choice, not a finding: %+v", fs)
	}
}

// Every node set to the same relaxed value is the cluster's decision, not a
// drift — this tool does not have an opinion about it.
func TestUniformlyRelaxedDurabilityIsQuiet(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["innodb_flush_log_at_trx_commit"] = "0"
		snaps[i].Vars["sync_binlog"] = "0"
	}
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "node/durability"); len(fs) != 0 {
		t.Fatalf("a uniform choice is not a finding: %+v", fs)
	}
}

// GD-31 — the segment map. Reported as the map it is, because the intent
// behind it lives in somebody's head and not in the server.
func TestTheSegmentMapIsReported(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["wsrep_provider_options"] = "gcache.size = 512M; gmcast.segment = 0;"
	snaps[1].Vars["wsrep_provider_options"] = "gcache.size = 512M; gmcast.segment = 0;"
	snaps[2].Vars["wsrep_provider_options"] = "gcache.size = 512M; gmcast.segment = 1;"
	f := one(t, Run(snaps, nil, opts()), "cluster/segments")
	if f.Status != finding.OK {
		t.Fatalf("two segments over three nodes is a normal topology: %+v", f)
	}
	if !strings.Contains(f.Message, "sg-01") || !strings.Contains(f.Message, "ov-03") {
		t.Fatalf("the map must name the nodes: %q", f.Message)
	}
}

// Every node in its own segment turns off the one thing segments are for: one
// copy of a write-set per segment instead of one per node.
func TestEveryNodeInItsOwnSegmentWarns(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_provider_options"] = fmt.Sprintf("gcache.size = 512M; gmcast.segment = %d;", i)
	}
	f := one(t, Run(snaps, nil, opts()), "cluster/segments")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Hint, "per segment") {
		t.Fatalf("the hint has to say what segments buy: %q", f.Hint)
	}
}

func TestNoSegmentsReportedIsNotGraded(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if fs := byCheck(t, rep, "cluster/segments"); len(fs) != 0 {
		t.Fatalf("an option nobody reported cannot be graded: %+v", fs)
	}
}

// GD-38 — the peer list against the membership. A node starts fine today and
// cannot find the cluster after a restart, because the list it was given
// describes a cluster that no longer exists.
func TestAPeerThatIsNotInTheClusterIsFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["wsrep_cluster_address"] = "gcomm://sg-01,cl-02,ov-99"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "cluster/peers")
	if f.Status != finding.WARN || f.Target != "sg-01" {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Message, "ov-99") {
		t.Fatalf("the message must name the peer that is not there: %q", f.Message)
	}
}

// A list naming nobody who is currently a member: this node cannot rejoin at
// all, and nothing says so until it tries.
func TestAPeerListWithNoCurrentMemberIsBad(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Vars["wsrep_cluster_address"] = "gcomm://old-01,old-02"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "cluster/peers")
	if f.Status != finding.BAD || f.Target != "ov-03" {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Hint, "rejoin") {
		t.Fatalf("the hint has to say what happens at the next restart: %q", f.Hint)
	}
}

// An empty gcomm:// is the bootstrap form. Left in a running node's
// configuration it is a split brain waiting for a restart.
func TestAnEmptyClusterAddressIsBad(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].Vars["wsrep_cluster_address"] = "gcomm://"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "cluster/peers")
	if f.Status != finding.BAD || f.Target != "cl-02" {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Message, "bootstrap") && !strings.Contains(f.Hint, "bootstrap") {
		t.Fatalf("the finding has to say it would bootstrap its own cluster: %+v", f)
	}
}

// The list is written by a human: a peer named by address and port is the same
// peer, and a node that is right there must not be reported as missing.
func TestPeersNamedByAddressAndPortAreMatched(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["wsrep_node_address"] = "10.11.1.5"
	snaps[1].Vars["wsrep_cluster_address"] = "gcomm://10.11.1.5:4567,ov-03"
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "cluster/peers"); len(fs) != 0 {
		t.Fatalf("a peer named by address and port is in the cluster: %+v", fs)
	}
}

func TestAGoodPeerListIsQuiet(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if fs := byCheck(t, rep, "cluster/peers"); len(fs) != 0 {
		t.Fatalf("a list that names the cluster is not a finding: %+v", fs)
	}
}

func TestAnUnreportedClusterAddressIsNotGraded(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		delete(snaps[i].Vars, "wsrep_cluster_address")
	}
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "cluster/peers"); len(fs) != 0 {
		t.Fatalf("a variable nobody reported cannot be graded: %+v", fs)
	}
}

// GD-39 — flow control that one node decides for everybody. flow/paused
// reports the symptom; this is the reason.
func TestASmallerFlowControlLimitIsFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Vars["wsrep_provider_options"] = "gcache.size = 512M; gcs.fc_limit = 4;"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "flow/settings")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "ov-03") {
		t.Fatalf("the node that paces the cluster must be named: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "flow/paused") {
		t.Fatalf("the hint has to point at the check that reports the symptom: %q", f.Hint)
	}
}

func TestEqualFlowControlLimitsAreQuiet(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if fs := byCheck(t, rep, "flow/settings"); len(fs) != 0 {
		t.Fatalf("equal limits are not a finding: %+v", fs)
	}
}

func TestADifferentFlowControlFactorIsFound(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_provider_options"] = "gcache.size = 512M; gcs.fc_limit = 16; gcs.fc_factor = 1.0;"
	}
	snaps[0].Vars["wsrep_provider_options"] = "gcache.size = 512M; gcs.fc_limit = 16; gcs.fc_factor = 0.5;"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "flow/settings")
	if f.Status != finding.WARN || !strings.Contains(f.Message, "fc_factor") {
		t.Fatalf("got %+v", f)
	}
}

// GD-40 — appliers that are not the same size. Slower by configuration is a
// different fix from "look at its disk".
func TestFewerApplierThreadsAreFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].Vars["wsrep_slave_threads"] = "1"
	snaps[1].Status["wsrep_local_recv_queue"] = "12"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/appliers")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "cl-02") {
		t.Fatalf("the node with fewer appliers must be named: %q", f.Message)
	}
	// The queue is the reason this matters, so it belongs in the same line.
	if !strings.Contains(f.Message, "12") {
		t.Fatalf("the message must carry that node's receive queue: %q", f.Message)
	}
}

func TestEqualApplierThreadsAreQuiet(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if fs := byCheck(t, rep, "repl/appliers"); len(fs) != 0 {
		t.Fatalf("equal applier counts are not a finding: %+v", fs)
	}
}

// MariaDB 10.6 renamed the variable and kept the old one as an alias; a build
// that only reports the new name must still be compared.
func TestTheNewerApplierThreadsNameIsUnderstood(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		delete(snaps[i].Vars, "wsrep_slave_threads")
		snaps[i].Vars["wsrep_applier_threads"] = "8"
	}
	snaps[0].Vars["wsrep_applier_threads"] = "2"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/appliers")
	if f.Status != finding.WARN || !strings.Contains(f.Message, "sg-01") {
		t.Fatalf("got %+v", f)
	}
}

// GD-41 — what a rejoin will actually copy. "This node needs a full SST" is
// not actionable without the number of gigabytes it implies and the donor it
// takes out of service while it happens.
func TestTheSSTSizeIsReported(t *testing.T) {
	f := one(t, Run(threeHealthy(), nil, opts()), "sst/size")
	if f.Status != finding.OK {
		t.Fatalf("a size is a number, not a fault: %+v", f)
	}
	if !strings.Contains(f.Message, "42") {
		t.Fatalf("the message must carry the size: %q", f.Message)
	}
	if !strings.Contains(f.Message, "mariabackup") {
		t.Fatalf("the method decides how the copy happens, so it belongs here: %q", f.Message)
	}
	if f.Value == nil || *f.Value != float64(42*1024*1024*1024) {
		t.Fatalf("the value has to be the byte count: %v", f.Value)
	}
	if f.Unit != "bytes" {
		t.Fatalf("unit = %q", f.Unit)
	}
}

func TestAnUnreadDatasetSizeIsNotGraded(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].DataBytes = nil
	}
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "sst/size"); len(fs) != 0 {
		t.Fatalf("a size nobody read cannot be reported: %+v", fs)
	}
}

// GD-43 — what this run could not audit. A cron job needs one line to know
// whether "no findings" meant "nothing is wrong".
func TestCoverageIsReportedWhenEverythingWasAudited(t *testing.T) {
	f := one(t, Run(threeHealthy(), nil, opts()), "audit/coverage")
	if f.Status != finding.OK {
		t.Fatalf("status = %s, want OK: %+v", f.Status, f)
	}
	// Without --state the counter checks report lifetime totals, and the
	// coverage line is where a cron job finds that out.
	if !strings.Contains(f.Message, "no baseline") {
		t.Fatalf("the message must say the counters were not graded: %q", f.Message)
	}
}

func TestCoverageNamesANodeThatCouldNotBeRead(t *testing.T) {
	snaps := threeHealthy()
	snaps = append(snaps, cluster.Snapshot{Node: "dr-04", At: now, Err: "dial tcp: i/o timeout"})
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "audit/coverage")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "dr-04") {
		t.Fatalf("the node that was not audited must be named: %q", f.Message)
	}
}

func TestCoverageNamesAMissingGrant(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].SysTables = nil
	snaps[1].AppTables = nil
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "audit/coverage")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "cl-02") {
		t.Fatalf("the node whose schema could not be read must be named: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "OK") {
		t.Fatalf("the hint has to say an OK from a check that never ran is not an OK: %q", f.Hint)
	}
}

// With a baseline the counter checks are graded, and the coverage line says so
// instead of warning about it.
func TestCoverageWithABaselineDoesNotMentionIt(t *testing.T) {
	snaps := threeHealthy()
	prev := state.New(snaps, now.Add(-10*time.Minute))
	f := one(t, Run(snaps, &prev, opts()), "audit/coverage")
	if f.Status != finding.OK {
		t.Fatalf("status = %s, want OK: %+v", f.Status, f)
	}
	if strings.Contains(f.Message, "no baseline") {
		t.Fatalf("there is a baseline: %q", f.Message)
	}
}

// GD-32 — what changed since the last run.
//
// The person reading this ran the audit twenty minutes ago and did something
// in between. The one thing they need is not the list of findings again: it is
// which of them appeared, which cleared, and which got worse.
func TestNoBaselineMeansNoChangeReport(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if fs := byCheck(t, rep, "audit/changes"); len(fs) != 0 {
		t.Fatalf("without a previous run there are no transitions to report: %+v", fs)
	}
}

func TestAClearedFindingIsReported(t *testing.T) {
	snaps := threeHealthy()
	prev := state.New(snaps, now.Add(-20*time.Minute))
	prev.Findings = map[string]string{"systables/drift@mysql.column_stats": "BAD"}

	rep := Run(snaps, &prev, opts())
	f := one(t, rep, "audit/changes")
	if f.Status != finding.OK {
		t.Fatalf("a transition summary does not add severity of its own: %+v", f)
	}
	if !strings.Contains(f.Message, "cleared") || !strings.Contains(f.Message, "systables/drift") {
		t.Fatalf("the message must say what cleared: %q", f.Message)
	}
}

func TestANewFindingIsReportedAsAppeared(t *testing.T) {
	snaps := threeHealthy()
	prev := state.New(snaps, now.Add(-20*time.Minute))
	prev.Findings = map[string]string{"cluster/size@compress": "OK"}
	snaps[1].SysTables["column_stats"] = "cccccccccccccccc"

	rep := Run(snaps, &prev, opts())
	f := one(t, rep, "audit/changes")
	if !strings.Contains(f.Message, "appeared") || !strings.Contains(f.Message, "systables/drift") {
		t.Fatalf("the message must say what appeared: %q", f.Message)
	}
}

func TestAWorseFindingIsReportedAsWorse(t *testing.T) {
	snaps := threeHealthy()
	prev := state.New(snaps, now.Add(-20*time.Minute))
	prev.Findings = map[string]string{"cluster/primary@cl-02": "WARN"}
	snaps[1].Status["wsrep_cluster_status"] = "non-Primary"

	rep := Run(snaps, &prev, opts())
	f := one(t, rep, "audit/changes")
	if !strings.Contains(f.Message, "worse") || !strings.Contains(f.Message, "cluster/primary") {
		t.Fatalf("the message must say what got worse, and from what: %q", f.Message)
	}
	if !strings.Contains(f.Message, "WARN") || !strings.Contains(f.Message, "BAD") {
		t.Fatalf("both statuses belong in the line: %q", f.Message)
	}
}

// A run that changed nothing is worth one line saying so: that is the answer a
// cron job and an incident channel are both waiting for.
func TestNothingChangedIsSaidOutLoud(t *testing.T) {
	snaps := threeHealthy()
	first := Run(snaps, nil, opts())
	prev := first.State
	prev.At = now.Add(-20 * time.Minute)

	rep := Run(snaps, &prev, opts())
	f := one(t, rep, "audit/changes")
	if f.Status != finding.OK || !strings.Contains(f.Message, "nothing changed") {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Message, "20m") {
		t.Fatalf("the interval is the point of the line: %q", f.Message)
	}
}

// The transition summary must not be part of what it compares, or every run
// after the first reports itself as a change.
func TestTheChangeReportIsNotItsOwnFinding(t *testing.T) {
	snaps := threeHealthy()
	first := Run(snaps, nil, opts())
	for key := range first.State.Findings {
		if strings.HasPrefix(key, "audit/changes@") {
			t.Fatalf("the change report stored itself: %+v", first.State.Findings)
		}
	}
	second := Run(snaps, &first.State, opts())
	f := one(t, second, "audit/changes")
	if !strings.Contains(f.Message, "nothing changed") {
		t.Fatalf("a second identical run has nothing to report: %q", f.Message)
	}
}

// The findings of the run are what the next run compares against, so they have
// to be in the state the report carries.
func TestTheReportCarriesItsFindingsForwards(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if len(rep.State.Findings) == 0 {
		t.Fatal("the state to persist carries no findings: the next run would have no baseline")
	}
	if got, ok := rep.State.Findings["cluster/size@compress"]; !ok || got != "OK" {
		t.Fatalf("cluster/size is missing or wrong in the carried state: %q %v", got, ok)
	}
}

// A run that could not audit anything still has to be comparable, or "the node
// came back" is a transition nobody reports.
func TestAnUnreadableClusterStillCarriesItsFindingsForwards(t *testing.T) {
	snaps := []cluster.Snapshot{{Node: "sg-01", At: now, Err: "dial tcp: i/o timeout"}}
	rep := Run(snaps, nil, opts())
	if len(rep.State.Findings) == 0 {
		t.Fatalf("nothing was carried forward: %+v", rep.Findings)
	}
	if rep.State.Findings["cluster/membership@compress"] != "ERROR" {
		t.Fatalf("the membership ERROR must be in the carried state: %+v", rep.State.Findings)
	}

	// And the next run, with the node back, reports the transition.
	prev := rep.State
	prev.At = now.Add(-10 * time.Minute)
	back := Run(threeHealthy(), &prev, opts())
	f := one(t, back, "audit/changes")
	if !strings.Contains(f.Message, "cleared") || !strings.Contains(f.Message, "cluster/membership") {
		t.Fatalf("a cluster that came back is the clearest transition there is: %q", f.Message)
	}
}

// GD-17 — slow, or simply far away.
//
// A deep send queue is reported by queue/send and says nothing about the
// cause: a node across a WAN link is doing exactly what physics allows, and a
// node with a failing disk in the same rack looks identical from there. The
// cluster measures the round trip itself, in wsrep_evs_repl_latency, and the
// segment map says which pairs are supposed to be far apart.
func TestANodeSlowWithinItsOwnSegmentIsFound(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_provider_options"] = "gcache.size = 512M; gmcast.segment = 0;"
	}
	// 12ms inside a segment whose other members are at half a millisecond.
	snaps[1].Status["wsrep_evs_repl_latency"] = "0.008/0.012/0.030/0.004/15"
	snaps[1].Status["wsrep_local_send_queue"] = "9"

	rep := Run(snaps, nil, opts())
	var f *finding.Finding
	for i := range rep.Findings {
		if rep.Findings[i].Check == "cluster/latency" && rep.Findings[i].Target == "cl-02" {
			f = &rep.Findings[i]
		}
	}
	if f == nil || f.Status != finding.WARN {
		t.Fatalf("a node slow inside its own segment is a finding: %+v", rep.Findings)
	}
	if !strings.Contains(f.Message, "segment 0") {
		t.Fatalf("the message must say the comparison was inside one segment: %q", f.Message)
	}
	// The whole point is the attribution, so the hint must rule distance out.
	if !strings.Contains(f.Hint, "not distance") {
		t.Fatalf("the hint has to say this is not distance: %q", f.Hint)
	}
	if !strings.Contains(f.Hint, "9") {
		t.Fatalf("the hint must carry that node's send queue: %q", f.Hint)
	}
}

// The same latency in a segment of its own is a WAN link behaving like a WAN
// link. Reported, never graded.
func TestANodeFarAwayIsNotAFinding(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["wsrep_provider_options"] = "gcache.size = 512M; gmcast.segment = 0;"
	snaps[1].Vars["wsrep_provider_options"] = "gcache.size = 512M; gmcast.segment = 0;"
	snaps[2].Vars["wsrep_provider_options"] = "gcache.size = 512M; gmcast.segment = 1;"
	snaps[2].Status["wsrep_evs_repl_latency"] = "0.020/0.024/0.031/0.003/15"

	rep := Run(snaps, nil, opts())
	for _, f := range byCheck(t, rep, "cluster/latency") {
		if f.Target == "ov-03" && f.Status != finding.OK {
			t.Fatalf("a node in its own segment is far away, not slow: %+v", f)
		}
	}
}

// The map itself is worth one line: it is what makes "far away" a fact rather
// than an assumption.
func TestTheLatencyMapIsReported(t *testing.T) {
	f := one(t, Run(threeHealthy(), nil, opts()), "cluster/latency")
	if f.Status != finding.OK {
		t.Fatalf("a healthy cluster's latency is a measurement, not a fault: %+v", f)
	}
	// A duration in whatever unit reads best at that magnitude: 544µs, not
	// 0.000544 seconds and not 0.544 milliseconds.
	if !strings.Contains(f.Message, "µs") && !strings.Contains(f.Message, "ms") {
		t.Fatalf("the message must carry the measurement in something readable: %q", f.Message)
	}
	if !strings.Contains(f.Message, "segment 0") {
		t.Fatalf("the map must name the segments: %q", f.Message)
	}
}

// Microsecond differences are not a finding: a 4x ratio between 90µs and 350µs
// is noise, and grading it is how a check becomes something people switch off.
func TestSmallLatencyDifferencesAreNotGraded(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].Status["wsrep_evs_repl_latency"] = "0.0002/0.00035/0.0009/0.0001/15"
	rep := Run(snaps, nil, opts())
	for _, f := range byCheck(t, rep, "cluster/latency") {
		if f.Status != finding.OK {
			t.Fatalf("a fraction of a millisecond is not a finding: %+v", f)
		}
	}
}

// A provider that does not report the metric is not a cluster with no latency.
func TestAnUnreportedLatencyIsNotGraded(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		delete(snaps[i].Status, "wsrep_evs_repl_latency")
	}
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "cluster/latency"); len(fs) != 0 {
		t.Fatalf("a metric nobody reported cannot be graded: %+v", fs)
	}
}

// Galera prints the counter as min/avg/max/stddev/samples and zeroes it when
// no samples have been taken. Zero samples is not zero latency.
func TestALatencyWithNoSamplesIsNotGraded(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Status["wsrep_evs_repl_latency"] = "0/0/0/0/0"
	}
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "cluster/latency"); len(fs) != 0 {
		t.Fatalf("no samples means nothing was measured: %+v", fs)
	}
}

// GD-48 — GTID domains that do not agree.
//
// Nothing inside the cluster is affected by any of this, which is exactly why
// nothing reports it: it is the replicas *downstream* that discover a failover
// rewrote their history.
func TestDuplicateServerIDsAreBad(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Vars["server_id"] = "1" // the same as sg-01
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/server-id")
	if f.Status != finding.BAD {
		t.Fatalf("status = %s, want BAD: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "sg-01") || !strings.Contains(f.Message, "ov-03") {
		t.Fatalf("both nodes sharing the id must be named: %q", f.Message)
	}
}

func TestDistinctServerIDsAreQuiet(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if fs := byCheck(t, rep, "repl/server-id"); len(fs) != 0 {
		t.Fatalf("distinct ids are the requirement, not a finding: %+v", fs)
	}
}

func TestDisagreeingGTIDDomainsAreFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].Vars["gtid_domain_id"] = "7"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/gtid-domain")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "cl-02") {
		t.Fatalf("the odd node must be named: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "downstream") {
		t.Fatalf("the hint has to say who finds out: %q", f.Hint)
	}
}

// Without a binary log nothing can replicate out of the cluster, and a domain
// id that cannot reach anybody is not a finding. This tool does not have
// opinions about settings nothing reads.
func TestGTIDDomainsAreNotGradedWithoutABinlog(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["log_bin"] = "OFF"
	}
	snaps[1].Vars["gtid_domain_id"] = "7"
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "repl/gtid-domain"); len(fs) != 0 {
		t.Fatalf("nothing can replicate out, so nothing is affected: %+v", fs)
	}
	// A duplicate server_id is still wrong: it breaks the cluster's own
	// internals, not only what leaves it.
	snaps[2].Vars["server_id"] = "1"
	rep = Run(snaps, nil, opts())
	if f := byCheck(t, rep, "repl/server-id"); len(f) != 1 {
		t.Fatalf("a duplicate server_id is wrong with or without a binlog: %+v", f)
	}
}

func TestDisagreeingGTIDStrictModeIsFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["gtid_strict_mode"] = "OFF"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/gtid-strict")
	if f.Status != finding.WARN || !strings.Contains(f.Message, "sg-01") {
		t.Fatalf("got %+v", f)
	}
}

func TestAnUnreportedServerIDIsNotGraded(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		delete(snaps[i].Vars, "server_id")
		delete(snaps[i].Vars, "gtid_domain_id")
	}
	rep := Run(snaps, nil, opts())
	for _, check := range []string{"repl/server-id", "repl/gtid-domain"} {
		if fs := byCheck(t, rep, check); len(fs) != 0 {
			t.Fatalf("%s graded a variable nobody reported: %+v", check, fs)
		}
	}
}

// GD-50 — triggers that run on one node only.
//
// The writer's trigger has already put its rows in the writeset. An applier
// that runs the trigger again applies them twice; an applier that does not is
// doing the right thing. So the nodes disagreeing is divergence produced by
// design, and no counter has an opinion about it.
func TestNodesDisagreeingAboutApplierTriggersIsBad(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].Vars["wsrep_slave_run_triggers"] = "ON"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/triggers")
	if f.Status != finding.BAD {
		t.Fatalf("status = %s, want BAD: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "cl-02") {
		t.Fatalf("the odd node must be named: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "twice") {
		t.Fatalf("the hint has to say what the applier does with the writeset: %q", f.Hint)
	}
}

// Every node running them is a deliberate choice with a real hazard: worth
// saying once, not worth calling an outage.
func TestApplierTriggersEverywhereWarns(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_slave_run_triggers"] = "ON"
	}
	f := one(t, Run(snaps, nil, opts()), "repl/triggers")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
}

func TestApplierTriggersOffEverywhereIsQuiet(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	if fs := byCheck(t, rep, "repl/triggers"); len(fs) != 0 {
		t.Fatalf("off everywhere is the default and the right answer: %+v", fs)
	}
}

// GD-47 — async replication attached to the cluster.
//
// A cluster diagram shows three nodes replicating to each other. It does not
// show the node that is also an async replica of something else, or the one
// feeding a reporting replica downstream — and the cluster cannot see a write
// path it is not part of.
func TestANodeThatIsAlsoAnAsyncReplicaIsFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[1].Replicas = []cluster.ReplicaLink{{Source: "legacy-01", IORunning: true, SQLRunning: true}}
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/async-in")
	if f.Status != finding.WARN || f.Target != "cl-02" {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Message, "legacy-01") {
		t.Fatalf("the source must be named: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "write path") {
		t.Fatalf("the hint has to say what it is: %q", f.Hint)
	}
}

// A configured link that is not running is worse than one that is: somebody
// believes those writes are arriving.
func TestAStoppedAsyncLinkIsBad(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Replicas = []cluster.ReplicaLink{{Source: "legacy-01", IORunning: false, SQLRunning: true, LastError: "Could not connect"}}
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/async-in")
	if f.Status != finding.BAD {
		t.Fatalf("status = %s, want BAD: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "not running") {
		t.Fatalf("the message must say the link is down: %q", f.Message)
	}
	// The server's own error belongs in the finding; it is the one thing that
	// says why.
	if !strings.Contains(f.Message, "Could not connect") && !strings.Contains(f.Hint, "Could not connect") {
		t.Fatalf("the server's error was dropped: %+v", f)
	}
}

// A member feeding a downstream replica is a dependency nobody else in the
// cluster knows about — and the next SST rebuilds its binlogs out from under it.
func TestANodeFeedingADownstreamReplicaIsFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].ReplicaHosts = []string{"reporting-01", "reporting-02"}
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "repl/async-out")
	if f.Status != finding.WARN || f.Target != "ov-03" {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Message, "2") {
		t.Fatalf("the message must carry how many: %q", f.Message)
	}
	if !strings.Contains(f.Hint, "SST") {
		t.Fatalf("the hint has to say what a state transfer does to them: %q", f.Hint)
	}
}

func TestAClusterWithNoAsyncReplicationIsQuiet(t *testing.T) {
	rep := Run(threeHealthy(), nil, opts())
	for _, check := range []string{"repl/async-in", "repl/async-out"} {
		if fs := byCheck(t, rep, check); len(fs) != 0 {
			t.Fatalf("%s fired on a cluster with no async replication: %+v", check, fs)
		}
	}
}

// A node that could not be asked is not a node with no replication: nil means
// not read, and the coverage line is where that is said.
func TestAnUnreadReplicaStatusIsNotGraded(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Replicas = nil
		snaps[i].ReplicaHosts = nil
	}
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "repl/async-in"); len(fs) != 0 {
		t.Fatalf("nil must not be read as a running link: %+v", fs)
	}
	f := one(t, rep, "audit/coverage")
	if !strings.Contains(f.Message, "replication status") {
		t.Fatalf("coverage has to say the replication status was not read: %q", f.Message)
	}
}
