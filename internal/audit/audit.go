// Package audit turns a set of node snapshots into findings.
//
// Two rules shape every check in here.
//
// A cluster statement is only as good as the nodes it was made from. A node
// that could not be read is an ERROR, not a skip, because "the cluster has 3
// members" said while one of five nodes was unreachable is a sentence with a
// hole in it.
//
// A total is not a rate. The wsrep counters only go up and reset on restart, so
// they are graded against the previous run or not graded at all — a threshold
// over a lifetime total goes red once and stays red, and a check that stays red
// is a check people stop reading.
package audit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Allan-Nava/galera-doctor/internal/cluster"
	"github.com/Allan-Nava/galera-doctor/internal/finding"
	"github.com/Allan-Nava/galera-doctor/internal/state"
)

// Options are the thresholds an audit grades against.
type Options struct {
	Cluster string
	// ExpectNodes is the membership the cluster should report. 0 means "as many
	// nodes as were configured", which is the right default and wrong exactly
	// when someone probes a subset on purpose.
	ExpectNodes int
	// FlowWarn and FlowBad are the share of the interval spent flow-controlled.
	FlowWarn float64
	FlowBad  float64
	// RecvQueueWarn is the instantaneous received-queue depth above which a node
	// is falling behind.
	RecvQueueWarn float64
	// ISTWarn is the shortest gcache window worth having: below it, a node that
	// restarts is likely to need a full SST instead of an incremental transfer.
	ISTWarn time.Duration
	// LatencyFloor is the replication latency below which a difference between
	// nodes in one segment is noise: a 4x ratio between 90µs and 350µs is not
	// a finding, and grading it is how a check gets switched off.
	LatencyFloor time.Duration
	// ClockWarn and ClockBad are the spread between the nodes' own clocks. The
	// nodes are compared with each other, not with the auditing host, so a
	// threshold here is about the cluster rather than about this machine's NTP.
	ClockWarn time.Duration
	ClockBad  time.Duration
	Now       time.Time
}

// DefaultOptions are the thresholds used when a caller passes none.
func DefaultOptions() Options {
	return Options{
		FlowWarn:      0.01,
		FlowBad:       0.10,
		RecvQueueWarn: 10,
		ISTWarn:       30 * time.Minute,
		LatencyFloor:  2 * time.Millisecond,
		ClockWarn:     2 * time.Second,
		ClockBad:      30 * time.Second,
		Now:           time.Now(),
	}
}

// Report is one audit.
type Report struct {
	Cluster  string            `json:"cluster"`
	At       time.Time         `json:"at"`
	Nodes    []string          `json:"nodes"`
	Findings []finding.Finding `json:"findings"`
	// State is what to persist for the next run's rate calculations.
	State state.State `json:"-"`
}

// Worst is the highest severity in the report.
func (r Report) Worst() finding.Status { return finding.Worst(r.Findings) }

// Run audits a set of snapshots. prev may be nil: the counter checks then say
// they have no baseline instead of inventing one.
func Run(snaps []cluster.Snapshot, prev *state.State, opt Options) Report {
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	rep := Report{
		Cluster: opt.Cluster,
		At:      opt.Now,
		Nodes:   cluster.Names(snaps),
		State:   state.New(snaps, opt.Now),
	}
	if rep.Cluster == "" {
		rep.Cluster = "cluster"
	}

	add := func(fs ...finding.Finding) { rep.Findings = append(rep.Findings, fs...) }

	// Every return path ends the same way: the transitions since the previous
	// run, then this run's verdicts carried forward for the next one. A
	// cluster that could not be read at all is a run whose findings still have
	// to be comparable — otherwise "the node came back" is a transition nobody
	// reports.
	finish := func() Report {
		add(changes(rep.Cluster, rep.Findings, prev, opt.Now)...)
		rep.State.Findings = carry(rep.Findings)
		finding.SortWorstFirst(rep.Findings)
		return rep
	}

	add(nodesRead(snaps)...)
	live := readable(snaps)
	if len(live) == 0 {
		add(finding.Finding{
			Check: "cluster/membership", Target: rep.Cluster, Status: finding.ERROR,
			Message: "no node could be read",
			Hint:    "nothing below this line was audited — fix access first",
		})
		return finish()
	}

	// A server without the wsrep provider is not a cluster member having a bad
	// day: it is a standalone server that somebody pointed this tool at. Every
	// cluster check would fire at once (size 0, status Disconnected, not ready,
	// wsrep off) and describe a catastrophe that is not happening — so it gets
	// one finding, and it is excluded from every comparison below.
	var foreign []cluster.Snapshot
	live, foreign = splitGalera(live)
	add(notGalera(foreign)...)
	if len(live) == 0 {
		add(finding.Finding{
			Check: "cluster/membership", Target: rep.Cluster, Status: finding.ERROR,
			Message: "no node in this list is running Galera",
			Hint:    "nothing was audited as a cluster — check the DSNs, or use a tool for standalone servers",
		})
		return finish()
	}

	add(clusterIdentity(rep.Cluster, live)...)
	add(membership(rep.Cluster, live, len(snaps), opt)...)
	add(nodeState(live)...)
	add(flowControl(live, prev, rep.State, opt)...)
	add(certification(live, prev, rep.State)...)
	add(queues(live, opt)...)
	add(sysTableDrift(rep.Cluster, live)...)
	add(schemaDrift(rep.Cluster, live)...)
	add(sstReadiness(rep.Cluster, live)...)
	add(quorumSettings(rep.Cluster, live)...)
	add(syncWait(rep.Cluster, live)...)
	add(autoIncrement(rep.Cluster, live)...)
	add(gcacheRecover(live)...)
	add(osuMethod(live)...)
	add(clockSkew(rep.Cluster, live, opt)...)
	add(writeSetLimits(rep.Cluster, live)...)
	add(durability(rep.Cluster, live)...)
	add(segments(rep.Cluster, live)...)
	add(peerList(live)...)
	add(flowSettings(rep.Cluster, live)...)
	add(appliers(rep.Cluster, live)...)
	add(sstSize(rep.Cluster, live)...)
	add(latency(rep.Cluster, live, opt)...)
	add(serverIdentity(rep.Cluster, live)...)
	add(applierTriggers(rep.Cluster, live)...)
	add(asyncReplication(live)...)
	add(binlog(rep.Cluster, live)...)
	add(writers(rep.Cluster, live, prev, rep.State)...)
	add(restarted(live, prev, rep.State)...)
	add(membershipView(rep.Cluster, live)...)
	add(coverage(rep.Cluster, snaps, live, prev)...)
	add(storageEngines(live)...)
	add(primaryKeys(live)...)
	add(versions(rep.Cluster, live)...)
	add(gcache(live, prev, rep.State, opt)...)

	return finish()
}

// splitGalera separates cluster members from standalone servers.
//
// wsrep_provider is the reliable signal: it is a path to the library and reads
// "none" on a server with replication compiled in but not configured.
// wsrep_on, by contrast, is OFF on a real cluster member that somebody has
// temporarily taken out of replication — a serious finding that must not be
// mistaken for "this is not a cluster".
func splitGalera(live []cluster.Snapshot) (galera, foreign []cluster.Snapshot) {
	for _, s := range live {
		provider, ok := s.Var("wsrep_provider")
		switch {
		case ok && provider != "" && !strings.EqualFold(provider, "none"):
			galera = append(galera, s)
		case ok:
			foreign = append(foreign, s)
		default:
			// No such variable at all: a MySQL build without wsrep.
			foreign = append(foreign, s)
		}
	}
	return galera, foreign
}

// notGalera reports a standalone server, once.
func notGalera(foreign []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, s := range foreign {
		version, _ := s.Var("version")
		msg := "not a Galera node: wsrep_provider is not configured"
		if version != "" {
			msg += " (server " + version + ")"
		}
		out = append(out, finding.Finding{
			Check: "node/not-galera", Target: s.Node, Status: finding.ERROR,
			Message: msg,
			Hint:    "excluded from every cluster comparison — grading it would report an outage that is not happening",
		})
	}
	return out
}

func readable(snaps []cluster.Snapshot) []cluster.Snapshot {
	var out []cluster.Snapshot
	for _, s := range snaps {
		if s.OK() {
			out = append(out, s)
		}
	}
	return out
}

// nodesRead reports the nodes that were not read. It comes first because every
// other cluster-wide statement is conditional on it.
func nodesRead(snaps []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, s := range snaps {
		if s.OK() {
			continue
		}
		out = append(out, finding.Finding{
			Check: "node/read", Target: s.Node, Status: finding.ERROR,
			Message: "not read: " + s.Err,
			Hint:    "every cluster-wide finding below was made without this node",
		})
	}
	return out
}

// clusterIdentity is the split-brain check: two nodes that report different
// cluster state UUIDs are two clusters wearing the same name.
func clusterIdentity(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding

	uuids := groupBy(live, func(s cluster.Snapshot) string {
		v, _ := s.Get("wsrep_cluster_state_uuid")
		return v
	})
	if len(uuids) > 1 {
		out = append(out, finding.Finding{
			Check: "cluster/uuid", Target: name, Status: finding.BAD,
			Message: "nodes report different cluster state UUIDs: " + describeGroups(uuids),
			Value:   finding.Num(float64(len(uuids))), Unit: "clusters",
			Hint: "this is a partition, not a lag: the groups have diverged and one side has to be reinitialised from the other",
		})
	}

	confs := groupBy(live, func(s cluster.Snapshot) string {
		v, _ := s.Get("wsrep_cluster_conf_id")
		return v
	})
	if len(uuids) == 1 && len(confs) > 1 {
		out = append(out, finding.Finding{
			Check: "cluster/conf-id", Target: name, Status: finding.WARN,
			Message: "nodes report different membership configuration ids: " + describeGroups(confs),
			Hint:    "usually a membership change caught mid-flight — re-run; if it persists, a node is not receiving membership updates",
		})
	}

	for _, s := range live {
		status, ok := s.Get("wsrep_cluster_status")
		if !ok {
			continue
		}
		if !strings.EqualFold(status, "Primary") {
			out = append(out, finding.Finding{
				Check: "cluster/primary", Target: s.Node, Status: finding.BAD,
				Message: "cluster status is " + status + ", not Primary",
				Hint:    "a non-Primary node refuses writes and reads stale data: it has lost quorum or was partitioned off",
			})
		}
	}
	return out
}

// membership compares what each node believes about the size of the cluster
// with what was asked of it.
func membership(name string, live []cluster.Snapshot, configured int, opt Options) []finding.Finding {
	var out []finding.Finding
	expect := opt.ExpectNodes
	if expect == 0 {
		expect = configured
	}

	sizes := groupBy(live, func(s cluster.Snapshot) string {
		v, _ := s.Get("wsrep_cluster_size")
		return v
	})
	if len(sizes) > 1 {
		out = append(out, finding.Finding{
			Check: "cluster/size", Target: name, Status: finding.BAD,
			Message: "nodes disagree about the cluster size: " + describeGroups(sizes),
			Hint:    "the nodes are not seeing the same membership — check connectivity between the groups above",
		})
		return out
	}
	for _, s := range live {
		size, ok := s.Float("wsrep_cluster_size")
		if !ok {
			continue
		}
		st := finding.OK
		hint := ""
		if int(size) != expect {
			st = finding.BAD
			hint = fmt.Sprintf("expected %d members; a member that left is not replicating and, below quorum, the rest stop accepting writes", expect)
		}
		out = append(out, finding.Finding{
			Check: "cluster/size", Target: name, Status: st,
			Message: fmt.Sprintf("%d member(s), expected %d", int(size), expect),
			Value:   finding.Num(size), Unit: "nodes", Hint: hint,
		})
		break // every node agreed; one statement is enough
	}
	return out
}

// nodeState grades what each node says about itself.
func nodeState(live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		if ready, ok := s.Bool("wsrep_ready"); ok && !ready {
			out = append(out, finding.Finding{
				Check: "node/ready", Target: s.Node, Status: finding.BAD,
				Message: "wsrep_ready is OFF",
				Hint:    "the node is refusing queries that touch replicated tables",
			})
		}
		if conn, ok := s.Bool("wsrep_connected"); ok && !conn {
			out = append(out, finding.Finding{
				Check: "node/connected", Target: s.Node, Status: finding.BAD,
				Message: "wsrep_connected is OFF",
				Hint:    "the node is not part of the group communication at all",
			})
		}
		if on, ok := s.Bool("wsrep_on"); ok && !on {
			out = append(out, finding.Finding{
				Check: "node/wsrep-on", Target: s.Node, Status: finding.BAD,
				Message: "wsrep_on is OFF: writes on this node are not replicated",
				Hint:    "anything written here stays here, and will conflict when the setting is put back",
			})
		}
		if desync, ok := s.Bool("wsrep_desync"); ok && desync {
			out = append(out, finding.Finding{
				Check: "node/desync", Target: s.Node, Status: finding.WARN,
				Message: "wsrep_desync is ON",
				Hint:    "deliberate during a backup or a schema change; left on, the node drifts without flow-controlling the cluster",
			})
		}
		if comment, ok := s.Get("wsrep_local_state_comment"); ok {
			st, hint := stateComment(comment)
			out = append(out, finding.Finding{
				Check: "node/state", Target: s.Node, Status: st,
				Message: "local state is " + comment, Hint: hint,
			})
		}
		if ro, ok := s.Bool("read_only"); ok && ro {
			out = append(out, finding.Finding{
				Check: "node/read-only", Target: s.Node, Status: finding.WARN,
				Message: "read_only is ON",
				Hint:    "expected on a node kept out of the write path; unexpected, it is a failover that never finished",
			})
		}
	}
	return out
}

func stateComment(comment string) (finding.Status, string) {
	switch strings.ToLower(strings.TrimSpace(comment)) {
	case "synced":
		return finding.OK, ""
	case "donor/desynced", "donor":
		return finding.WARN, "the node is feeding a state transfer: expected while another node joins, a problem if it never ends"
	case "joined", "joiner":
		return finding.WARN, "the node is catching up and is not yet serving replicated reads consistently"
	case "initialized", "initializing":
		return finding.BAD, "the node is not part of the cluster: it has not completed its state transfer"
	default:
		return finding.BAD, "unexpected local state — the node is neither synced nor knowingly transferring"
	}
}

// flowControl grades the share of the interval the node spent flow-controlled,
// which is the honest form of a counter that only ever goes up.
func flowControl(live []cluster.Snapshot, prev *state.State, now state.State, opt Options) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		ns := now.Nodes[s.Node]
		d, ok := prev.Since(s.Node, "wsrep_flow_control_paused_ns", ns)
		if !ok {
			// No baseline: report the lifetime figure as context and refuse to
			// grade it. A total that covers an incident from March would
			// otherwise be red forever.
			if v, has := s.Float("wsrep_flow_control_paused"); has {
				out = append(out, finding.Finding{
					Check: "flow/paused", Target: s.Node, Status: finding.OK,
					Message: fmt.Sprintf("%.1f%% of the time since the last status reset (not graded: no baseline)", v*100),
					Value:   finding.Num(v), Unit: "fraction",
					Hint: "run again with --state to grade the interval between runs instead of the lifetime total",
				})
			}
			continue
		}
		frac := d.Fraction()
		st := finding.OK
		hint := ""
		switch {
		case frac >= opt.FlowBad:
			st, hint = finding.BAD, "the cluster is being held back by this node: writers everywhere are waiting on it"
		case frac >= opt.FlowWarn:
			st, hint = finding.WARN, "this node is intermittently the slowest in the cluster; look at its disk and its replication threads"
		}
		out = append(out, finding.Finding{
			Check: "flow/paused", Target: s.Node, Status: st,
			Message: fmt.Sprintf("flow-controlled %.2f%% of the last %s", frac*100, d.Elapsed.Round(time.Second)),
			Value:   finding.Num(frac), Unit: "fraction", Hint: hint,
		})
	}
	return out
}

// certification grades conflicts, again over the interval.
func certification(live []cluster.Snapshot, prev *state.State, now state.State) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		ns := now.Nodes[s.Node]
		fails, ok1 := prev.Since(s.Node, "wsrep_local_cert_failures", ns)
		repl, ok2 := prev.Since(s.Node, "wsrep_replicated", ns)
		if !ok1 || !ok2 {
			continue
		}
		if repl.Value == 0 {
			continue
		}
		ratio := fails.Value / repl.Value
		st := finding.OK
		hint := ""
		switch {
		case ratio >= 0.05:
			st, hint = finding.BAD, "transactions are being rolled back after committing locally: the same rows are being written on more than one node"
		case ratio >= 0.005:
			st, hint = finding.WARN, "some write conflicts across nodes — usually a workload that should be pinned to one writer"
		}
		out = append(out, finding.Finding{
			Check: "repl/cert-failures", Target: s.Node, Status: st,
			Message: fmt.Sprintf("%.0f certification failure(s) out of %.0f writesets in the last %s (%.3f%%)",
				fails.Value, repl.Value, fails.Elapsed.Round(time.Second), ratio*100),
			Value: finding.Num(ratio), Unit: "fraction", Hint: hint,
		})
	}
	return out
}

// queues reads the instantaneous depths, which are gauges and can be graded
// as they are.
func queues(live []cluster.Snapshot, opt Options) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		if q, ok := s.Float("wsrep_local_recv_queue"); ok && q >= opt.RecvQueueWarn {
			out = append(out, finding.Finding{
				Check: "queue/recv", Target: s.Node, Status: finding.WARN,
				Message: fmt.Sprintf("%.0f writesets waiting to be applied", q),
				Value:   finding.Num(q), Unit: "writesets",
				Hint: "this node is applying more slowly than the cluster writes; it is the next one to flow-control",
			})
		}
		if q, ok := s.Float("wsrep_local_send_queue"); ok && q >= opt.RecvQueueWarn {
			out = append(out, finding.Finding{
				Check: "queue/send", Target: s.Node, Status: finding.WARN,
				Message: fmt.Sprintf("%.0f writesets waiting to be sent", q),
				Value:   finding.Num(q), Unit: "writesets",
				Hint: "the node cannot push to the group fast enough — look at the network before the disk",
			})
		}
	}
	return out
}

// sysTableDrift is the check no wsrep counter can make.
//
// Galera replicates the application's DDL, not the maintenance that happens to
// the server's own tables. So two nodes can hold different definitions of
// mysql.column_stats — or of the tables mysql_upgrade touched on one node and
// not the others — for months, while every replication metric stays green. The
// symptom, when it finally arrives, is a query plan that differs per node or a
// flood of errors in one node's log, which nobody connects to a schema nobody
// looked at.
func sysTableDrift(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	var audited []cluster.Snapshot
	for _, s := range live {
		if len(s.SysTables) == 0 {
			out = append(out, finding.Finding{
				Check: "systables/drift", Target: s.Node, Status: finding.WARN,
				Message: "system table definitions were not read",
				Hint:    "the audit user needs SELECT on information_schema.COLUMNS; without it this node is excluded from the drift comparison",
			})
			continue
		}
		audited = append(audited, s)
	}
	if len(audited) < 2 {
		return out
	}

	tables := map[string]bool{}
	for _, s := range audited {
		for t := range s.SysTables {
			tables[t] = true
		}
	}
	names := make([]string, 0, len(tables))
	for t := range tables {
		names = append(names, t)
	}
	sort.Strings(names)

	var drifted []string
	for _, table := range names {
		groups := map[string][]string{}
		for _, s := range audited {
			fp, ok := s.SysTables[table]
			if !ok {
				fp = "absent"
			}
			groups[fp] = append(groups[fp], s.Node)
		}
		if len(groups) == 1 {
			continue
		}
		drifted = append(drifted, table)
		out = append(out, finding.Finding{
			Check: "systables/drift", Target: "mysql." + table, Status: finding.BAD,
			Message: "definition differs across nodes: " + describeGroups(groups),
			Hint:    "Galera does not replicate this: fix it per node (mysql_upgrade, or align the definition by hand) — no wsrep_* metric will ever show it",
		})
	}
	if len(drifted) == 0 {
		out = append(out, finding.Finding{
			Check: "systables/drift", Target: name, Status: finding.OK,
			Message: fmt.Sprintf("%d system table(s) identical across %d node(s)", len(names), len(audited)),
			Value:   finding.Num(float64(len(names))), Unit: "tables",
		})
	}
	return out
}

// maxDriftedListed bounds the per-table findings. A node that missed a whole
// schema drifts on every table in it, and four hundred findings bury the rest
// of the report — at which point the number is the finding.
const maxDriftedListed = 5

// schemaDrift compares the application schemas across nodes (GD-13).
//
// Galera *does* replicate application DDL, which is what makes this a
// different diagnosis from sysTableDrift rather than the same check pointed at
// another schema: a difference here means a change that failed, was applied on
// one node by hand, or landed while a node was desynced — and the fix is to
// re-apply it, not to run mysql_upgrade. Both are equally invisible to every
// wsrep_* counter, because replication is not broken. It already carried
// whatever it was given.
func schemaDrift(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	var audited []cluster.Snapshot
	for _, s := range live {
		// nil is "not read"; an empty map is "read, and there are none".
		if s.AppTables == nil {
			out = append(out, finding.Finding{
				Check: "schema/drift", Target: s.Node, Status: finding.WARN,
				Message: "application schema definitions were not read",
				Hint:    "the audit user needs SELECT on information_schema.COLUMNS; without it this node is excluded from the drift comparison",
			})
			continue
		}
		audited = append(audited, s)
	}
	if len(audited) < 2 {
		return out
	}

	tables := map[string]bool{}
	for _, s := range audited {
		for t := range s.AppTables {
			tables[t] = true
		}
	}
	names := make([]string, 0, len(tables))
	for t := range tables {
		names = append(names, t)
	}
	sort.Strings(names)

	type drift struct {
		table  string
		groups map[string][]string
	}
	var drifted []drift
	for _, table := range names {
		groups := map[string][]string{}
		for _, s := range audited {
			fp, ok := s.AppTables[table]
			if !ok {
				fp = "absent"
			}
			groups[fp] = append(groups[fp], s.Node)
		}
		if len(groups) > 1 {
			drifted = append(drifted, drift{table: table, groups: groups})
		}
	}

	const hint = "application DDL is replicated, so this is a change that did not finish: compare the definitions and re-apply it on the nodes that are behind (a failed ALTER, a change applied by hand, or one that landed while a node was desynced)"

	switch {
	case len(drifted) == 0:
		out = append(out, finding.Finding{
			Check: "schema/drift", Target: name, Status: finding.OK,
			Message: fmt.Sprintf("%d application table(s) identical across %d node(s)", len(names), len(audited)),
			Value:   finding.Num(float64(len(names))), Unit: "tables",
		})
	case len(drifted) <= maxDriftedListed:
		for _, d := range drifted {
			out = append(out, finding.Finding{
				Check: "schema/drift", Target: d.table, Status: finding.BAD,
				Message: "definition differs across nodes: " + describeGroups(d.groups),
				Hint:    hint,
			})
		}
	default:
		listed := make([]string, 0, maxDriftedListed)
		for _, d := range drifted[:maxDriftedListed] {
			listed = append(listed, d.table)
		}
		out = append(out, finding.Finding{
			Check: "schema/drift", Target: name, Status: finding.BAD,
			Message: fmt.Sprintf("%d application table(s) differ across nodes: %s and %d more",
				len(drifted), strings.Join(listed, ", "), len(drifted)-maxDriftedListed),
			Value: finding.Num(float64(len(drifted))), Unit: "tables",
			Hint: "this many tables at once is a node that missed a whole schema change, not one bad ALTER: " + hint,
		})
	}
	return out
}

// backupSSTMethods are the state transfer methods that log in to the donor.
// rsync and its variants copy files and authenticate at the filesystem level,
// so an empty wsrep_sst_auth says nothing about them.
var backupSSTMethods = map[string]bool{
	"mariabackup":   true,
	"xtrabackup":    true,
	"xtrabackup-v2": true,
	"mysqldump":     true,
}

// sstReadiness reports what the next rejoin will cost (GD-25).
//
// Nothing here is wrong with the cluster today. A node whose SST method its
// peers cannot serve, or whose donor list names a server that was
// decommissioned in March, is Synced and green right up to the moment it
// restarts — at which point it either cannot rejoin or takes an unexpected
// donor out of service. No wsrep_* counter has an opinion about a setting that
// has not been exercised yet.
func sstReadiness(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding

	// The method, compared across the nodes that report it. A build that does
	// not report it is not a build that agrees with everybody else.
	var reporting []cluster.Snapshot
	for _, s := range live {
		if v, ok := s.Var("wsrep_sst_method"); ok && strings.TrimSpace(v) != "" {
			reporting = append(reporting, s)
		}
	}
	if len(reporting) > 0 {
		groups := groupBy(reporting, func(s cluster.Snapshot) string {
			v, _ := s.Var("wsrep_sst_method")
			return strings.ToLower(strings.TrimSpace(v))
		})
		if len(groups) > 1 {
			out = append(out, finding.Finding{
				Check: "sst/method", Target: name, Status: finding.WARN,
				Message: "nodes disagree about the state transfer method: " + describeGroups(groups),
				Hint:    "the joiner asks and the donor serves: a donor without the joiner's method installed cannot answer, so the node that restarts is the one that finds out",
			})
		} else {
			method, _ := reporting[0].Var("wsrep_sst_method")
			out = append(out, finding.Finding{
				Check: "sst/method", Target: name, Status: finding.OK,
				Message: fmt.Sprintf("all %d node(s) use %s for state transfer",
					len(reporting), strings.ToLower(strings.TrimSpace(method))),
			})
		}
	}

	// Every spelling this cluster answers to, so a donor named by address is
	// not reported as missing.
	known := map[string]bool{}
	for _, s := range live {
		for _, a := range s.Addresses() {
			known[strings.ToLower(a)] = true
		}
	}

	for _, s := range live {
		list, ok := s.Var("wsrep_sst_donor")
		if !ok || strings.TrimSpace(list) == "" {
			continue
		}
		// A trailing empty element — "node," — is Galera's way of saying "and
		// otherwise anybody". Without it the list is the only answer allowed.
		parts := strings.Split(list, ",")
		fallback := strings.TrimSpace(parts[len(parts)-1]) == ""
		var unknown []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" || known[strings.ToLower(p)] {
				continue
			}
			unknown = append(unknown, p)
		}
		if len(unknown) == 0 {
			continue
		}
		status, hint := finding.BAD, "with no trailing comma this list is the only donor allowed: the node will refuse to start when that donor is unavailable — fix the name, or end the list with a comma to allow any donor"
		if fallback {
			status, hint = finding.WARN, "the trailing comma means it will fall back to any available donor, so the node still starts — but the list no longer says what somebody meant it to say"
		}
		out = append(out, finding.Finding{
			Check: "sst/donor", Target: s.Node, Status: status,
			Message: fmt.Sprintf("SST donor list names %s, which is not in this cluster (wsrep_sst_donor=%s)",
				strings.Join(unknown, ", "), list),
			Hint: hint,
		})
	}

	// A backup-based transfer logs in to the donor. An empty wsrep_sst_auth is
	// not proof the credentials are missing — they can live in the [sst]
	// section of the config, where the server never sees them — so the finding
	// says that rather than pretending to be sure.
	for _, s := range live {
		method, ok := s.Var("wsrep_sst_method")
		if !ok || !backupSSTMethods[strings.ToLower(strings.TrimSpace(method))] {
			continue
		}
		auth, ok := s.Var("wsrep_sst_auth")
		if !ok || strings.TrimSpace(auth) != "" {
			continue
		}
		out = append(out, finding.Finding{
			Check: "sst/auth", Target: s.Node, Status: finding.WARN,
			Message: fmt.Sprintf("wsrep_sst_auth is empty and the method is %s, which authenticates against the donor",
				strings.ToLower(strings.TrimSpace(method))),
			Hint: "credentials may be in the [sst] section of the config instead, which the server cannot see — confirm it there, because the alternative is an SST that fails at 03:00",
		})
	}
	return out
}

// quorumSettings reports a split brain that is already configured (GD-26).
//
// The cluster is Primary, every counter is green, and pc.* has already decided
// what happens at the next partition: a node left with pc.ignore_sb keeps
// taking writes on the wrong side of it, and the weights decide which side
// that is. None of this is exercised until the network moves, which is why no
// metric reports it.
func quorumSettings(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding

	for _, s := range live {
		if v, ok := s.ProviderOption("pc.ignore_sb"); ok && truthy(v) {
			out = append(out, finding.Finding{
				Check: "quorum/ignore-sb", Target: s.Node, Status: finding.BAD,
				Message: "pc.ignore_sb is on: this node keeps accepting writes in a non-Primary component",
				Hint:    "at the next partition both sides stay writable and diverge — this is normally left on after somebody recovered a cluster by hand, and it has to be turned off again",
			})
		}
		if v, ok := s.ProviderOption("pc.bootstrap"); ok && truthy(v) {
			out = append(out, finding.Finding{
				Check: "quorum/bootstrap", Target: s.Node, Status: finding.WARN,
				Message: "pc.bootstrap is still set on this node",
				Hint:    "the bootstrap trigger is a one-shot: left in the configuration it makes this node form its own Primary component the next time it starts alone",
			})
		}
	}

	// The weights, as arithmetic rather than as an opinion: unequal weights are
	// legal and sometimes deliberate, and the useful statement is what the sum
	// makes possible.
	weights := map[string]float64{}
	total := 0.0
	for _, s := range live {
		v, ok := s.ProviderOption("pc.weight")
		if !ok {
			continue
		}
		w, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			continue
		}
		weights[s.Node] = w
		total += w
		if w == 0 {
			out = append(out, finding.Finding{
				Check: "quorum/weight", Target: s.Node, Status: finding.BAD,
				Message: "pc.weight is 0: this node never counts towards quorum",
				Value:   finding.Num(0), Unit: "weight",
				Hint: "the cluster has one fewer vote than it has nodes — losing one of the others is losing quorum, and the node count says nothing about it",
			})
		}
	}
	if len(weights) > 1 {
		equal := true
		var first float64
		i := 0
		for _, w := range weights {
			if i == 0 {
				first = w
			} else if w != first {
				equal = false
			}
			i++
		}
		switch {
		case equal && first > 0:
			out = append(out, finding.Finding{
				Check: "quorum/weight", Target: name, Status: finding.OK,
				Message: fmt.Sprintf("all %d node(s) carry equal quorum weight (%s)", len(weights), trimFloat(first)),
				Value:   finding.Num(total), Unit: "weight",
			})
		case !equal:
			nodes := make([]string, 0, len(weights))
			for n := range weights {
				nodes = append(nodes, n)
			}
			sort.Strings(nodes)
			parts := make([]string, 0, len(nodes))
			var majority []string
			for _, n := range nodes {
				parts = append(parts, fmt.Sprintf("%s=%s", n, trimFloat(weights[n])))
				if weights[n]*2 > total {
					majority = append(majority, n)
				}
			}
			msg := fmt.Sprintf("quorum weights are not equal: %s of %s total",
				strings.Join(parts, ", "), trimFloat(total))
			hint := "the node count is not the vote count: check that this is the arithmetic somebody meant, because it decides which side of a partition survives"
			if len(majority) > 0 {
				msg += fmt.Sprintf(" — %s alone holds a majority", strings.Join(majority, ", "))
				hint = "a single node holding a majority is a cluster that survives losing everything else, and dies when it loses that one: deliberate for a two-node-plus-arbitrator setup, an accident otherwise"
			}
			out = append(out, finding.Finding{
				Check: "quorum/weight", Target: name, Status: finding.WARN,
				Message: msg, Value: finding.Num(total), Unit: "weight",
				Hint: hint,
			})
		}
	}
	return out
}

// syncWait reports nodes that disagree about causal reads (GD-27).
//
// With wsrep_sync_wait on, a read waits for the writes that were committed
// before it; with it off, the same query can return a row that is not there
// yet. When the nodes disagree, the answer depends on which node the proxy
// picked — and every node is behaving exactly as configured, so nothing
// reports a problem. What the cluster wants is not this tool's business; the
// nodes not agreeing is.
func syncWait(name string, live []cluster.Snapshot) []finding.Finding {
	var reporting []cluster.Snapshot
	for _, s := range live {
		if v, ok := s.Var("wsrep_sync_wait"); ok && strings.TrimSpace(v) != "" {
			reporting = append(reporting, s)
		}
	}
	if len(reporting) < 2 {
		return nil
	}
	groups := groupBy(reporting, func(s cluster.Snapshot) string {
		v, _ := s.Var("wsrep_sync_wait")
		return strings.TrimSpace(v)
	})
	if len(groups) == 1 {
		return nil
	}
	return []finding.Finding{{
		Check: "repl/sync-wait", Target: name, Status: finding.WARN,
		Message: "nodes disagree about wsrep_sync_wait: " + describeGroups(groups),
		Hint:    "a read is causal or not depending on which node the proxy picked, which reaches the application as \"sometimes the row is not there yet\" and reaches no dashboard at all",
	}}
}

// autoIncrement reports ids that will collide (GD-28).
//
// Galera keeps auto_increment_increment and auto_increment_offset in step with
// the membership itself — unless wsrep_auto_increment_control was turned off,
// at which point the values are whatever somebody typed. Two nodes sharing an
// offset generate the same ids as soon as both take writes, and the damage
// lands in application data rather than in a replication counter.
func autoIncrement(name string, live []cluster.Snapshot) []finding.Finding {
	var uncontrolled []cluster.Snapshot
	controlled := 0
	for _, s := range live {
		on, ok := s.Bool("wsrep_auto_increment_control")
		if !ok {
			continue
		}
		if on {
			controlled++
			continue
		}
		uncontrolled = append(uncontrolled, s)
	}
	if len(uncontrolled) == 0 {
		if controlled == 0 {
			return nil
		}
		return []finding.Finding{{
			Check: "repl/auto-increment", Target: name, Status: finding.OK,
			Message: fmt.Sprintf("Galera manages the auto-increment step and offset on all %d node(s)", controlled),
		}}
	}

	offsets := map[string][]string{}
	step := map[string]bool{}
	var smallest float64
	first := true
	for _, s := range uncontrolled {
		off, ok := s.Float("auto_increment_offset")
		if !ok {
			if v, k := s.Var("auto_increment_offset"); k {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					off, ok = f, true
				}
			}
		}
		if ok {
			offsets[trimFloat(off)] = append(offsets[trimFloat(off)], s.Node)
		}
		if v, k := s.Var("auto_increment_increment"); k {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				step[trimFloat(f)] = true
				if first || f < smallest {
					smallest, first = f, false
				}
			}
		}
	}

	var shared []string
	for off, nodes := range offsets {
		if len(nodes) > 1 {
			sort.Strings(nodes)
			shared = append(shared, fmt.Sprintf("%s (%s)", off, strings.Join(nodes, ", ")))
		}
	}
	sort.Strings(shared)

	const hint = "wsrep_auto_increment_control is ON by default and keeps the step and the offsets in line with the membership; with it OFF the values are whatever was typed, and the ids collide the moment a second node takes a write"
	if len(shared) > 0 {
		return []finding.Finding{{
			Check: "repl/auto-increment", Target: name, Status: finding.BAD,
			Message: fmt.Sprintf("wsrep_auto_increment_control is off and nodes share an auto_increment_offset: %s",
				strings.Join(shared, "; ")),
			Hint: hint,
		}}
	}
	if !first && smallest < float64(len(live)) {
		return []finding.Finding{{
			Check: "repl/auto-increment", Target: name, Status: finding.WARN,
			Message: fmt.Sprintf("wsrep_auto_increment_control is off and the step is %s for %d node(s): the offsets run out",
				trimFloat(smallest), len(live)),
			Value: finding.Num(smallest), Unit: "step",
			Hint: hint,
		}}
	}
	return []finding.Finding{{
		Check: "repl/auto-increment", Target: name, Status: finding.OK,
		Message: fmt.Sprintf("auto-increment is set by hand on %d node(s), with distinct offsets and a step of %s",
			len(uncontrolled), trimFloat(smallest)),
	}}
}

// truthy reads the spellings Galera uses inside wsrep_provider_options, which
// are not the ON/OFF of a server variable.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

// trimFloat prints a weight or an offset the way somebody typed it: 3, not
// 3.000000.
func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// storageEngines reports application tables Galera does not replicate (GD-29).
//
// Galera replicates InnoDB. A write to a MyISAM or Aria table succeeds, is
// certified by nothing, travels nowhere, and leaves the row on exactly one
// node — with every replication counter green, because from replication's
// point of view nothing happened. MariaDB can be told to replicate MyISAM and
// Aria (wsrep_mode, or the older wsrep_replicate_myisam), which is
// experimental and, more to the point here, per node: nodes disagreeing about
// it is worse than none of them doing it, because then the same write lands on
// some of them.
func storageEngines(live []cluster.Snapshot) []finding.Finding {
	seen := map[string]bool{}
	for _, s := range live {
		for _, t := range s.TablesNonInnoDB {
			seen[t] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	tables := make([]string, 0, len(seen))
	for t := range seen {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	shown := tables
	suffix := ""
	if len(shown) > 5 {
		shown, suffix = shown[:5], fmt.Sprintf(" (+%d more)", len(tables)-5)
	}

	// How each node answers "do you replicate these at all".
	groups := groupBy(live, func(s cluster.Snapshot) string {
		mode, hasMode := s.Var("wsrep_mode")
		if hasMode && (containsFold(mode, "REPLICATE_MYISAM") || containsFold(mode, "REPLICATE_ARIA")) {
			return "replicated"
		}
		if on, ok := s.Bool("wsrep_replicate_myisam"); ok && on {
			return "replicated"
		}
		if !hasMode {
			if _, ok := s.Var("wsrep_replicate_myisam"); !ok {
				return "unknown"
			}
		}
		return "not replicated"
	})
	delete(groups, "unknown")

	msg := fmt.Sprintf("%d application table(s) on an engine Galera does not replicate: %s%s",
		len(tables), strings.Join(shown, ", "), suffix)
	if len(groups) > 1 {
		return []finding.Finding{{
			Check: "schema/engine", Target: "schema", Status: finding.BAD,
			Message: msg + " — and the nodes disagree about replicating them: " + describeGroups(groups),
			Value:   finding.Num(float64(len(tables))), Unit: "tables",
			Hint: "the same write lands on some nodes and not others, which is divergence arriving one statement at a time: align wsrep_mode across the nodes, then move these tables to InnoDB",
		}}
	}
	hint := "these writes are not replicated: they succeed, no counter records them, and the rows exist on the node that took them — move the tables to InnoDB, or accept that they are per-node data"
	if _, replicated := groups["replicated"]; replicated {
		hint = "MyISAM and Aria replication is enabled here, which is experimental and certifies nothing: a conflicting write is not detected, it is applied — move the tables to InnoDB"
	}
	return []finding.Finding{{
		Check: "schema/engine", Target: "schema", Status: finding.WARN,
		Message: msg,
		Value:   finding.Num(float64(len(tables))), Unit: "tables",
		Hint: hint,
	}}
}

// containsFold is strings.Contains, case-insensitively.
func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToUpper(haystack), strings.ToUpper(needle))
}

// gcacheRecover reports a restart that throws the write-set cache away (GD-33).
//
// gcache/window measures how much time the cache buys before a restarting node
// needs a full state transfer. With gcache.recover off, a clean restart
// discards the cache along with the process: the window the other check
// reports is a buffer this setting quietly throws away, and the node comes back
// needing an SST — which takes a donor out of service with it. Each node's
// restart is its own, so this is one finding per node rather than one about the
// cluster.
func gcacheRecover(live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		v, ok := s.ProviderOption("gcache.recover")
		// The option arrived in Galera 3.19: a provider that does not report
		// it is not a provider with it off.
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		if truthy(v) {
			continue
		}
		out = append(out, finding.Finding{
			Check: "gcache/recover", Target: s.Node, Status: finding.WARN,
			Message: fmt.Sprintf("gcache.recover is %s: a clean restart discards this node's write-set cache",
				strings.TrimSpace(v)),
			Hint: "with it on the node rejoins by IST from its own cache after a restart; with it off even a two-minute maintenance window costs a full SST, and an SST takes a donor out of service too",
		})
	}
	return out
}

// osuMethod reports the DDL method that explains schema/drift (GD-34).
//
// TOI replicates a schema change to every node; NBO does the same without
// holding the cluster-wide lock. RSU does not replicate it at all — it applies
// the change on the node it was run on and leaves the others alone, which is
// precisely how the application schema drift that schema/drift reports comes to
// exist. Reporting the cause next to the symptom is the difference between a
// finding and a diagnosis.
func osuMethod(live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		v, ok := s.Var("wsrep_osu_method")
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(v), "RSU") {
			continue
		}
		out = append(out, finding.Finding{
			Check: "repl/osu-method", Target: s.Node, Status: finding.WARN,
			Message: "wsrep_OSU_method is RSU: DDL run on this node is applied here and not replicated",
			Hint:    "this is where a schema/drift finding comes from — RSU is a per-node operation to be turned on for one change and off again, not a default; TOI and NBO both replicate",
		})
	}
	return out
}

// clockSkew reports nodes whose clocks disagree (GD-16).
//
// Certification does not care: Galera orders writes by sequence number, not by
// time. Everything a human does during an incident cares a great deal —
// reading two error logs side by side, correlating a flow-control spike with a
// backup, believing a timestamp in a monitoring system. The nodes are compared
// with each other rather than with the auditing host, because this host's clock
// is not a reference either.
func clockSkew(name string, live []cluster.Snapshot, opt Options) []finding.Finding {
	var read []cluster.Snapshot
	for _, s := range live {
		if !s.Clock.IsZero() {
			read = append(read, s)
		}
	}
	if len(read) < 2 {
		return nil
	}

	// Each node's clock, corrected for when its snapshot was taken: the
	// snapshots are concurrent but not simultaneous.
	offset := make(map[string]time.Duration, len(read))
	var lo, hi time.Duration
	first := true
	for _, s := range read {
		at := s.At
		if at.IsZero() {
			at = opt.Now
		}
		d := s.Clock.Sub(at)
		offset[s.Node] = d
		if first || d < lo {
			lo = d
		}
		if first || d > hi {
			hi = d
		}
		first = false
	}
	spread := hi - lo

	// Name the nodes at the edges of the spread rather than every node's
	// offset: a report is read at 03:00.
	var slowest, fastest string
	for _, s := range read {
		if offset[s.Node] == lo && slowest == "" {
			slowest = s.Node
		}
		if offset[s.Node] == hi && fastest == "" {
			fastest = s.Node
		}
	}

	status := finding.OK
	switch {
	case spread >= opt.ClockBad:
		status = finding.BAD
	case spread >= opt.ClockWarn:
		status = finding.WARN
	}
	f := finding.Finding{
		Check: "node/clock", Target: name, Status: status,
		Value: finding.Num(spread.Seconds()), Unit: "seconds",
	}
	if status == finding.OK {
		f.Message = fmt.Sprintf("node clocks agree to within %s across %d node(s)", spread.Round(time.Millisecond), len(read))
		return []finding.Finding{f}
	}
	f.Message = fmt.Sprintf("node clocks differ by %s: %s is ahead of %s",
		spread.Round(time.Millisecond), fastest, slowest)
	f.Hint = "Galera orders writes by sequence number, so replication is unaffected — but two error logs cannot be read side by side, and a timestamp in a monitoring system stops meaning anything. Check NTP on " + fastest + " and " + slowest
	return []finding.Finding{f}
}

// writeSetLimits reports appliers that will refuse what was certified (GD-30).
//
// A transaction is certified on the node that accepted it. It is then applied
// everywhere — and an applier whose wsrep_max_ws_size is smaller than the
// write-set refuses it and leaves the cluster, which arrives as a node failure
// rather than as the configuration difference it is.
func writeSetLimits(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, key := range []string{"wsrep_max_ws_size", "wsrep_max_ws_rows"} {
		var reporting []cluster.Snapshot
		for _, s := range live {
			if v, ok := s.Var(key); ok && strings.TrimSpace(v) != "" {
				reporting = append(reporting, s)
			}
		}
		if len(reporting) < 2 {
			continue
		}
		groups := groupBy(reporting, func(s cluster.Snapshot) string {
			v, _ := s.Var(key)
			return strings.TrimSpace(v)
		})
		if len(groups) == 1 {
			continue
		}
		// The smallest non-zero limit is the cluster's real limit: 0 is
		// "unlimited" for both of these.
		var weakest string
		var smallest float64
		for _, s := range reporting {
			v, ok := s.Float(key)
			if !ok || v == 0 {
				continue
			}
			if weakest == "" || v < smallest {
				weakest, smallest = s.Node, v
			}
		}
		msg := fmt.Sprintf("nodes disagree about %s: %s", key, describeGroups(groups))
		if weakest != "" {
			msg += fmt.Sprintf(" — the cluster's real limit is %s's %s", weakest, trimFloat(smallest))
		}
		out = append(out, finding.Finding{
			Check: "repl/ws-limits", Target: name, Status: finding.WARN,
			Message: msg,
			Hint:    "the write-set is certified on the node that accepted it and refused by the applier with the smaller limit, which then leaves the cluster — it arrives as a node failure and it is a configuration difference",
		})
	}
	return out
}

// durability reports nodes whose durability is not the others' (GD-35).
//
// A cluster's durability is its weakest node's, not its average: "committed on
// three nodes" means something different when one of them acknowledges before
// its log reaches the disk. Each node is doing exactly what it was told, so
// nothing reports it — and a uniform relaxed setting is the cluster's decision
// rather than a finding.
func durability(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, v := range []struct{ key, safe string }{
		{"innodb_flush_log_at_trx_commit", "1"},
		{"sync_binlog", "1"},
	} {
		var reporting []cluster.Snapshot
		for _, s := range live {
			if val, ok := s.Var(v.key); ok && strings.TrimSpace(val) != "" {
				reporting = append(reporting, s)
			}
		}
		if len(reporting) < 2 {
			continue
		}
		groups := groupBy(reporting, func(s cluster.Snapshot) string {
			val, _ := s.Var(v.key)
			return strings.TrimSpace(val)
		})
		if len(groups) == 1 {
			continue
		}
		var weak []string
		for _, s := range reporting {
			if val, ok := s.Var(v.key); ok && strings.TrimSpace(val) != v.safe {
				weak = append(weak, s.Node)
			}
		}
		sort.Strings(weak)
		out = append(out, finding.Finding{
			Check: "node/durability", Target: name, Status: finding.WARN,
			Message: fmt.Sprintf("nodes disagree about %s: %s", v.key, describeGroups(groups)),
			Hint: "a cluster's durability is its weakest node's, not its average: on " + strings.Join(weak, ", ") +
				" a commit is acknowledged before it reaches the disk, so \"committed on every node\" survives a process crash and not a power cut",
		})
	}
	return out
}

// segments reports the segment map (GD-31).
//
// Segments are what stops a write-set crossing a WAN link once per node
// instead of once per segment. The intent behind a particular map lives in
// somebody's head and not in the server, so this reports the map it found and
// grades only the case that cannot be deliberate: every node in a segment of
// its own, which turns the optimisation off entirely.
func segments(name string, live []cluster.Snapshot) []finding.Finding {
	var reporting []cluster.Snapshot
	for _, s := range live {
		if v, ok := s.ProviderOption("gmcast.segment"); ok && strings.TrimSpace(v) != "" {
			reporting = append(reporting, s)
		}
	}
	if len(reporting) < 2 {
		return nil
	}
	groups := groupBy(reporting, func(s cluster.Snapshot) string {
		v, _ := s.ProviderOption("gmcast.segment")
		return strings.TrimSpace(v)
	})

	parts := make([]string, 0, len(groups))
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		nodes := append([]string(nil), groups[k]...)
		sort.Strings(nodes)
		parts = append(parts, fmt.Sprintf("segment %s: %s", k, strings.Join(nodes, ", ")))
	}
	msg := strings.Join(parts, "; ")

	if len(groups) == len(reporting) && len(reporting) > 2 {
		return []finding.Finding{{
			Check: "cluster/segments", Target: name, Status: finding.WARN,
			Message: "every node is in a segment of its own — " + msg,
			Value:   finding.Num(float64(len(groups))), Unit: "segments",
			Hint: "a write-set crosses a link once per segment, not once per node: with one node per segment there is nothing left to share, so every node pays the WAN transfer separately",
		}}
	}
	return []finding.Finding{{
		Check: "cluster/segments", Target: name, Status: finding.OK,
		Message: fmt.Sprintf("%d segment(s) over %d node(s) — %s", len(groups), len(reporting), msg),
		Value:   finding.Num(float64(len(groups))), Unit: "segments",
	}}
}

// peerList compares what a node is configured to believe with what is there
// (GD-38).
//
// wsrep_cluster_address is the list of peers a node contacts when it starts.
// It is written by a human, and it is only exercised at a restart: a list that
// names two decommissioned servers, or none of the current members, belongs to
// a node that is Synced and green today and cannot find its cluster tomorrow.
// An empty gcomm:// is worse — that is the bootstrap form, and a restart makes
// the node form its own Primary component.
func peerList(live []cluster.Snapshot) []finding.Finding {
	// Every spelling the cluster answers to, so a peer named by address is not
	// reported as missing.
	known := map[string]bool{}
	for _, s := range live {
		for _, a := range s.Addresses() {
			known[strings.ToLower(a)] = true
		}
	}

	var out []finding.Finding
	for _, s := range live {
		raw, ok := s.Var("wsrep_cluster_address")
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		list := strings.TrimSpace(raw)
		// gcomm://host:port,host?params
		if i := strings.Index(list, "://"); i >= 0 {
			list = list[i+3:]
		}
		if i := strings.Index(list, "?"); i >= 0 {
			list = list[:i]
		}

		var peers, unknown, present []string
		for _, p := range strings.Split(list, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			peers = append(peers, p)
			if known[strings.ToLower(cluster.HostOnly(p))] {
				present = append(present, p)
			} else {
				unknown = append(unknown, p)
			}
		}

		switch {
		case len(peers) == 0:
			out = append(out, finding.Finding{
				Check: "cluster/peers", Target: s.Node, Status: finding.BAD,
				Message: "wsrep_cluster_address is empty (gcomm://): this node would bootstrap its own cluster",
				Hint:    "the empty form is for bootstrapping a new cluster once — left in a running node's configuration it means the next restart forms a second Primary component instead of rejoining this one",
			})
		case len(present) == 0:
			out = append(out, finding.Finding{
				Check: "cluster/peers", Target: s.Node, Status: finding.BAD,
				Message: fmt.Sprintf("wsrep_cluster_address names no current member: %s", strings.Join(peers, ", ")),
				Hint:    "this node cannot rejoin after a restart — it will contact servers that are not in the cluster and give up, and nothing says so until it tries",
			})
		case len(unknown) > 0:
			out = append(out, finding.Finding{
				Check: "cluster/peers", Target: s.Node, Status: finding.WARN,
				Message: fmt.Sprintf("wsrep_cluster_address names %s, which is not in this cluster", strings.Join(unknown, ", ")),
				Hint:    "a restart still works — the remaining peers answer — but the list describes a cluster that no longer exists, and it is one decommission away from naming nobody",
			})
		}
	}
	return out
}

// flowSettings reports flow control that one node decides for everybody
// (GD-39).
//
// The cluster throttles when the slowest queue reaches its own limit, so the
// node configured with the smallest gcs.fc_limit paces every writer in the
// cluster. flow/paused reports the pausing; this reports the reason, which is
// otherwise invisible: each node is doing exactly what it was configured to do.
func flowSettings(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, opt := range []string{"gcs.fc_limit", "gcs.fc_factor", "gcs.fc_master_slave", "gcs.fc_single_primary"} {
		var reporting []cluster.Snapshot
		for _, s := range live {
			if v, ok := s.ProviderOption(opt); ok && strings.TrimSpace(v) != "" {
				reporting = append(reporting, s)
			}
		}
		if len(reporting) < 2 {
			continue
		}
		groups := groupBy(reporting, func(s cluster.Snapshot) string {
			v, _ := s.ProviderOption(opt)
			return strings.TrimSpace(v)
		})
		if len(groups) == 1 {
			continue
		}
		msg := fmt.Sprintf("nodes disagree about %s: %s", opt, describeGroups(groups))
		hint := "these settings are the cluster's pacing, not a node's: the strictest node decides when everybody waits"
		if opt == "gcs.fc_limit" {
			// The smallest limit is the one that fires first.
			var strictest string
			var smallest float64
			for _, s := range reporting {
				v, _ := s.ProviderOption(opt)
				f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
				if err != nil {
					continue
				}
				if strictest == "" || f < smallest {
					strictest, smallest = s.Node, f
				}
			}
			if strictest != "" {
				msg += fmt.Sprintf(" — %s throttles first, at %s", strictest, trimFloat(smallest))
			}
			hint = "the cluster pauses when the slowest queue hits its own limit, so " + strictest +
				" paces every writer in the cluster — flow/paused reports that pausing without this reason"
		}
		out = append(out, finding.Finding{
			Check: "flow/settings", Target: name, Status: finding.WARN,
			Message: msg, Hint: hint,
		})
	}
	return out
}

// appliers reports nodes that apply with fewer threads than their peers
// (GD-40).
//
// A node with a quarter of its peers' apply threads is slower by
// configuration rather than by load, and it shows up as queue depth — which
// sends whoever is on call to look at its disk. The receive queue goes in the
// same line for exactly that reason.
func appliers(name string, live []cluster.Snapshot) []finding.Finding {
	// MariaDB 10.6 renamed the variable; a build may report either or both.
	value := func(s cluster.Snapshot) (float64, bool) {
		for _, key := range []string{"wsrep_applier_threads", "wsrep_slave_threads"} {
			v, ok := s.Var(key)
			if !ok || strings.TrimSpace(v) == "" {
				continue
			}
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return f, true
			}
		}
		return 0, false
	}

	var reporting []cluster.Snapshot
	for _, s := range live {
		if _, ok := value(s); ok {
			reporting = append(reporting, s)
		}
	}
	if len(reporting) < 2 {
		return nil
	}
	groups := groupBy(reporting, func(s cluster.Snapshot) string {
		f, _ := value(s)
		return trimFloat(f)
	})
	if len(groups) == 1 {
		return nil
	}

	var slowest string
	var fewest float64
	for _, s := range reporting {
		f, _ := value(s)
		if slowest == "" || f < fewest {
			slowest, fewest = s.Node, f
		}
	}
	msg := fmt.Sprintf("nodes apply with different thread counts: %s — %s has the fewest, %s",
		describeGroups(groups), slowest, trimFloat(fewest))
	for _, s := range live {
		if s.Node != slowest {
			continue
		}
		if q, ok := s.Float("wsrep_local_recv_queue"); ok {
			msg += fmt.Sprintf(" (receive queue %s)", trimFloat(q))
		}
	}
	return []finding.Finding{{
		Check: "repl/appliers", Target: name, Status: finding.WARN,
		Message: msg,
		Value:   finding.Num(fewest), Unit: "threads",
		Hint: "an applier count is per node while everybody discusses throughput as the cluster's: " + slowest +
			" is behind by configuration rather than by load, which is a different fix from looking at its disk",
	}}
}

// sstSize reports what a rejoin will actually copy (GD-41).
//
// "This node needs a full SST" is not actionable on its own. The number of
// gigabytes it implies is what tells whoever is on call whether that is two
// minutes or two hours — and a full SST takes the donor out of service for the
// duration, so the sentence has to carry both. The size is a number, not a
// fault, so this is OK: gcache/window is the check that grades whether an SST
// is likely in the first place.
func sstSize(name string, live []cluster.Snapshot) []finding.Finding {
	var largest int64
	var on string
	for _, s := range live {
		if s.DataBytes == nil {
			continue
		}
		if on == "" || *s.DataBytes > largest {
			largest, on = *s.DataBytes, s.Node
		}
	}
	if on == "" {
		return nil
	}

	msg := fmt.Sprintf("a full state transfer copies about %s (largest node: %s)", humanBytes(largest), on)
	methods := map[string]bool{}
	for _, s := range live {
		if v, ok := s.Var("wsrep_sst_method"); ok && strings.TrimSpace(v) != "" {
			methods[strings.ToLower(strings.TrimSpace(v))] = true
		}
	}
	if len(methods) == 1 {
		for m := range methods {
			msg += " with " + m
		}
	}
	return []finding.Finding{{
		Check: "sst/size", Target: name, Status: finding.OK,
		Message: msg,
		Value:   finding.Num(float64(largest)), Unit: "bytes",
		Hint: "that is how much a rejoining node has to receive, and how long a donor is out of service while it sends it — the pair of numbers behind \"needs a full SST\"",
	}}
}

// coverage says what this run could not audit (GD-43).
//
// A cron job sees an exit code and a worst status. Neither of them
// distinguishes "nothing is wrong" from "the check that would have found it
// never ran" — a missing grant, a metric this build does not report, a node
// that could not be read. This is the one line that does.
//
// Access gaps escalate to WARN because a statement was not made. A missing
// baseline does not: it is named in the same line, but running without --state
// is a choice, and warning about a choice on every run is how a check stops
// being read.
func coverage(name string, snaps, live []cluster.Snapshot, prev *state.State) []finding.Finding {
	var unread, noSysTables, noSchema, noClock, noSize, noReplication []string
	for _, s := range snaps {
		if !s.OK() {
			unread = append(unread, s.Node)
		}
	}
	for _, s := range live {
		if len(s.SysTables) == 0 {
			noSysTables = append(noSysTables, s.Node)
		}
		if s.AppTables == nil {
			noSchema = append(noSchema, s.Node)
		}
		if s.Clock.IsZero() {
			noClock = append(noClock, s.Node)
		}
		if s.DataBytes == nil {
			noSize = append(noSize, s.Node)
		}
		if s.Replicas == nil && s.ReplicaHosts == nil {
			noReplication = append(noReplication, s.Node)
		}
	}

	var gaps []string
	access := false
	if len(unread) > 0 {
		gaps = append(gaps, fmt.Sprintf("%d node(s) could not be read (%s)", len(unread), strings.Join(unread, ", ")))
		access = true
	}
	if len(noSysTables) > 0 {
		gaps = append(gaps, "system table definitions on "+strings.Join(noSysTables, ", "))
		access = true
	}
	if len(noSchema) > 0 {
		gaps = append(gaps, "application schemas on "+strings.Join(noSchema, ", "))
		access = true
	}
	if len(noClock) > 0 {
		gaps = append(gaps, "the clock on "+strings.Join(noClock, ", "))
		access = true
	}
	if len(noSize) > 0 {
		gaps = append(gaps, "the dataset size on "+strings.Join(noSize, ", "))
		access = true
	}
	if len(noReplication) > 0 {
		gaps = append(gaps, "the replication status on "+strings.Join(noReplication, ", "))
		access = true
	}
	if prev == nil {
		gaps = append(gaps, "no baseline: the counter checks report lifetime totals instead of the interval")
	}

	if len(gaps) == 0 {
		return []finding.Finding{{
			Check: "audit/coverage", Target: name, Status: finding.OK,
			Message: fmt.Sprintf("every check ran, across %d node(s)", len(live)),
			Value:   finding.Num(float64(len(live))), Unit: "nodes",
		}}
	}
	status := finding.OK
	prefix := "everything was audited, with one caveat: "
	if access {
		status = finding.WARN
		prefix = "audited with gaps: "
	}
	if len(gaps) > 1 {
		prefix = "audited with gaps: "
	}
	return []finding.Finding{{
		Check: "audit/coverage", Target: name, Status: status,
		Message: prefix + strings.Join(gaps, "; "),
		Value:   finding.Num(float64(len(live))), Unit: "nodes",
		Hint: "an OK from a check that never ran is not an OK — grant the audit user what is missing above, or take the gap into account before believing this report",
	}}
}

// humanBytes prints a size the way somebody talks about it.
func humanBytes(n int64) string {
	const unit = 1024.0
	f := float64(n)
	for _, suffix := range []string{"B", "KiB", "MiB", "GiB", "TiB"} {
		if f < unit || suffix == "TiB" {
			if suffix == "B" {
				return fmt.Sprintf("%d B", n)
			}
			return fmt.Sprintf("%.1f %s", f, suffix)
		}
		f /= unit
	}
	return fmt.Sprintf("%d B", n)
}

// changes says what moved since the previous run (GD-32).
//
// The person reading this ran the audit twenty minutes ago and did something in
// between. What they need is not the same list again: it is which findings
// appeared, which cleared, and which got worse. Only the statuses are compared
// — the messages carry measurements, and comparing prose would report a change
// every time a percentage moved by 0.1.
//
// The summary is OK whatever it contains: the findings it describes are in the
// same report with their own severities, and counting them twice would make one
// incident look like two.
func changes(name string, current []finding.Finding, prev *state.State, now time.Time) []finding.Finding {
	if prev == nil || prev.Version != state.Version || len(prev.Findings) == 0 {
		return nil
	}

	// The summary is never part of what it compares: a run that stored its own
	// transition line would report itself as a change forever.
	nowMap := carry(current)

	var appeared, cleared, worse, better []string
	for key, status := range nowMap {
		before, seen := prev.Findings[key]
		switch {
		case !seen && status != string(finding.OK):
			appeared = append(appeared, fmt.Sprintf("%s (%s)", key, status))
		case seen && before != status:
			if finding.Severity(finding.Status(status)) > finding.Severity(finding.Status(before)) {
				worse = append(worse, fmt.Sprintf("%s (%s → %s)", key, before, status))
			} else if status == string(finding.OK) {
				cleared = append(cleared, key)
			} else {
				better = append(better, fmt.Sprintf("%s (%s → %s)", key, before, status))
			}
		}
	}
	// A finding that is gone entirely cleared too: the check may not even have
	// run this time, and saying so is better than silence.
	for key, before := range prev.Findings {
		if _, still := nowMap[key]; !still && before != string(finding.OK) {
			cleared = append(cleared, key+" (no longer reported)")
		}
	}
	sort.Strings(appeared)
	sort.Strings(cleared)
	sort.Strings(worse)
	sort.Strings(better)

	interval := now.Sub(prev.At).Round(time.Second)
	var parts []string
	for _, group := range []struct {
		label string
		items []string
	}{
		{"got worse", worse},
		{"appeared", appeared},
		{"cleared", cleared},
		{"improved", better},
	} {
		if len(group.items) == 0 {
			continue
		}
		shown := group.items
		suffix := ""
		if len(shown) > 4 {
			shown, suffix = shown[:4], fmt.Sprintf(" (+%d more)", len(group.items)-4)
		}
		parts = append(parts, fmt.Sprintf("%s: %s%s", group.label, strings.Join(shown, ", "), suffix))
	}

	if len(parts) == 0 {
		return []finding.Finding{{
			Check: "audit/changes", Target: name, Status: finding.OK,
			Message: fmt.Sprintf("nothing changed since the previous run (%s ago)", interval),
			Value:   finding.Num(0), Unit: "changes",
		}}
	}
	total := len(appeared) + len(cleared) + len(worse) + len(better)
	return []finding.Finding{{
		Check: "audit/changes", Target: name, Status: finding.OK,
		Message: fmt.Sprintf("since the previous run (%s ago) — %s", interval, strings.Join(parts, "; ")),
		Value:   finding.Num(float64(total)), Unit: "changes",
		Hint: "the transitions, not the verdicts: each of these is reported with its own severity elsewhere in this report, and \"got worse\" is where to start",
	}}
}

// carry is the findings of this run in the shape the next one compares
// against: the status per check and target, and never the transition summary
// itself — a run that stored that would report itself as a change forever.
func carry(fs []finding.Finding) map[string]string {
	out := make(map[string]string, len(fs))
	for _, f := range fs {
		if f.Check == "audit/changes" {
			continue
		}
		out[state.Key(f.Check, f.Target)] = string(f.Status)
	}
	return out
}

// latencySlowFactor is how much slower than its own segment's fastest peer a
// node has to be before it is a finding. A ratio rather than a threshold,
// because the right absolute number is different in every rack and every
// datacentre — and LatencyFloor keeps a ratio between microseconds out of the
// report.
const latencySlowFactor = 4

// latency says whether a node is slow or simply far away (GD-17).
//
// queue/send reports a deep queue and cannot say why: a node across a WAN link
// is doing exactly what physics allows, and a node with a failing disk in the
// same rack looks identical from there. Two things the cluster already knows
// make the distinction — wsrep_evs_repl_latency, which is its own measurement
// of the round trip, and gmcast.segment, which says which pairs are *supposed*
// to be far apart.
//
// So the comparison happens inside a segment. Across segments the latency is
// reported and never graded: that is what the segment was configured for.
func latency(name string, live []cluster.Snapshot, opt Options) []finding.Finding {
	type sample struct {
		node    string
		segment string
		avg     time.Duration
		max     time.Duration
	}
	var samples []sample
	for _, s := range live {
		avg, max, ok := s.ReplLatency()
		if !ok {
			continue
		}
		seg := s.Segment()
		if seg == "" {
			seg = "0" // the default, and the only sensible name for "unset"
		}
		samples = append(samples, sample{node: s.Node, segment: seg, avg: avg, max: max})
	}
	if len(samples) == 0 {
		return nil
	}

	bySegment := map[string][]sample{}
	for _, s := range samples {
		bySegment[s.segment] = append(bySegment[s.segment], s)
	}
	segs := make([]string, 0, len(bySegment))
	for seg := range bySegment {
		segs = append(segs, seg)
	}
	sort.Strings(segs)

	var out []finding.Finding
	parts := make([]string, 0, len(segs))
	for _, seg := range segs {
		in := bySegment[seg]
		fastest := in[0]
		var total time.Duration
		for _, s := range in {
			if s.avg < fastest.avg {
				fastest = s
			}
			total += s.avg
		}
		parts = append(parts, fmt.Sprintf("segment %s: %s avg over %d node(s)",
			seg, (total/time.Duration(len(in))).Round(time.Microsecond), len(in)))

		// Inside one segment, distance is not an explanation.
		for _, s := range in {
			if s.node == fastest.node || len(in) < 2 {
				continue
			}
			if s.avg < opt.LatencyFloor || s.avg < time.Duration(latencySlowFactor)*fastest.avg {
				continue
			}
			hint := fmt.Sprintf("same segment, so this is not distance: the link to %s, or that node itself. ", s.node)
			for _, l := range live {
				if l.Node != s.node {
					continue
				}
				if q, ok := l.Float("wsrep_local_send_queue"); ok {
					hint += fmt.Sprintf("Its send queue is %s", trimFloat(q))
				}
			}
			out = append(out, finding.Finding{
				Check: "cluster/latency", Target: s.node, Status: finding.WARN,
				Message: fmt.Sprintf("replicates at %s in segment %s, %.0fx the %s of %s",
					s.avg.Round(time.Microsecond), s.segment,
					float64(s.avg)/float64(fastest.avg), fastest.avg.Round(time.Microsecond), fastest.node),
				Value: finding.Num(s.avg.Seconds() * 1000), Unit: "ms",
				Hint: strings.TrimSpace(hint),
			})
		}
	}

	// The map itself, which is what makes "far away" a fact rather than an
	// assumption.
	out = append(out, finding.Finding{
		Check: "cluster/latency", Target: name, Status: finding.OK,
		Message: "replication latency as the cluster measures it — " + strings.Join(parts, "; "),
		Hint:    "across segments this is distance and nothing to fix; inside one it is the link or the node",
	})
	return out
}

// serverIdentity reports what breaks outside the cluster (GD-48).
//
// server_id has to be distinct on every node: two members sharing one make an
// async replica downstream unable to tell their events apart, and a replication
// loop possible. gtid_domain_id is the opposite requirement — every node should
// share it, or a failover inside the cluster rewrites history for every replica
// reading from it.
//
// Neither is visible from inside: the cluster replicates by writeset and does
// not care. The nodes only find out through somebody else's replica, which is
// why the domain checks are graded only when a binary log exists to replicate
// from at all. This tool does not have opinions about settings nothing reads.
func serverIdentity(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding

	// Duplicate server ids, with or without a binlog: this one breaks the
	// cluster's own internals too.
	ids := map[string][]string{}
	for _, s := range live {
		v, ok := s.Var("server_id")
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		id := strings.TrimSpace(v)
		ids[id] = append(ids[id], s.Node)
	}
	var dupes []string
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		if len(ids[id]) < 2 {
			continue
		}
		nodes := append([]string(nil), ids[id]...)
		sort.Strings(nodes)
		dupes = append(dupes, fmt.Sprintf("%s on %s", id, strings.Join(nodes, ", ")))
	}
	if len(dupes) > 0 {
		out = append(out, finding.Finding{
			Check: "repl/server-id", Target: name, Status: finding.BAD,
			Message: "nodes share a server_id: " + strings.Join(dupes, "; "),
			Hint:    "an async replica downstream cannot tell their events apart, and a replication loop becomes possible — every node needs its own, and it has to survive being rebuilt from a backup",
		})
	}

	// Anything that leaves the cluster needs a binary log somewhere.
	binlog := false
	for _, s := range live {
		if on, ok := s.Bool("log_bin"); ok && on {
			binlog = true
			break
		}
	}
	if !binlog {
		return out
	}

	for _, v := range []struct {
		key, check, hint string
	}{
		{"gtid_domain_id", "repl/gtid-domain",
			"every node in the cluster should share the domain: with different ones, a failover inside the cluster rewrites history for every downstream replica reading from it — and nothing inside the cluster notices, because replication here is by writeset"},
		{"gtid_strict_mode", "repl/gtid-strict",
			"the nodes enforce different rules about out-of-order GTIDs, so whether a downstream replica refuses a bad sequence depends on which node it happened to be reading from"},
	} {
		var reporting []cluster.Snapshot
		for _, s := range live {
			if val, ok := s.Var(v.key); ok && strings.TrimSpace(val) != "" {
				reporting = append(reporting, s)
			}
		}
		if len(reporting) < 2 {
			continue
		}
		groups := groupBy(reporting, func(s cluster.Snapshot) string {
			val, _ := s.Var(v.key)
			return strings.TrimSpace(val)
		})
		if len(groups) == 1 {
			continue
		}
		out = append(out, finding.Finding{
			Check: v.check, Target: name, Status: finding.WARN,
			Message: fmt.Sprintf("nodes disagree about %s: %s", v.key, describeGroups(groups)),
			Hint:    v.hint,
		})
	}
	return out
}

// applierTriggers reports triggers that run on one node only (GD-50).
//
// A trigger on the writer has already put its rows into the writeset, so an
// applier that runs the trigger again applies them twice, and one that does not
// is doing the right thing. Nodes disagreeing is therefore divergence produced
// by design — the same statement, applied on each node, ends up with different
// rows — and it is certified by nothing, because certification compares
// writesets and not their consequences.
func applierTriggers(name string, live []cluster.Snapshot) []finding.Finding {
	var reporting []cluster.Snapshot
	on := 0
	for _, s := range live {
		v, ok := s.Bool("wsrep_slave_run_triggers")
		if _, present := s.Var("wsrep_slave_run_triggers"); !present || !ok {
			continue
		}
		reporting = append(reporting, s)
		if v {
			on++
		}
	}
	if len(reporting) < 2 {
		return nil
	}
	groups := groupBy(reporting, func(s cluster.Snapshot) string {
		v, _ := s.Var("wsrep_slave_run_triggers")
		return strings.ToUpper(strings.TrimSpace(v))
	})

	const consequence = "the writer's trigger has already put its rows in the writeset, so an applier that runs the trigger again applies them twice"
	if len(groups) > 1 {
		return []finding.Finding{{
			Check: "repl/triggers", Target: name, Status: finding.BAD,
			Message: "nodes disagree about wsrep_slave_run_triggers: " + describeGroups(groups),
			Hint:    consequence + " — so the same statement ends up with different rows per node, and certification compares writesets rather than their consequences",
		}}
	}
	if on == len(reporting) {
		return []finding.Finding{{
			Check: "repl/triggers", Target: name, Status: finding.WARN,
			Message: fmt.Sprintf("all %d node(s) run triggers on apply (wsrep_slave_run_triggers is ON)", len(reporting)),
			Hint:    consequence + " — uniform, so the nodes stay consistent with each other, but each row the trigger writes is applied twice unless the trigger is written to expect that",
		}}
	}
	return nil
}

// asyncReplication reports the write paths a cluster diagram does not show
// (GD-47).
//
// A cluster is drawn as three nodes replicating to each other. It does not show
// the member that is also an async replica of a legacy server — a second write
// path *into* the cluster, whose writes are certified like any other and
// arrive from somewhere nobody listed — nor the member feeding a reporting
// replica downstream, which is a dependency the other nodes know nothing about
// and which the next state transfer breaks, because an SST rebuilds this
// node's binary logs out from under whoever is reading them.
//
// The cluster cannot see a write path it is not part of, which is why none of
// this appears in any wsrep_* counter.
func asyncReplication(live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		for _, link := range s.Replicas {
			name := link.Name
			if name == "" {
				name = "the default connection"
			}
			if !link.Running() {
				msg := fmt.Sprintf("async replication from %s is configured and not running (%s)", link.Source, name)
				if link.LastError != "" {
					msg += ": " + link.LastError
				}
				out = append(out, finding.Finding{
					Check: "repl/async-in", Target: s.Node, Status: finding.BAD,
					Message: msg,
					Hint:    "somebody believes those writes are arriving in this cluster — and a link that resumes after a long stop replays everything it missed into a cluster that has moved on",
				})
				continue
			}
			msg := fmt.Sprintf("replicating asynchronously from %s (%s)", link.Source, name)
			if link.Behind != nil {
				msg += fmt.Sprintf(", %s behind", (time.Duration(*link.Behind) * time.Second).String())
			}
			out = append(out, finding.Finding{
				Check: "repl/async-in", Target: s.Node, Status: finding.WARN,
				Message: msg,
				Hint:    "this is a second write path into the cluster, from a server no cluster check can see: those writes certify like any other, and the cluster has no opinion about where they came from",
			})
		}

		if len(s.ReplicaHosts) == 0 {
			continue
		}
		hosts := append([]string(nil), s.ReplicaHosts...)
		sort.Strings(hosts)
		out = append(out, finding.Finding{
			Check: "repl/async-out", Target: s.Node, Status: finding.WARN,
			Message: fmt.Sprintf("%d replica(s) read from this node: %s", len(hosts), strings.Join(hosts, ", ")),
			Value:   finding.Num(float64(len(hosts))), Unit: "replicas",
			Hint: "a dependency the rest of the cluster knows nothing about: the next SST rebuilds this node's binary logs out from under them, and so does anything that reinitialises it",
		})
	}
	return out
}

// binlog reports what the cluster cannot be used for (GD-51).
//
// Galera does not need the binary log: it replicates by writeset. Everything
// *around* a cluster needs it — a backup, a downstream replica, a
// point-in-time recovery — so a node with it off is a node none of those can
// be taken from, and a failover is when people find that out. Same for
// log_slave_updates: a node that does not log what it applies cannot be the
// source of a downstream replica, however healthy it looks.
func binlog(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding

	var off []string
	reporting := 0
	for _, s := range live {
		on, ok := s.Bool("log_bin")
		if !ok {
			continue
		}
		reporting++
		if !on {
			off = append(off, s.Node)
		}
	}
	if len(off) > 0 && len(off) < reporting {
		sort.Strings(off)
		out = append(out, finding.Finding{
			Check: "node/binlog", Target: name, Status: finding.WARN,
			Message: fmt.Sprintf("log_bin is off on %s, and on elsewhere", strings.Join(off, ", ")),
			Hint:    "no backup, no downstream replica and no point-in-time recovery can be taken from those nodes — which is discovered during a failover, when they are the ones left",
		})
	}

	// Galera requires ROW. A cluster that agrees on something else agrees on
	// something unsupported.
	var formats []cluster.Snapshot
	for _, s := range live {
		if v, ok := s.Var("binlog_format"); ok && strings.TrimSpace(v) != "" {
			formats = append(formats, s)
		}
	}
	if len(formats) > 0 {
		groups := groupBy(formats, func(s cluster.Snapshot) string {
			v, _ := s.Var("binlog_format")
			return strings.ToUpper(strings.TrimSpace(v))
		})
		switch {
		case len(groups) > 1:
			out = append(out, finding.Finding{
				Check: "node/binlog-format", Target: name, Status: finding.WARN,
				Message: "nodes disagree about binlog_format: " + describeGroups(groups),
				Hint:    "Galera requires ROW; anything else is unsupported for replication here, and it changes what a downstream replica receives depending on which node it reads from",
			})
		default:
			for format := range groups {
				if format == "ROW" {
					break
				}
				out = append(out, finding.Finding{
					Check: "node/binlog-format", Target: name, Status: finding.WARN,
					Message: fmt.Sprintf("binlog_format is %s on all %d node(s)", format, len(formats)),
					Hint:    "Galera requires ROW — the nodes agree, and they agree on something its own documentation calls unsupported",
				})
			}
		}
	}

	// Only worth saying when there is a binary log to write into.
	anyBinlog := false
	for _, s := range live {
		if on, ok := s.Bool("log_bin"); ok && on {
			anyBinlog = true
			break
		}
	}
	if anyBinlog {
		var updates []cluster.Snapshot
		for _, s := range live {
			if _, ok := s.Var("log_slave_updates"); ok {
				updates = append(updates, s)
			}
		}
		if len(updates) > 1 {
			groups := groupBy(updates, func(s cluster.Snapshot) string {
				v, _ := s.Var("log_slave_updates")
				return strings.ToUpper(strings.TrimSpace(v))
			})
			if len(groups) > 1 {
				out = append(out, finding.Finding{
					Check: "node/binlog-updates", Target: name, Status: finding.WARN,
					Message: "nodes disagree about log_slave_updates: " + describeGroups(groups),
					Hint:    "a node that does not log the writesets it applies cannot be the source of a downstream replica: failing over to it breaks that replica silently, and nothing in the cluster notices",
				})
			}
		}
	}
	return out
}

// writers reports who is actually writing (GD-49).
//
// "We only write to one node" is a belief, and the cluster has the numbers:
// wsrep_replicated counts the writesets each node *originated*. A second
// writer nobody meant to have is the cause behind half the certification
// failures repl/cert-failures reports, and it is invisible in any per-node
// dashboard, because each node looks busy in its own right.
//
// A lifetime total answers a different question — who has written since each
// node last restarted — so without a baseline this says so and does not grade.
func writers(name string, live []cluster.Snapshot, prev *state.State, now state.State) []finding.Finding {
	type share struct {
		node  string
		count float64
	}
	var shares []share
	var total float64
	graded := true
	for _, s := range live {
		ns, ok := now.Nodes[s.Node]
		if !ok {
			continue
		}
		if d, ok := prev.Since(s.Node, "wsrep_replicated", ns); ok {
			shares = append(shares, share{node: s.Node, count: d.Value})
			total += d.Value
			continue
		}
		graded = false
		if v, ok := s.Float("wsrep_replicated"); ok {
			shares = append(shares, share{node: s.Node, count: v})
			total += v
		}
	}
	if len(shares) == 0 {
		return nil
	}
	sort.SliceStable(shares, func(i, j int) bool { return shares[i].count > shares[j].count })

	if !graded {
		return []finding.Finding{{
			Check: "repl/writers", Target: name, Status: finding.OK,
			Message: fmt.Sprintf("%s has originated the most writesets since its last restart (not graded: no baseline)", shares[0].node),
			Hint:    "run again with --state: over an interval this says who is writing *now*, which is the question — a lifetime total says who wrote since each node last came up",
		}}
	}
	if total == 0 {
		return []finding.Finding{{
			Check: "repl/writers", Target: name, Status: finding.OK,
			Message: "no writes replicated in the interval",
			Value:   finding.Num(0), Unit: "writesets",
		}}
	}

	// A share this small is a heartbeat, a schema tool, or a stray connection
	// — not a second writer, and grading it would make every cluster look
	// multi-writer.
	const minShare = 0.02
	var writing []string
	for _, sh := range shares {
		if sh.count/total < minShare {
			continue
		}
		writing = append(writing, fmt.Sprintf("%s %.0f%%", sh.node, 100*sh.count/total))
	}
	if len(writing) < 2 {
		return []finding.Finding{{
			Check: "repl/writers", Target: name, Status: finding.OK,
			Message: fmt.Sprintf("writes arrive on %s (%.0f%% of %s writesets in the interval)",
				shares[0].node, 100*shares[0].count/total, trimFloat(total)),
			Value: finding.Num(total), Unit: "writesets",
		}}
	}
	return []finding.Finding{{
		Check: "repl/writers", Target: name, Status: finding.WARN,
		Message: fmt.Sprintf("writes arrive on %d nodes: %s (%s writesets in the interval)",
			len(writing), strings.Join(writing, ", "), trimFloat(total)),
		Value: finding.Num(float64(len(writing))), Unit: "nodes",
		Hint: "if this cluster is meant to have one writer, something is bypassing that — and writing to several nodes is where certification conflicts come from, so this is the cause behind a repl/cert-failures finding rather than another symptom",
	}}
}

// restarted reports a node that came back between two runs (GD-52).
//
// A restart resets every counter, which is exactly why the rate checks fall
// back to "no baseline" — and that fallback, on its own, looks like a tool
// being coy. This says what happened: wsrep_gcomm_uuid is a new value on every
// boot, and an uptime shorter than it was is the same event on a build that
// does not report the uuid.
func restarted(live []cluster.Snapshot, prev *state.State, now state.State) []finding.Finding {
	if prev == nil || prev.Version != state.Version {
		return nil
	}
	var out []finding.Finding
	for _, s := range live {
		before, ok := prev.Nodes[s.Node]
		if !ok {
			continue
		}
		current, ok := now.Nodes[s.Node]
		if !ok {
			continue
		}

		var why string
		switch {
		// An absent uuid on either side is "nothing to compare", never a
		// change: an older state file must not report every node as
		// restarted.
		case before.GcommUUID != "" && current.GcommUUID != "" && before.GcommUUID != current.GcommUUID:
			why = "its group communication UUID changed"
		case before.Uptime > 0 && current.Uptime > 0 && current.Uptime < before.Uptime:
			why = fmt.Sprintf("its uptime went from %s to %s",
				(time.Duration(before.Uptime) * time.Second).String(),
				(time.Duration(current.Uptime) * time.Second).String())
		default:
			continue
		}
		out = append(out, finding.Finding{
			Check: "node/restarted", Target: s.Node, Status: finding.WARN,
			Message: "this node restarted since the previous run: " + why,
			Hint:    "every counter on it started again from zero, which is why the rate checks above report no baseline for it — and if nobody planned this restart, that is the finding rather than the rates",
		})
	}
	return out
}

// membershipView compares the group's own list of members with the nodes this
// run audited (GD-53).
//
// Every other membership check reads what each node says about *itself*:
// wsrep_cluster_size, wsrep_cluster_conf_id, wsrep_cluster_status. The
// wsrep_info plugin exposes the group's list, which makes two independent
// views of one membership — and the interesting states live only in the
// comparison. A member the group lists that this run never read is a node
// every cluster-wide statement above was made without; a node that is Synced
// and green while the group has not listed it is a state no single node can
// report about itself.
//
// The plugin is optional, so its absence is silence rather than a gap: unlike
// a missing grant, nothing was denied.
func membershipView(name string, live []cluster.Snapshot) []finding.Finding {
	// The group's list, from whichever nodes can see it, de-duplicated.
	seen := map[string]cluster.Member{}
	for _, s := range live {
		for _, m := range s.Membership {
			key := strings.ToLower(cluster.HostOnly(m.Name))
			if key == "" {
				key = strings.ToLower(m.UUID)
			}
			if key == "" {
				continue
			}
			if _, ok := seen[key]; !ok {
				seen[key] = m
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}

	// Every spelling each audited node answers to, exactly as the proxy's
	// server list is matched: the plugin reports what a node calls itself.
	audited := map[string]string{}
	for _, s := range live {
		for _, a := range s.Addresses() {
			audited[strings.ToLower(cluster.HostOnly(a))] = s.Node
		}
	}

	var unknown []string
	for key, m := range seen {
		if _, ok := audited[key]; ok {
			continue
		}
		label := m.Name
		if label == "" {
			label = m.UUID
		}
		unknown = append(unknown, label)
	}
	sort.Strings(unknown)

	var unlisted []string
	for _, s := range live {
		found := false
		for _, a := range s.Addresses() {
			if _, ok := seen[strings.ToLower(cluster.HostOnly(a))]; ok {
				found = true
				break
			}
		}
		if !found {
			unlisted = append(unlisted, s.Node)
		}
	}
	sort.Strings(unlisted)

	var out []finding.Finding
	if len(unlisted) > 0 {
		out = append(out, finding.Finding{
			Check: "cluster/membership-view", Target: name, Status: finding.BAD,
			Message: fmt.Sprintf("the cluster does not list %s, which reported itself as a member",
				strings.Join(unlisted, ", ")),
			Hint: "the node believes it is in the group and the group has not listed it — no single node can report this about itself, and writes it accepts are certified by a component it is not part of",
		})
	}
	if len(unknown) > 0 {
		out = append(out, finding.Finding{
			Check: "cluster/membership-view", Target: name, Status: finding.WARN,
			Message: fmt.Sprintf("the cluster lists %d member(s) this run did not audit: %s",
				len(unknown), strings.Join(unknown, ", ")),
			Value: finding.Num(float64(len(unknown))), Unit: "nodes",
			Hint: "every cluster-wide statement in this report was made without them: add them to the config, or find out what they are — a member nobody knows about is a member nobody is watching",
		})
	}
	return out
}

// primaryKeys reports application tables Galera cannot certify reliably.
func primaryKeys(live []cluster.Snapshot) []finding.Finding {
	seen := map[string]bool{}
	for _, s := range live {
		for _, t := range s.TablesNoPK {
			seen[t] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	tables := make([]string, 0, len(seen))
	for t := range seen {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	shown := tables
	suffix := ""
	if len(shown) > 5 {
		shown, suffix = shown[:5], fmt.Sprintf(" (+%d more)", len(tables)-5)
	}
	return []finding.Finding{{
		Check: "schema/no-pk", Target: "schema", Status: finding.WARN,
		Message: fmt.Sprintf("%d table(s) without a primary key: %s%s", len(tables), strings.Join(shown, ", "), suffix),
		Value:   finding.Num(float64(len(tables))), Unit: "tables",
		Hint: "row-based replication applies these by full-row scan on every node, and DELETEs can apply in a different order — Galera's own documentation calls this unsupported",
	}}
}

// versions reports a cluster running more than one build.
func versions(name string, live []cluster.Snapshot) []finding.Finding {
	var out []finding.Finding
	server := groupBy(live, func(s cluster.Snapshot) string {
		v, _ := s.Var("version")
		return v
	})
	if len(server) > 1 {
		out = append(out, finding.Finding{
			Check: "cluster/versions", Target: name, Status: finding.WARN,
			Message: "mixed server versions: " + describeGroups(server),
			Value:   finding.Num(float64(len(server))), Unit: "versions",
			Hint: "expected during a rolling upgrade and a liability after it: keep the window short",
		})
	}
	provider := groupBy(live, func(s cluster.Snapshot) string {
		v, _ := s.Get("wsrep_provider_version")
		return v
	})
	if len(provider) > 1 {
		out = append(out, finding.Finding{
			Check: "cluster/provider-version", Target: name, Status: finding.WARN,
			Message: "mixed wsrep provider versions: " + describeGroups(provider),
			Value:   finding.Num(float64(len(provider))), Unit: "versions",
			Hint: "the group communication protocol is negotiated down to the oldest member",
		})
	}
	return out
}

// gcache estimates how far back a restarting node can catch up incrementally.
//
// The number that matters is not the gcache size but the *time* it buys at the
// current write rate, and that needs two runs. Without a baseline the size is
// reported without a verdict, because "1 GB" is either an hour or ninety
// seconds depending on a workload the tool has not measured yet.
func gcache(live []cluster.Snapshot, prev *state.State, now state.State, opt Options) []finding.Finding {
	var out []finding.Finding
	for _, s := range live {
		raw, ok := s.ProviderOption("gcache.size")
		if !ok {
			continue
		}
		size, ok := cluster.Bytes(raw)
		if !ok || size <= 0 {
			continue
		}
		d, haveRate := prev.Since(s.Node, "wsrep_replicated_bytes", now.Nodes[s.Node])
		if !haveRate || d.PerSecond() <= 0 {
			out = append(out, finding.Finding{
				Check: "gcache/window", Target: s.Node, Status: finding.OK,
				Message: fmt.Sprintf("gcache is %s (not graded: no write-rate baseline)", raw),
				Value:   finding.Num(float64(size)), Unit: "bytes",
				Hint: "run again with --state: the useful number is how many minutes of writes it holds, not how many bytes",
			})
			continue
		}
		window := time.Duration(float64(size)/d.PerSecond()) * time.Second
		st := finding.OK
		hint := ""
		if window < opt.ISTWarn {
			st = finding.WARN
			hint = "a node restarting after longer than this needs a full SST: hours of transfer, and a donor taken out of service to provide it"
		}
		out = append(out, finding.Finding{
			Check: "gcache/window", Target: s.Node, Status: st,
			Message: fmt.Sprintf("gcache %s holds about %s of writes at the current rate (%.1f MB/s)",
				raw, window.Round(time.Minute), d.PerSecond()/(1<<20)),
			Value: finding.Num(window.Seconds()), Unit: "seconds", Hint: hint,
		})
	}
	return out
}

// groupBy buckets nodes by a value. A missing value is its own bucket, spelled
// so a report does not show an empty string.
func groupBy(live []cluster.Snapshot, key func(cluster.Snapshot) string) map[string][]string {
	out := map[string][]string{}
	for _, s := range live {
		v := key(s)
		if v == "" {
			v = "unknown"
		}
		out[v] = append(out[v], s.Node)
	}
	return out
}

// describeGroups renders "value (node, node) vs value (node)" deterministically.
func describeGroups(groups map[string][]string) string {
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		nodes := append([]string(nil), groups[k]...)
		sort.Strings(nodes)
		parts = append(parts, fmt.Sprintf("%s (%s)", short(k), strings.Join(nodes, ", ")))
	}
	return strings.Join(parts, " vs ")
}

// short keeps a UUID or a fingerprint readable in one line of terminal.
func short(v string) string {
	if len(v) > 12 {
		return v[:12] + "…"
	}
	return v
}
