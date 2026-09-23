package audit

// GD-77 — the nine checks that had only their quiet half.
//
// Every one of these was walked by the healthy fixture, which is why statement
// coverage of this package sat at 95.7% while nothing anywhere asserted that
// they *fire*, or that they name the node responsible when they do. A check
// that cannot go red in the suite is a check whose red path ships untried, and
// the failure it produces in the field — wrong node named, wrong severity — is
// the one nobody can reproduce.
//
// Each test here plants the condition on exactly one node of a three-node
// cluster and asserts the finding by check id, by target and by status. The
// healthy-stays-quiet half already existed for most of these in
// TestHealthyClusterIsQuiet; where it did not, it is here too.

import (
	"strings"
	"testing"
	"time"

	"github.com/Allan-Nava/galera-doctor/internal/finding"
)

// --- node state: ready, connected, desync, read_only ------------------------

func TestANodeThatIsNotConnectedIsFound(t *testing.T) {
	// wsrep_connected OFF is further out than not being Synced: the node is
	// not in the group communication at all, so it is not lagging, it is gone.
	snaps := threeHealthy()
	snaps[1].Status["wsrep_connected"] = "OFF"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "node/connected")
	if f.Status != finding.BAD {
		t.Fatalf("status = %s, want BAD: %+v", f.Status, f)
	}
	if f.Target != "cl-02" {
		t.Fatalf("target = %q, want the disconnected node", f.Target)
	}
}

func TestANodeThatIsNotReadyIsFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Status["wsrep_ready"] = "OFF"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "node/ready")
	if f.Status != finding.BAD || f.Target != "ov-03" {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Hint, "refusing queries") {
		t.Fatalf("the hint has to say what it means for whoever is on call: %q", f.Hint)
	}
}

func TestADesyncedNodeIsAWarningAndNotAFailure(t *testing.T) {
	// wsrep_desync ON is how a backup or a schema change is supposed to look,
	// so it cannot be BAD — but left on, the node drifts without ever
	// flow-controlling the cluster, which is why it cannot be silent either.
	// Planted on the second node on purpose: a target assertion against the
	// first one passes even when the check reports live[0] regardless.
	snaps := threeHealthy()
	snaps[1].Vars["wsrep_desync"] = "ON"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "node/desync")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if f.Target != "cl-02" {
		t.Fatalf("target = %q, want the desynced node", f.Target)
	}
}

func TestADesyncThatIsOffIsNotAFinding(t *testing.T) {
	snaps := threeHealthy()
	for i := range snaps {
		snaps[i].Vars["wsrep_desync"] = "OFF"
	}
	if fs := byCheck(t, Run(snaps, nil, opts()), "node/desync"); len(fs) != 0 {
		t.Fatalf("wsrep_desync OFF is the normal state: %+v", fs)
	}
}

func TestAReadOnlyNodeIsFound(t *testing.T) {
	// Expected on a node deliberately kept out of the write path; unexpected,
	// it is a failover that never finished — which is why it is reported and
	// not graded harder.
	snaps := threeHealthy()
	snaps[1].Vars["read_only"] = "ON"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "node/read-only")
	if f.Status != finding.WARN || f.Target != "cl-02" {
		t.Fatalf("got %+v", f)
	}
}

// --- queues -----------------------------------------------------------------

func TestADeepReceiveQueueIsFoundAndAttributed(t *testing.T) {
	snaps := threeHealthy()
	snaps[2].Status["wsrep_local_recv_queue"] = "250"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "queue/recv")
	if f.Status != finding.WARN || f.Target != "ov-03" {
		t.Fatalf("got %+v", f)
	}
	if f.Value == nil || *f.Value != 250 {
		t.Fatalf("the depth belongs in Value, not only in the prose: %+v", f)
	}
	if f.Unit != "writesets" {
		t.Fatalf("unit = %q", f.Unit)
	}
}

func TestADeepSendQueueIsFoundAndIsADifferentCheck(t *testing.T) {
	// Send and receive point at different things — the network and the
	// applier — so a node deep in one must not be reported as deep in both.
	snaps := threeHealthy()
	snaps[0].Status["wsrep_local_send_queue"] = "80"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "queue/send")
	if f.Status != finding.WARN || f.Target != "sg-01" {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Hint, "network") {
		t.Fatalf("a send queue points at the network first: %q", f.Hint)
	}
	if fs := byCheck(t, rep, "queue/recv"); len(fs) != 0 {
		t.Fatalf("a deep send queue is not a deep receive queue: %+v", fs)
	}
}

func TestAQueueBelowTheThresholdIsQuiet(t *testing.T) {
	o := opts()
	snaps := threeHealthy()
	snaps[0].Status["wsrep_local_recv_queue"] = trimFloat(o.RecvQueueWarn - 1)
	rep := Run(snaps, nil, o)
	if fs := byCheck(t, rep, "queue/recv"); len(fs) != 0 {
		t.Fatalf("below the threshold is not a finding: %+v", fs)
	}
}

// --- cluster identity: the configuration id ---------------------------------

func TestNodesDisagreeingAboutTheConfigurationIdAreFound(t *testing.T) {
	// One UUID and two conf ids is a membership change caught mid-flight, or a
	// node that stopped receiving membership updates. It is a warning, not a
	// partition — the partition is cluster/uuid, and the two must not be
	// confused.
	snaps := threeHealthy()
	snaps[2].Status["wsrep_cluster_conf_id"] = "43"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "cluster/conf-id")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s, want WARN: %+v", f.Status, f)
	}
	if f.Target != "compress" {
		t.Fatalf("a disagreement is about the cluster, not one node: %q", f.Target)
	}
	if !strings.Contains(f.Message, "43") || !strings.Contains(f.Message, "42") {
		t.Fatalf("both generations have to be in the message: %q", f.Message)
	}
	if fs := byCheck(t, rep, "cluster/uuid"); len(fs) != 0 {
		t.Fatalf("a conf-id disagreement is not a split brain: %+v", fs)
	}
}

func TestAConfIdDisagreementIsNotReportedOnTopOfASplitBrain(t *testing.T) {
	// When the UUIDs already differ, the conf ids differ as a consequence.
	// Reporting both would describe two incidents where there is one, and the
	// second one is the less serious of the two.
	snaps := threeHealthy()
	snaps[2].Status["wsrep_cluster_state_uuid"] = "5b1e2a8c-2222-11ef-9d2b-000000000002"
	snaps[2].Status["wsrep_cluster_conf_id"] = "1"
	rep := Run(snaps, nil, opts())
	if fs := byCheck(t, rep, "cluster/uuid"); len(fs) != 1 {
		t.Fatalf("the split brain is the finding: %+v", fs)
	}
	if fs := byCheck(t, rep, "cluster/conf-id"); len(fs) != 0 {
		t.Fatalf("conf-id is a consequence here, not a second incident: %+v", fs)
	}
}

// --- mixed provider versions ------------------------------------------------

func TestMixedProviderVersionsAreFound(t *testing.T) {
	// Distinct from cluster/versions: the server can be uniform while the
	// wsrep provider under it is not, and it is the provider that negotiates
	// the group communication protocol down to its oldest member.
	snaps := threeHealthy()
	snaps[1].Status["wsrep_provider_version"] = "26.4.11(r)"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "cluster/provider-version")
	if f.Status != finding.WARN || f.Target != "compress" {
		t.Fatalf("got %+v", f)
	}
	if f.Value == nil || *f.Value != 2 {
		t.Fatalf("the number of distinct versions belongs in Value: %+v", f)
	}
	if !strings.Contains(f.Message, "26.4.11(r)") || !strings.Contains(f.Message, "26.4.16(r)") {
		t.Fatalf("both versions have to be named: %q", f.Message)
	}
	if fs := byCheck(t, rep, "cluster/versions"); len(fs) != 0 {
		t.Fatalf("the servers agree — only the provider differs: %+v", fs)
	}
}

// --- time zones -------------------------------------------------------------

func TestNodesInDifferentTimeZonesAreFound(t *testing.T) {
	snaps := threeHealthy()
	snaps[0].Vars["time_zone"] = "Europe/Rome"
	snaps[1].Vars["time_zone"] = "UTC"
	snaps[2].Vars["time_zone"] = "UTC"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "node/timezone")
	if f.Status != finding.WARN || f.Target != "compress" {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Message, "Europe/Rome") {
		t.Fatalf("the odd node's value has to be named: %q", f.Message)
	}
}

func TestAgreeingOnSYSTEMIsNotAgreeingOnATimeZone(t *testing.T) {
	// The fixture runs time_zone=SYSTEM everywhere, which is uniform. What it
	// resolves to is not, and a NOW() written on one node and applied on
	// another is then an hour out with every setting reported as matching.
	snaps := threeHealthy()
	snaps[1].Vars["system_time_zone"] = "CEST"
	rep := Run(snaps, nil, opts())
	f := one(t, rep, "node/timezone")
	if f.Status != finding.WARN {
		t.Fatalf("status = %s: %+v", f.Status, f)
	}
	if !strings.Contains(f.Message, "SYSTEM") || !strings.Contains(f.Message, "CEST") {
		t.Fatalf("the message has to say the setting matched and the value did not: %q", f.Message)
	}
}

// --- certification failures, which are a counter -----------------------------

func TestCertificationFailuresAreGradedOverTheInterval(t *testing.T) {
	// 60 failures in 1000 new writesets is 6%: the same rows are being written
	// on more than one node. The point of the test is the *rate* — the same
	// lifetime totals with no baseline must not produce this finding.
	snaps := threeHealthy()
	prev := baseline(snaps, 10*time.Minute)
	for i := range snaps {
		snaps[i].Status["wsrep_replicated"] = "2000"
	}
	snaps[1].Status["wsrep_local_cert_failures"] = "60"

	rep := Run(snaps, prev, opts())
	fs := byCheck(t, rep, "repl/cert-failures")
	var bad *finding.Finding
	for i := range fs {
		if fs[i].Status != finding.OK {
			bad = &fs[i]
		}
	}
	if bad == nil {
		t.Fatalf("6%% of writesets failing certification is a finding: %+v", fs)
	}
	if bad.Status != finding.BAD {
		t.Fatalf("status = %s, want BAD: %+v", bad.Status, *bad)
	}
	if bad.Target != "cl-02" {
		t.Fatalf("target = %q, want the node doing the conflicting writes", bad.Target)
	}
	if bad.Value == nil || *bad.Value < 0.05 {
		t.Fatalf("Value carries the ratio: %+v", *bad)
	}
	if !strings.Contains(bad.Message, "10m0s") {
		t.Fatalf("the message has to say over what interval: %q", bad.Message)
	}
}

func TestCertificationFailuresAreNotGradedWithoutABaseline(t *testing.T) {
	// The lifetime total says nothing about now, and grading it would go red
	// on the first run against a cluster that has been up for a year.
	snaps := threeHealthy()
	snaps[1].Status["wsrep_local_cert_failures"] = "60000"
	rep := Run(snaps, nil, opts())
	for _, f := range byCheck(t, rep, "repl/cert-failures") {
		if f.Status != finding.OK {
			t.Fatalf("a lifetime total must not be graded: %+v", f)
		}
	}
}

func TestAFewCertificationFailuresAreAWarningNotAFailure(t *testing.T) {
	snaps := threeHealthy()
	prev := baseline(snaps, 10*time.Minute)
	for i := range snaps {
		snaps[i].Status["wsrep_replicated"] = "2000"
	}
	snaps[0].Status["wsrep_local_cert_failures"] = "10" // 1% of 1000 new writesets
	rep := Run(snaps, prev, opts())
	var warn *finding.Finding
	fs := byCheck(t, rep, "repl/cert-failures")
	for i := range fs {
		if fs[i].Target == "sg-01" {
			warn = &fs[i]
		}
	}
	if warn == nil || warn.Status != finding.WARN {
		t.Fatalf("1%% is a warning, not a failure: %+v", warn)
	}
}

// --- the local state comment, which is a severity mapping ---------------------

func TestEveryLocalStateCommentMapsToTheRightSeverity(t *testing.T) {
	// node/state is emitted on every run for every node, so this switch is the
	// most-read line of the report. Joined and Joiner are transient and
	// expected; Initialized is a node that never finished its state transfer;
	// anything the provider has not been seen to say is BAD rather than
	// ignored, because an unrecognised state is not a healthy one.
	for _, tc := range []struct {
		comment string
		want    finding.Status
	}{
		{"Synced", finding.OK},
		{"synced", finding.OK},
		{" Synced ", finding.OK},
		{"Donor/Desynced", finding.WARN},
		{"Donor", finding.WARN},
		{"Joined", finding.WARN},
		{"Joiner", finding.WARN},
		{"Initialized", finding.BAD},
		{"Initializing", finding.BAD},
		{"Inconsistent", finding.BAD},
		{"", finding.BAD},
	} {
		snaps := threeHealthy()
		snaps[1].Status["wsrep_local_state_comment"] = tc.comment
		var got *finding.Finding
		fs := byCheck(t, Run(snaps, nil, opts()), "node/state")
		for i := range fs {
			if fs[i].Target == "cl-02" {
				got = &fs[i]
			}
		}
		if got == nil {
			t.Errorf("%q: no node/state finding for the node reporting it", tc.comment)
			continue
		}
		if got.Status != tc.want {
			t.Errorf("local state %q graded %s, want %s", tc.comment, got.Status, tc.want)
		}
		if tc.want != finding.OK && got.Hint == "" {
			t.Errorf("local state %q says nothing about what to do", tc.comment)
		}
	}
}
